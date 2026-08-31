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
	ProfileOpenAI  = "openai"
	ProfileClaude  = "claude"
	ProfileGateway = "gateway"
	ProfileCustom  = "custom"
)

const (
	LevelQuick    = "quick"
	LevelStandard = "standard"
	LevelFull     = "full"
)

const (
	CapabilityRoute   = "route"
	CapabilityBasic   = "basic"
	CapabilityContext = "context"
	CapabilityStream  = "stream"
	CapabilityTools   = "tools"
	CapabilityErrors  = "errors"
)

const (
	SeverityRequired = "required"
	SeverityAdvisory = "advisory"
)

const (
	StatusPending  = "PENDING"
	StatusRunning  = "RUNNING"
	StatusComplete = "COMPLETE"
	StatusPass     = "PASS"
	StatusWarn     = "WARN"
	StatusFail     = "FAIL"
	StatusSkip     = "SKIP"
	StatusBlocked  = "BLOCKED"
	StatusError    = "ERROR"
)

type RunConfig struct {
	BaseURL string        `json:"base_url"`
	APIKey  string        `json:"api_key,omitempty"`
	Model   string        `json:"model"`
	Profile string        `json:"profile"`
	Level   string        `json:"level"`
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

	c.Profile = strings.ToLower(strings.TrimSpace(c.Profile))
	if c.Profile == "" {
		c.Profile = inferProfile(c.Routes)
	}
	if !slices.Contains(allProfiles(), c.Profile) {
		return fmt.Errorf("unknown profile %q", c.Profile)
	}

	c.Level = strings.ToLower(strings.TrimSpace(c.Level))
	if c.Level == "" {
		c.Level = LevelStandard
	}
	if !slices.Contains(allLevels(), c.Level) {
		return fmt.Errorf("unknown test level %q", c.Level)
	}

	if len(c.Routes) == 0 {
		c.Routes = defaultRoutesForProfile(c.Profile)
	}
	seen := make(map[string]bool, len(c.Routes))
	normalized := make([]string, 0, len(c.Routes))
	for _, route := range c.Routes {
		route = strings.ToLower(strings.TrimSpace(route))
		if !slices.Contains(allRouteIDs(), route) {
			return fmt.Errorf("unknown route %q", route)
		}
		if seen[route] {
			return fmt.Errorf("duplicate route %q", route)
		}
		seen[route] = true
		normalized = append(normalized, route)
	}
	if len(normalized) == 0 {
		return fmt.Errorf("at least one route is required")
	}
	c.Routes = normalized
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

func inferProfile(routes []string) string {
	if len(routes) == 1 && routes[0] == RouteMessages {
		return ProfileClaude
	}
	if len(routes) == 2 && slices.Contains(routes, RouteChat) && slices.Contains(routes, RouteResponses) {
		return ProfileOpenAI
	}
	if len(routes) == 0 || len(routes) == len(allRouteIDs()) {
		return ProfileGateway
	}
	return ProfileCustom
}

func defaultRoutesForProfile(profile string) []string {
	switch profile {
	case ProfileOpenAI:
		return []string{RouteChat, RouteResponses}
	case ProfileClaude:
		return []string{RouteMessages}
	default:
		return allRouteIDs()
	}
}

func allProfiles() []string {
	return []string{ProfileOpenAI, ProfileClaude, ProfileGateway, ProfileCustom}
}

func allLevels() []string {
	return []string{LevelQuick, LevelStandard, LevelFull}
}

func allRouteIDs() []string {
	return []string{RouteChat, RouteResponses, RouteMessages}
}

type AssertionPlan struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Severity string `json:"severity"`
}

type PlannedCase struct {
	ID              string          `json:"id"`
	Name            string          `json:"name"`
	Description     string          `json:"description"`
	RouteID         string          `json:"route_id"`
	RouteName       string          `json:"route_name"`
	Capability      string          `json:"capability"`
	Level           string          `json:"level"`
	DependsOn       []string        `json:"depends_on,omitempty"`
	Assertions      []AssertionPlan `json:"assertions"`
	ModelCalls      int             `json:"model_calls"`
	MaxOutputTokens int             `json:"max_output_tokens"`
}

