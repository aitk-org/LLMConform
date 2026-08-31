package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultMaxBodyBytes = 8 << 20
	maxStoredBodyBytes  = 12 << 10
)

// Runner executes the same compatibility checks against each configured route.
// The protocol-specific request and response rules live in protocol.go.
type Runner struct {
	Client        *http.Client
	MaxBodyBytes  int64
	MaxStoredBody int
}

// ProgressFunc receives a snapshot after every completed check.
type ProgressFunc func(Report)

type probeResponse struct {
	StatusCode  int
	ContentType string
	Headers     http.Header
	Body        []byte
	Events      []sseEvent
	Duration    time.Duration
	Err         error
}

func NewRunner() *Runner {
	return &Runner{
		Client:        &http.Client{},
		MaxBodyBytes:  defaultMaxBodyBytes,
		MaxStoredBody: maxStoredBodyBytes,
	}
}

func (r *Runner) Run(ctx context.Context, cfg RunConfig, onProgress ProgressFunc) Report {
	started := time.Now().UTC()
	report := Report{
		ID:        newReportID(),
		State:     StatusRunning,
		BaseURL:   cfg.BaseURL,
		Model:     cfg.Model,
		StartedAt: started,
		Progress:  Progress{Label: "准备开始"},
	}

	if err := cfg.Validate(); err != nil {
		report.State = StatusComplete
		report.Summary.Fail = 1
		report.Progress = Progress{Current: 1, Total: 1, Label: "配置校验"}
		report.Routes = []RouteResult{{
			ID:     "config",
			Name:   "配置校验",
			Path:   "-",
			Status: StatusFail,
			Checks: []CheckResult{{ID: "config", Name: "配置校验", Status: StatusFail, Summary: err.Error()}},
		}}
		finished := time.Now().UTC()
		report.FinishedAt = &finished
		if onProgress != nil {
			onProgress(cloneReport(report))
		}
		return report
	}

	report.BaseURL = cfg.BaseURL
	report.Model = cfg.Model
	report.Progress.Total = len(cfg.Routes) * len(allCheckIDs())
	for _, routeID := range cfg.Routes {
		protocol := protocolByID(routeID)
		route := RouteResult{ID: protocol.ID, Name: protocol.Name, Path: protocol.Path, Status: StatusPending}
		for _, checkID := range allCheckIDs() {
			route.Checks = append(route.Checks, CheckResult{ID: checkID, Name: checkDisplayName(checkID), Status: StatusPending})
		}
		report.Routes = append(report.Routes, route)
	}
	if onProgress != nil {
		onProgress(cloneReport(report))
	}

	current := 0
	for routeIndex := range report.Routes {
		protocol := protocolByID(report.Routes[routeIndex].ID)
		report.Routes[routeIndex].Status = StatusRunning
		for checkIndex, checkID := range allCheckIDs() {
			report.Routes[routeIndex].Checks[checkIndex].Status = StatusRunning
			report.Progress.Label = protocol.Name + " / " + checkDisplayName(checkID)
			if onProgress != nil {
				onProgress(cloneReport(report))
			}
			check := r.runCheck(ctx, cfg, protocol, checkID)
			report.Routes[routeIndex].Checks[checkIndex] = check
			if check.Status == StatusFail {
				report.Routes[routeIndex].Status = StatusFail
			} else if check.Status == StatusWarn && report.Routes[routeIndex].Status != StatusFail {
				report.Routes[routeIndex].Status = StatusWarn
			} else if report.Routes[routeIndex].Status == StatusRunning {
				report.Routes[routeIndex].Status = StatusPass
			}
			switch check.Status {
			case StatusPass:
				report.Summary.Pass++
			case StatusWarn:
				report.Summary.Warn++
			case StatusFail:
				report.Summary.Fail++
			case StatusSkip:
				report.Summary.Skip++
			}
			current++
			report.Progress = Progress{
				Current: current,
				Total:   report.Progress.Total,
				Label:   protocol.Name + " / " + check.Name,
			}
			if onProgress != nil {
				snapshot := cloneReport(report)
				onProgress(snapshot)
			}
		}
	}

	report.State = StatusComplete
	finished := time.Now().UTC()
	report.FinishedAt = &finished
	if onProgress != nil {
		onProgress(cloneReport(report))
	}
	return report
}

