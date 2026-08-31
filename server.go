package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

//go:embed web/*
var webFiles embed.FS

type runJob struct {
	mu        sync.Mutex
	report    Report
	listeners map[chan Report]struct{}
	finished  bool
}

type runRegistry struct {
	mu   sync.RWMutex
	jobs map[string]*runJob
}

func serve(addr string, timeout time.Duration) error {
	static, err := fs.Sub(webFiles, "web")
	if err != nil {
		return err
	}
	registry := &runRegistry{jobs: make(map[string]*runJob)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "catalog_version": catalogVersion})
	})
	mux.HandleFunc("GET /api/test-catalog", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"version":  catalogVersion,
			"profiles": allProfiles(),
			"levels":   allLevels(),
			"routes":   allRouteIDs(),
		})
	})
	mux.HandleFunc("POST /api/preflight", func(w http.ResponseWriter, req *http.Request) {
		req.Body = http.MaxBytesReader(w, req.Body, 1<<20)
		var input PreflightRequest
		decoder := json.NewDecoder(req.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
			return
		}
		baseURL, err := normalizeBaseURL(input.BaseURL)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
			return
		}
		ctx, cancel := context.WithTimeout(req.Context(), timeout)
		defer cancel()
		models, modelErr := fetchModels(ctx, &http.Client{}, ModelListRequest{BaseURL: baseURL, APIKey: input.APIKey})
		response := PreflightResponse{BaseURL: baseURL, Models: models}
		if modelErr != nil {
			if isOptionalModelListError(modelErr) {
				response.Warning = "目标可以访问，但没有可用的 /v1/models；请手动填写模型。"
				writeJSON(w, http.StatusOK, response)
				return
			}
			writeAPIError(w, http.StatusBadGateway, "preflight_error", modelErr.Error())
			return
		}
		writeJSON(w, http.StatusOK, response)
	})
	mux.HandleFunc("POST /api/models", func(w http.ResponseWriter, req *http.Request) {
		req.Body = http.MaxBytesReader(w, req.Body, 1<<20)
		var input ModelListRequest
		decoder := json.NewDecoder(req.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
			return
		}
		ctx, cancel := context.WithTimeout(req.Context(), timeout)
		defer cancel()
		models, err := fetchModels(ctx, &http.Client{}, input)
		if err != nil {
			writeAPIError(w, http.StatusBadGateway, "model_list_error", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, ModelListResponse{Models: models})
	})
	mux.HandleFunc("POST /api/run-plans", func(w http.ResponseWriter, req *http.Request) {
		req.Body = http.MaxBytesReader(w, req.Body, 1<<20)
		input, ok := decodeRunRequest(w, req)
		if !ok {
			return
		}
		cfg := runConfigFromRequest(input, timeout)
		plan, err := BuildRunPlan(cfg)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, plan)
	})
	mux.HandleFunc("POST /api/runs", func(w http.ResponseWriter, req *http.Request) {
		req.Body = http.MaxBytesReader(w, req.Body, 1<<20)
		input, ok := decodeRunRequest(w, req)
		if !ok {
			return
		}
		cfg := runConfigFromRequest(input, timeout)
		if err := cfg.Validate(); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
			return
		}
		job := newRunJob(cfg)
		registry.mu.Lock()
		registry.jobs[job.report.ID] = job
		registry.mu.Unlock()
		writeJSON(w, http.StatusAccepted, cloneReport(job.report))

		go func() {
			report := NewRunner().Run(context.Background(), cfg, func(snapshot Report) {
				job.publish(snapshot)
			})
			job.publish(report)
		}()
	})
	mux.HandleFunc("GET /api/runs/", func(w http.ResponseWriter, req *http.Request) {
		path := strings.TrimPrefix(req.URL.Path, "/api/runs/")
		if strings.HasSuffix(path, "/events") {
			serveRunEvents(w, req, registry, strings.TrimSuffix(path, "/events"))
			return
		}
		if strings.HasSuffix(path, "/report") {
			serveRunReport(w, registry, strings.TrimSuffix(path, "/report"))
			return
		}
		serveRunSnapshot(w, registry, path)
	})
	mux.Handle("GET /", http.FileServer(http.FS(static)))

	server := &http.Server{
		Addr:              addr,
		Handler:           securityHeaders(mux),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       90 * time.Second,
	}
	slog.Info("LLMConform is ready", "url", "http://"+addr)
	err = server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func newRunJob(cfg RunConfig) *runJob {
	now := time.Now().UTC()
	plan, _ := BuildRunPlan(cfg)
	report := newReport(now, plan)
	return &runJob{report: report, listeners: make(map[chan Report]struct{})}
}

