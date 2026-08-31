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

type Runner struct {
	Client        *http.Client
	MaxBodyBytes  int64
	MaxStoredBody int
}

type ProgressFunc func(Report)

type probeResponse struct {
	StatusCode int
	Headers    http.Header
	Body       []byte
	Events     []sseEvent
	Duration   time.Duration
	Err        error
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
	plan, err := BuildRunPlan(cfg)
	if err != nil {
		return configErrorReport(started, err, onProgress)
	}
	report := newReport(started, plan)
	publish(onProgress, report)

	completedCases := make(map[string]string, len(plan.Cases))
	for routeIndex := range report.Routes {
		route := &report.Routes[routeIndex]
		protocol := protocolByID(route.ID)
		for caseIndex := range route.Cases {
			planned := plannedCaseByID(plan, route.Cases[caseIndex].ID)
			route.Cases[caseIndex].Status = StatusRunning
			report.Progress.Label = protocol.Name + " / " + planned.Name
			publish(onProgress, report)

			var result CaseResult
			if dependency, blocked := blockedBy(planned.DependsOn, completedCases); blocked {
				result = blockedCase(planned, dependency)
			} else {
				result = r.runCase(ctx, cfg, protocol, planned)
			}
			route.Cases[caseIndex] = result
			completedCases[result.ID] = result.Status
			route.Status = routeStatus(route.Cases)
			report.Progress.Current++
			report.Progress.AssertionsCurrent += len(result.Assertions)
			report.Progress.Label = protocol.Name + " / " + result.Name
			report.Summary = summarize(report.Routes)
			publish(onProgress, report)
		}
	}

	report.State = StatusComplete
	finished := time.Now().UTC()
	report.FinishedAt = &finished
	report.Summary = summarize(report.Routes)
	publish(onProgress, report)
	return report
}

func newReport(started time.Time, plan RunPlan) Report {
	report := Report{
		ID:             newReportID(),
		State:          StatusRunning,
		CatalogVersion: plan.CatalogVersion,
		Plan:           plan,
		BaseURL:        plan.BaseURL,
		Model:          plan.Model,
		StartedAt:      started,
		Progress: Progress{
			Total:           plan.ScenarioCount,
			AssertionsTotal: plan.AssertionCount,
			Label:           "准备执行测试计划",
		},
	}
	for _, routeID := range plan.Routes {
		protocol := protocolByID(routeID)
		route := RouteResult{ID: routeID, Name: protocol.Name, Path: protocol.Path, Status: StatusPending}
		for _, planned := range plan.Cases {
			if planned.RouteID != routeID {
				continue
			}
			result := CaseResult{
				ID:         planned.ID,
				Name:       planned.Name,
				Capability: planned.Capability,
				Status:     StatusPending,
				DependsOn:  append([]string(nil), planned.DependsOn...),
			}
			for _, assertion := range planned.Assertions {
				result.Assertions = append(result.Assertions, AssertionResult{
					ID:       assertion.ID,
					Name:     assertion.Name,
					Severity: assertion.Severity,
					Status:   StatusPending,
				})
			}
			route.Cases = append(route.Cases, result)
		}
		report.Routes = append(report.Routes, route)
	}
	return report
}

func configErrorReport(started time.Time, validationErr error, onProgress ProgressFunc) Report {
	finished := time.Now().UTC()
	report := Report{
		ID:         newReportID(),
		State:      StatusComplete,
		StartedAt:  started,
		FinishedAt: &finished,
		Progress:   Progress{Current: 1, Total: 1, Label: "配置校验失败"},
		Summary:    Summary{StatusCounts: StatusCounts{Error: 1}},
		Routes: []RouteResult{{
			ID:     "config",
			Name:   "配置",
			Path:   "-",
			Status: StatusError,
			Cases: []CaseResult{{
				ID:         "config.validate",
				Name:       "配置校验",
				Capability: CapabilityRoute,
				Status:     StatusError,
				Summary:    validationErr.Error(),
				ReasonCode: "config.invalid",
			}},
		}},
	}
	publish(onProgress, report)
	return report
}

func (r *Runner) runCase(ctx context.Context, cfg RunConfig, protocol protocolSpec, planned PlannedCase) CaseResult {
	result := CaseResult{
		ID:         planned.ID,
		Name:       planned.Name,
		Capability: planned.Capability,
		Status:     StatusError,
		DependsOn:  append([]string(nil), planned.DependsOn...),
	}
	definition, ok := findCaseDefinition(protocol.ID, planned.ID)
	if !ok {
		result.Summary = "测试目录中找不到场景定义。"
		result.ReasonCode = "catalog.case_missing"
		return result
	}
	payload, err := protocol.buildRequest(definition.Kind, cfg.Model)
	if err != nil {
		result.Summary = "无法构造测试请求：" + err.Error()
		result.ReasonCode = "request.build_failed"
		return result
	}
	stream := definition.Kind == caseStreamText
	probe := r.doProbe(ctx, cfg, protocol, payload, stream)
	result.Evidence = []Exchange{{
		Label:      "请求与响应",
		Request:    truncate(string(payload), r.MaxStoredBody),
		Response:   truncate(string(probe.Body), r.MaxStoredBody),
		HTTPStatus: probe.StatusCode,
		DurationMS: probe.Duration.Milliseconds(),
	}}
	result.Assertions = protocol.validate(definition, probe)
	result.Status, result.Summary, result.ReasonCode = caseOutcome(result.Assertions)
	return result
}