func (r *Runner) runCheck(ctx context.Context, cfg RunConfig, protocol protocolSpec, checkID string) CheckResult {
	started := time.Now()
	result := CheckResult{
		ID:     checkID,
		Name:   checkDisplayName(checkID),
		Status: StatusFail,
	}
	payload, err := protocol.buildRequest(checkID, cfg.Model)
	if err != nil {
		result.Summary = "无法构造测试请求：" + err.Error()
		result.DurationMS = time.Since(started).Milliseconds()
		return result
	}
	result.Request = truncate(string(payload), r.MaxStoredBody)
	stream := checkID == CheckStream
	probe := r.doProbe(ctx, cfg, protocol, payload, stream)
	result.HTTPStatus = probe.StatusCode
	result.DurationMS = probe.Duration.Milliseconds()
	if len(probe.Body) > 0 {
		result.Response = truncate(string(probe.Body), r.MaxStoredBody)
	}
	if probe.Err != nil {
		result.Summary = "请求失败：" + probe.Err.Error()
		return result
	}
	checked := protocol.validate(checkID, probe.StatusCode, probe.Headers, probe.Body, probe.Events)
	result.Status = checked.Status
	result.Summary = checked.Summary
	result.Expected = checked.Expected
	return result
}

func (r *Runner) doProbe(ctx context.Context, cfg RunConfig, protocol protocolSpec, payload []byte, stream bool) probeResponse {
	started := time.Now()
	result := probeResponse{}
	if r.Client == nil {
		r.Client = &http.Client{}
	}
	if r.MaxBodyBytes <= 0 {
		r.MaxBodyBytes = defaultMaxBodyBytes
	}
	endpoint := endpointURL(cfg.BaseURL, protocol.Path)
	if endpoint == "" {
		result.Err = fmt.Errorf("invalid base URL")
		result.Duration = time.Since(started)
		return result
	}
	requestCtx := ctx
	if cfg.Timeout > 0 {
		var cancel context.CancelFunc
		requestCtx, cancel = context.WithTimeout(ctx, cfg.Timeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		result.Err = err
		result.Duration = time.Since(started)
		return result
	}
	protocol.applyHeaders(req.Header, cfg.APIKey, stream)
	resp, err := r.Client.Do(req)
	if err != nil {
		result.Err = err
		result.Duration = time.Since(started)
		return result
	}
	defer resp.Body.Close()
	result.StatusCode = resp.StatusCode
	result.ContentType = resp.Header.Get("Content-Type")
	result.Headers = resp.Header.Clone()
	result.Body, result.Err = io.ReadAll(io.LimitReader(resp.Body, r.MaxBodyBytes+1))
	if result.Err == nil && int64(len(result.Body)) > r.MaxBodyBytes {
		result.Err = fmt.Errorf("response body exceeds %d bytes", r.MaxBodyBytes)
	}
	if stream && result.Err == nil {
		result.Events, result.Err = parseSSE(result.Body)
	}
	result.Duration = time.Since(started)
	return result
}

// endpointURL accepts both a host URL and a versioned base URL. Protocol paths
// already contain /v1, so /v1 is not duplicated when callers provide it.
func endpointURL(base, path string) string {
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(base), "/"))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return ""
	}
	basePath := strings.TrimRight(parsed.Path, "/")
	routePath := path
	if strings.HasSuffix(basePath, "/v1") && strings.HasPrefix(routePath, "/v1/") {
		routePath = strings.TrimPrefix(routePath, "/v1")
	}
	parsed.Path = basePath + routePath
	parsed.RawPath = ""
	return parsed.String()
}

func truncate(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit] + "…"
}

func newReportID() string {
	var data [8]byte
	if _, err := rand.Read(data[:]); err == nil {
		return "run_" + hex.EncodeToString(data[:])
	}
	return "run_" + strconv.FormatInt(time.Now().UnixNano(), 10)
}