func (job *runJob) publish(report Report) {
	job.mu.Lock()
	if report.ID == "" || report.ID != job.report.ID {
		report.ID = job.report.ID
	}
	job.report = cloneReport(report)
	if job.report.FinishedAt != nil || job.report.State == StatusComplete {
		job.finished = true
	}
	snapshot := cloneReport(job.report)
	listeners := make([]chan Report, 0, len(job.listeners))
	for listener := range job.listeners {
		listeners = append(listeners, listener)
	}
	job.mu.Unlock()
	for _, listener := range listeners {
		select {
		case listener <- snapshot:
		default:
		}
	}
}

func (job *runJob) snapshot() Report {
	job.mu.Lock()
	defer job.mu.Unlock()
	return cloneReport(job.report)
}

func (job *runJob) subscribe() (chan Report, Report) {
	channel := make(chan Report, 4)
	job.mu.Lock()
	job.listeners[channel] = struct{}{}
	snapshot := cloneReport(job.report)
	job.mu.Unlock()
	return channel, snapshot
}

func (job *runJob) unsubscribe(channel chan Report) {
	job.mu.Lock()
	delete(job.listeners, channel)
	job.mu.Unlock()
}

func (registry *runRegistry) get(id string) (*runJob, bool) {
	registry.mu.RLock()
	job, ok := registry.jobs[id]
	registry.mu.RUnlock()
	return job, ok
}

func serveRunSnapshot(w http.ResponseWriter, registry *runRegistry, id string) {
	job, ok := registry.get(id)
	if !ok {
		writeAPIError(w, http.StatusNotFound, "not_found_error", "run not found")
		return
	}
	writeJSON(w, http.StatusOK, job.snapshot())
}

func serveRunReport(w http.ResponseWriter, registry *runRegistry, id string) {
	job, ok := registry.get(id)
	if !ok {
		writeAPIError(w, http.StatusNotFound, "not_found_error", "run not found")
		return
	}
	report := job.snapshot()
	if report.State != StatusComplete {
		writeAPIError(w, http.StatusConflict, "run_incomplete", "run is not complete")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", report.ID+".json"))
	_ = json.NewEncoder(w).Encode(report)
}

func serveRunEvents(w http.ResponseWriter, req *http.Request, registry *runRegistry, id string) {
	job, ok := registry.get(id)
	if !ok {
		writeAPIError(w, http.StatusNotFound, "not_found_error", "run not found")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeAPIError(w, http.StatusInternalServerError, "server_error", "streaming is not supported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	listener, snapshot := job.subscribe()
	defer job.unsubscribe(listener)
	writeReportEvent(w, flusher, snapshot)
	if snapshot.State == StatusComplete {
		return
	}
	for {
		select {
		case <-req.Context().Done():
			return
		case report, open := <-listener:
			if !open {
				return
			}
			writeReportEvent(w, flusher, report)
			if report.State == StatusComplete {
				return
			}
		}
	}
}

func writeReportEvent(w http.ResponseWriter, flusher http.Flusher, report Report) {
	body, err := json.Marshal(report)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "event: report\ndata: %s\n\n", body)
	flusher.Flush()
}

func writeAPIError(w http.ResponseWriter, status int, errorType, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{"type": errorType, "message": message},
	})
}

func decodeRunRequest(w http.ResponseWriter, req *http.Request) (RunRequest, bool) {
	var input RunRequest
	decoder := json.NewDecoder(req.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return RunRequest{}, false
	}
	return input, true
}

func runConfigFromRequest(input RunRequest, timeout time.Duration) RunConfig {
	return RunConfig{
		BaseURL: input.BaseURL,
		APIKey:  input.APIKey,
		Model:   input.Model,
		Profile: input.Profile,
		Level:   input.Level,
		Routes:  input.Routes,
		Timeout: timeout,
	}
}

func isOptionalModelListError(err error) bool {
	message := err.Error()
	return strings.Contains(message, "HTTP 404") ||
		strings.Contains(message, "HTTP 405") ||
		strings.Contains(message, "没有可用的 model id")
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; base-uri 'none'; frame-ancestors 'none'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}
