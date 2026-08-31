package main

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"
)

const (
	RouteChat      = "chat"
	RouteResponses = "responses"
	RouteMessages  = "messages"
)

const (
	CheckBasic  = "basic"
	CheckStream = "stream"
	CheckTools  = "tools"
	CheckUsage  = "usage"
	CheckErrors = "errors"
)

const (
	StatusPending  = "PENDING"
	StatusRunning  = "RUNNING"
	StatusComplete = "COMPLETE"
	StatusPass     = "PASS"
	StatusWarn     = "WARN"
	StatusFail     = "FAIL"
	StatusSkip     = "SKIP"
)

type RunConfig struct {
	BaseURL string        `json:"base_url"`
	APIKey  string        `json:"api_key,omitempty"`
	Model   string        `json:"model"`
	Routes  []string      `json:"routes"`
	Timeout time.Duration `json:"-"`
}

func (c *RunConfig) Validate() error {
	baseURL, err := normalizeBaseURL(c.BaseURL)
	if err != nil {
		return err
	}
	c.BaseURL = baseURL
	c.Model = strings.TrimSpace(c.Model)
	if c.Model == "" {
		return fmt.Errorf("model is required")
	}
	if c.Timeout <= 0 {
		c.Timeout = 60 * time.Second
	}
	if len(c.Routes) == 0 {
		c.Routes = allRouteIDs()
	}
	seen := make(map[string]bool, len(c.Routes))
	for _, route := range c.Routes {
		if !slices.Contains(allRouteIDs(), route) {
			return fmt.Errorf("unknown route %q", route)
		}
		if seen[route] {
			return fmt.Errorf("duplicate route %q", route)
		}
		seen[route] = true
	}
	return nil
}

func normalizeBaseURL(value string) (string, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(value), "/")
	if baseURL == "" {
		return "", fmt.Errorf("base URL is required")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", fmt.Errorf("base URL must be an absolute http or https URL")
	}
	return baseURL, nil
}

type Report struct {
	ID         string        `json:"id"`
	State      string        `json:"state"`
	BaseURL    string        `json:"base_url"`
	Model      string        `json:"model"`
	StartedAt  time.Time     `json:"started_at"`
	FinishedAt *time.Time    `json:"finished_at,omitempty"`
	Progress   Progress      `json:"progress"`
	Summary    Summary       `json:"summary"`
	Routes     []RouteResult `json:"routes"`
}

type Progress struct {
	Current int    `json:"current"`
	Total   int    `json:"total"`
	Label   string `json:"label"`
}

type Summary struct {
	Pass int `json:"pass"`
	Warn int `json:"warn"`
	Fail int `json:"fail"`
	Skip int `json:"skip"`
}

type RouteResult struct {
	ID     string        `json:"id"`
	Name   string        `json:"name"`
	Path   string        `json:"path"`
	Status string        `json:"status"`
	Checks []CheckResult `json:"checks"`
}

type CheckResult struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Summary    string `json:"summary,omitempty"`
	Expected   string `json:"expected,omitempty"`
	Request    string `json:"request,omitempty"`
	Response   string `json:"response,omitempty"`
	HTTPStatus int    `json:"http_status,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
}

type RunRequest struct {
	BaseURL string   `json:"base_url"`
	APIKey  string   `json:"api_key"`
	Model   string   `json:"model"`
	Routes  []string `json:"routes"`
}

type ModelListRequest struct {
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key"`
}

type ModelSummary struct {
	ID      string `json:"id"`
	OwnedBy string `json:"owned_by,omitempty"`
}

type ModelListResponse struct {
	Models []ModelSummary `json:"models"`
}

func allRouteIDs() []string {
	return []string{RouteChat, RouteResponses, RouteMessages}
}

func allCheckIDs() []string {
	return []string{CheckBasic, CheckStream, CheckTools, CheckUsage, CheckErrors}
}

func checkDisplayName(id string) string {
	switch id {
	case CheckBasic:
		return "基础请求"
	case CheckStream:
		return "流式响应"
	case CheckTools:
		return "工具调用"
	case CheckUsage:
		return "Usage"
	case CheckErrors:
		return "错误格式"
	default:
		return id
	}
}

func cloneReport(report Report) Report {
	clone := report
	clone.Routes = make([]RouteResult, len(report.Routes))
	for i, route := range report.Routes {
		clone.Routes[i] = route
		clone.Routes[i].Checks = append([]CheckResult(nil), route.Checks...)
	}
	return clone
}