type RunPlan struct {
	CatalogVersion  string        `json:"catalog_version"`
	Profile         string        `json:"profile"`
	Level           string        `json:"level"`
	BaseURL         string        `json:"base_url"`
	Model           string        `json:"model"`
	Routes          []string      `json:"routes"`
	Cases           []PlannedCase `json:"cases"`
	ScenarioCount   int           `json:"scenario_count"`
	AssertionCount  int           `json:"assertion_count"`
	ModelCalls      int           `json:"model_calls"`
	MaxOutputTokens int           `json:"max_output_tokens"`
}

type Report struct {
	ID             string        `json:"id"`
	State          string        `json:"state"`
	CatalogVersion string        `json:"catalog_version"`
	Plan           RunPlan       `json:"plan"`
	BaseURL        string        `json:"base_url"`
	Model          string        `json:"model"`
	StartedAt      time.Time     `json:"started_at"`
	FinishedAt     *time.Time    `json:"finished_at,omitempty"`
	Progress       Progress      `json:"progress"`
	Summary        Summary       `json:"summary"`
	Routes         []RouteResult `json:"routes"`
}

type Progress struct {
	Current           int    `json:"current"`
	Total             int    `json:"total"`
	AssertionsCurrent int    `json:"assertions_current"`
	AssertionsTotal   int    `json:"assertions_total"`
	Label             string `json:"label"`
}

type StatusCounts struct {
	Pass    int `json:"pass"`
	Warn    int `json:"warn"`
	Fail    int `json:"fail"`
	Skip    int `json:"skip"`
	Blocked int `json:"blocked"`
	Error   int `json:"error"`
}

type Summary struct {
	StatusCounts
	Assertions StatusCounts `json:"assertions"`
}

type RouteResult struct {
	ID     string       `json:"id"`
	Name   string       `json:"name"`
	Path   string       `json:"path"`
	Status string       `json:"status"`
	Cases  []CaseResult `json:"cases"`
}

type CaseResult struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Capability string            `json:"capability"`
	Status     string            `json:"status"`
	Summary    string            `json:"summary,omitempty"`
	ReasonCode string            `json:"reason_code,omitempty"`
	DependsOn  []string          `json:"depends_on,omitempty"`
	Assertions []AssertionResult `json:"assertions"`
	Evidence   []Exchange        `json:"evidence,omitempty"`
}

type AssertionResult struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Severity   string `json:"severity"`
	Status     string `json:"status"`
	ReasonCode string `json:"reason_code,omitempty"`
	Expected   string `json:"expected,omitempty"`
	Observed   string `json:"observed,omitempty"`
}

type Exchange struct {
	Label      string `json:"label"`
	Request    string `json:"request,omitempty"`
	Response   string `json:"response,omitempty"`
	HTTPStatus int    `json:"http_status,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
}

type RunRequest struct {
	BaseURL string   `json:"base_url"`
	APIKey  string   `json:"api_key"`
	Model   string   `json:"model"`
	Profile string   `json:"profile"`
	Level   string   `json:"level"`
	Routes  []string `json:"routes"`
}

type PreflightRequest struct {
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key"`
	Profile string `json:"profile"`
}

type PreflightResponse struct {
	BaseURL string         `json:"base_url"`
	Models  []ModelSummary `json:"models"`
	Warning string         `json:"warning,omitempty"`
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

func cloneReport(report Report) Report {
	clone := report
	clone.Plan.Routes = append([]string(nil), report.Plan.Routes...)
	clone.Plan.Cases = make([]PlannedCase, len(report.Plan.Cases))
	for i, item := range report.Plan.Cases {
		clone.Plan.Cases[i] = item
		clone.Plan.Cases[i].DependsOn = append([]string(nil), item.DependsOn...)
		clone.Plan.Cases[i].Assertions = append([]AssertionPlan(nil), item.Assertions...)
	}
	clone.Routes = make([]RouteResult, len(report.Routes))
	for i, route := range report.Routes {
		clone.Routes[i] = route
		clone.Routes[i].Cases = make([]CaseResult, len(route.Cases))
		for j, result := range route.Cases {
			clone.Routes[i].Cases[j] = result
			clone.Routes[i].Cases[j].DependsOn = append([]string(nil), result.DependsOn...)
			clone.Routes[i].Cases[j].Assertions = append([]AssertionResult(nil), result.Assertions...)
			clone.Routes[i].Cases[j].Evidence = append([]Exchange(nil), result.Evidence...)
		}
	}
	return clone
}