func blockedCase(planned PlannedCase, dependency string) CaseResult {
	result := CaseResult{
		ID:         planned.ID,
		Name:       planned.Name,
		Capability: planned.Capability,
		Status:     StatusBlocked,
		Summary:    "前置场景未通过，本场景没有执行。",
		ReasonCode: "dependency.blocked",
		DependsOn:  append([]string(nil), planned.DependsOn...),
	}
	for _, assertion := range planned.Assertions {
		result.Assertions = append(result.Assertions, AssertionResult{
			ID:         assertion.ID,
			Name:       assertion.Name,
			Severity:   assertion.Severity,
			Status:     StatusBlocked,
			ReasonCode: "dependency.blocked:" + dependency,
		})
	}
	return result
}

func blockedBy(dependencies []string, completed map[string]string) (string, bool) {
	for _, dependency := range dependencies {
		status, exists := completed[dependency]
		if !exists || (status != StatusPass && status != StatusWarn) {
			return dependency, true
		}
	}
	return "", false
}

func caseOutcome(assertions []AssertionResult) (status, summary, reason string) {
	if len(assertions) == 0 {
		return StatusError, "场景没有产生断言结果。", "assertions.empty"
	}
	pass, warn, fail, blocked := 0, 0, 0, 0
	for _, assertion := range assertions {
		switch assertion.Status {
		case StatusPass:
			pass++
		case StatusWarn:
			warn++
			if reason == "" {
				reason = assertion.ReasonCode
			}
		case StatusFail:
			fail++
			if reason == "" {
				reason = assertion.ReasonCode
			}
		case StatusBlocked:
			blocked++
		}
	}
	summary = fmt.Sprintf("%d/%d 个断言通过", pass, len(assertions))
	if fail > 0 {
		return StatusFail, summary + fmt.Sprintf("，%d 个失败。", fail), reason
	}
	if warn > 0 {
		return StatusWarn, summary + fmt.Sprintf("，%d 个建议项未满足。", warn), reason
	}
	if blocked > 0 {
		return StatusError, summary + fmt.Sprintf("，%d 个断言无法执行。", blocked), "assertions.incomplete"
	}
	return StatusPass, summary + "。", ""
}

func routeStatus(cases []CaseResult) string {
	status := StatusPass
	seenCompleted := false
	for _, result := range cases {
		switch result.Status {
		case StatusError:
			return StatusError
		case StatusFail:
			status = StatusFail
			seenCompleted = true
		case StatusWarn:
			if status == StatusPass {
				status = StatusWarn
			}
			seenCompleted = true
		case StatusBlocked:
			if !seenCompleted && status == StatusPass {
				status = StatusBlocked
			}
		case StatusPass:
			seenCompleted = true
		case StatusPending, StatusRunning:
			if !seenCompleted {
				status = result.Status
			}
		}
	}
	return status
}

func summarize(routes []RouteResult) Summary {
	var summary Summary
	for _, route := range routes {
		for _, result := range route.Cases {
			incrementStatus(&summary.StatusCounts, result.Status)
			for _, assertion := range result.Assertions {
				incrementStatus(&summary.Assertions, assertion.Status)
			}
		}
	}
	return summary
}

func incrementStatus(counts *StatusCounts, status string) {
	switch status {
	case StatusPass:
		counts.Pass++
	case StatusWarn:
		counts.Warn++
	case StatusFail:
		counts.Fail++
	case StatusSkip:
		counts.Skip++
	case StatusBlocked:
		counts.Blocked++
	case StatusError:
		counts.Error++
	}
}

func plannedCaseByID(plan RunPlan, id string) PlannedCase {
	for _, planned := range plan.Cases {
		if planned.ID == id {
			return planned
		}
	}
	return PlannedCase{ID: id, Name: id}
}

func publish(onProgress ProgressFunc, report Report) {
	if onProgress != nil {
		onProgress(cloneReport(report))
	}
}

func (r *Runner) doProbe(ctx context.Context, cfg RunConfig, protocol protocolSpec, payload []byte, stream bool) probeResponse {
	started := time.Now()
	result := probeResponse{Headers: make(http.Header)}
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
	return strings.ToValidUTF8(value[:limit], "�") + "…"
}

func newReportID() string {
	var data [8]byte
	if _, err := rand.Read(data[:]); err == nil {
		return "run_" + hex.EncodeToString(data[:])
	}
	return "run_" + strconv.FormatInt(time.Now().UnixNano(), 10)
}
