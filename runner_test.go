package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunnerExecutesStandardCatalog(t *testing.T) {
	t.Parallel()
	server := newIPv4TestServer(t, http.HandlerFunc(strictMockProvider))
	cfg := RunConfig{
		BaseURL: server.URL,
		APIKey:  "test-secret",
		Model:   "test-model",
		Profile: ProfileGateway,
		Level:   LevelStandard,
		Routes:  allRouteIDs(),
		Timeout: 2 * time.Second,
	}
	report := NewRunner().Run(context.Background(), cfg, nil)
	if report.State != StatusComplete {
		t.Fatalf("state = %q, want COMPLETE", report.State)
	}
	if report.Plan.ScenarioCount != 24 || report.Plan.ModelCalls != 9 {
		t.Fatalf("plan = %+v", report.Plan)
	}
	if report.Summary.Pass != 24 || report.Summary.Warn != 0 || report.Summary.Fail != 0 || report.Summary.Blocked != 0 || report.Summary.Error != 0 {
		for _, route := range report.Routes {
			for _, result := range route.Cases {
				if result.Status != StatusPass {
					t.Logf("%s/%s: %s (%s)", route.ID, result.ID, result.Summary, result.ReasonCode)
					for _, assertion := range result.Assertions {
						if assertion.Status != StatusPass {
							t.Logf("  %s: %s (%s), observed=%s", assertion.ID, assertion.Status, assertion.ReasonCode, assertion.Observed)
						}
					}
				}
			}
		}
		t.Fatalf("summary = %+v", report.Summary)
	}
	if report.Progress.Current != 24 || report.Progress.AssertionsCurrent != report.Plan.AssertionCount {
		t.Fatalf("progress = %+v", report.Progress)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), cfg.APIKey) {
		t.Fatal("report contains API key")
	}
}

func TestRunnerExecutesFullCatalog(t *testing.T) {
	t.Parallel()
	server := newIPv4TestServer(t, http.HandlerFunc(strictMockProvider))
	report := NewRunner().Run(context.Background(), RunConfig{
		BaseURL: server.URL,
		APIKey:  "test-secret",
		Model:   "test-model",
		Profile: ProfileOpenAI,
		Level:   LevelFull,
		Routes:  []string{RouteChat},
		Timeout: 2 * time.Second,
	}, nil)
	if report.Plan.ScenarioCount != 10 || len(report.Routes) != 1 || len(report.Routes[0].Cases) != 10 {
		t.Fatalf("unexpected full plan: %+v", report.Plan)
	}
	if report.Summary.Pass != 10 || report.Summary.Warn != 0 || report.Summary.Fail != 0 || report.Summary.Blocked != 0 || report.Summary.Error != 0 {
		t.Fatalf("unexpected full report summary: %+v", report.Summary)
	}
}

func TestRunnerBlocksDependentCases(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := newIPv4TestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.NotFound(w, nil)
	}))
	report := NewRunner().Run(context.Background(), RunConfig{
		BaseURL: server.URL,
		Model:   "test-model",
		Profile: ProfileCustom,
		Level:   LevelStandard,
		Routes:  []string{RouteChat},
		Timeout: time.Second,
	}, nil)
	if requests.Load() != 1 {
		t.Fatalf("requests = %d, want 1", requests.Load())
	}
	if report.Summary.Fail != 1 || report.Summary.Blocked != 7 {
		t.Fatalf("summary = %+v", report.Summary)
	}
	if report.Routes[0].Cases[0].ReasonCode != "route.not_found" {
		t.Fatalf("first result = %+v", report.Routes[0].Cases[0])
	}
}

func TestEndpointURLAcceptsVersionedBase(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"https://example.com":        "https://example.com/v1/responses",
		"https://example.com/":       "https://example.com/v1/responses",
		"https://example.com/v1":     "https://example.com/v1/responses",
		"https://example.com/api/v1": "https://example.com/api/v1/responses",
	}
	for base, want := range tests {
		if got := endpointURL(base, "/v1/responses"); got != want {
			t.Errorf("endpointURL(%q) = %q, want %q", base, got, want)
		}
	}
}

func strictMockProvider(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	routeID, ok := routeForPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
		http.Error(w, "invalid transport", http.StatusBadRequest)
		return
	}
	if routeID == RouteMessages {
		if r.Header.Get("x-api-key") != "test-secret" || r.Header.Get("anthropic-version") == "" || r.Header.Get("Authorization") != "" {
			http.Error(w, "invalid anthropic auth", http.StatusUnauthorized)
			return
		}
	} else if r.Header.Get("Authorization") != "Bearer test-secret" {
		http.Error(w, "invalid bearer auth", http.StatusUnauthorized)
		return
	}

	var payload map[string]any
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if len(payload) == 0 {
		writeMockError(w, routeID, "model is required")
		return
	}
	if _, exists := payload["model"]; !exists {
		writeMockError(w, routeID, "model is required")
		return
	}
	input := inputField(routeID)
	if _, exists := payload[input]; !exists {
		writeMockError(w, routeID, input+" is required")
		return
	}
	if hasInvalidRole(payload[input]) {
		writeMockError(w, routeID, "role is invalid")
		return
	}
	if limit, exists := payload[tokenLimitField(routeID)].(float64); exists && limit < 0 {
		writeMockError(w, routeID, "token limit is invalid")
		return
	}
	if stream, _ := payload["stream"].(bool); stream {
		if r.Header.Get("Accept") != "text/event-stream" {
			http.Error(w, "stream request must accept SSE", http.StatusBadRequest)
			return
		}
		writeStrictMockStream(w, routeID)
		return
	}
	if _, tools := payload["tools"]; tools {
		if !validToolRequest(routeID, payload) {
			http.Error(w, "invalid tool request", http.StatusBadRequest)
			return
		}
		writeStrictMockTools(w, routeID)
		return
	}
	writeStrictMockBasic(w, routeID)
}

func validToolRequest(routeID string, payload map[string]any) bool {
	tools, ok := payload["tools"].([]any)
	if !ok || len(tools) != 1 {
		return false
	}
	tool, ok := tools[0].(map[string]any)
	if !ok {
		return false
	}
	choice, ok := payload["tool_choice"].(map[string]any)
	if !ok {
		return false
	}
	var name string
	var schema map[string]any
	var strict bool
	switch routeID {
	case RouteChat:
		function, _ := tool["function"].(map[string]any)
		name, _ = function["name"].(string)
		schema, _ = function["parameters"].(map[string]any)
		strict, _ = function["strict"].(bool)
		choiceFunction, _ := choice["function"].(map[string]any)
		if tool["type"] != "function" || choice["type"] != "function" || choiceFunction["name"] != "get_weather" {
			return false
		}
	case RouteResponses:
		name, _ = tool["name"].(string)
		schema, _ = tool["parameters"].(map[string]any)
		strict, _ = tool["strict"].(bool)
		if tool["type"] != "function" || choice["type"] != "function" || choice["name"] != "get_weather" {
			return false
		}
	case RouteMessages:
		name, _ = tool["name"].(string)
		schema, _ = tool["input_schema"].(map[string]any)
		strict, _ = tool["strict"].(bool)
		if choice["type"] != "tool" || choice["name"] != "get_weather" {
			return false
		}
	}
	properties, _ := schema["properties"].(map[string]any)
	city, _ := properties["city"].(map[string]any)
	required, _ := schema["required"].([]any)
	return name == "get_weather" && strict && schema["type"] == "object" && schema["additionalProperties"] == false &&
		city["type"] == "string" && len(required) == 1 && required[0] == "city"
}

func routeForPath(path string) (string, bool) {
	switch path {
	case "/v1/chat/completions":
		return RouteChat, true
	case "/v1/responses":
		return RouteResponses, true
	case "/v1/messages":
		return RouteMessages, true
	default:
		return "", false
	}
}

func hasInvalidRole(value any) bool {
	messages, ok := value.([]any)
	if !ok || len(messages) == 0 {
		return false
	}
	for _, raw := range messages {
		message, ok := raw.(map[string]any)
		if !ok {
			return true
		}
		if _, isString := message["role"].(string); !isString {
			return true
		}
	}
	return false
}

func writeMockError(w http.ResponseWriter, routeID, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	if routeID == RouteMessages {
		_, _ = fmt.Fprintf(w, `{"type":"error","error":{"type":"invalid_request_error","message":%q}}`, message)
		return
	}
	_, _ = fmt.Fprintf(w, `{"error":{"type":"invalid_request_error","message":%q}}`, message)
}

func writeStrictMockBasic(w http.ResponseWriter, routeID string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	switch routeID {
	case RouteChat:
		_, _ = w.Write([]byte(`{"id":"chatcmpl_1","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}],"usage":{"prompt_tokens":8,"completion_tokens":1,"total_tokens":9}}`))
	case RouteResponses:
		_, _ = w.Write([]byte(`{"id":"resp_1","object":"response","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"pong"}]}],"usage":{"input_tokens":8,"output_tokens":1,"total_tokens":9}}`))
	case RouteMessages:
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"pong"}],"stop_reason":"end_turn","usage":{"input_tokens":8,"output_tokens":1}}`))
	}
}

func writeStrictMockTools(w http.ResponseWriter, routeID string) {
	w.Header().Set("Content-Type", "application/json")
	switch routeID {
	case RouteChat:
		_, _ = w.Write([]byte(`{"id":"chatcmpl_tool","object":"chat.completion","choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Shanghai\"}"}}]},"finish_reason":"tool_calls"}]}`))
	case RouteResponses:
		_, _ = w.Write([]byte(`{"id":"resp_tool","object":"response","status":"completed","output":[{"id":"fc_1","type":"function_call","call_id":"call_1","name":"get_weather","arguments":"{\"city\":\"Shanghai\"}"}]}`))
	case RouteMessages:
		_, _ = w.Write([]byte(`{"id":"msg_tool","type":"message","role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"get_weather","input":{"city":"Shanghai"}}],"stop_reason":"tool_use"}`))
	}
}

func writeStrictMockStream(w http.ResponseWriter, routeID string) {
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	switch routeID {
	case RouteChat:
		_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl_stream\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"delta\":{\"role\":\"assistant\"},\"finish_reason\":null}]}\n\n" +
			"data: {\"id\":\"chatcmpl_stream\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"delta\":{\"content\":\"pong\"},\"finish_reason\":null}]}\n\n" +
			"data: {\"id\":\"chatcmpl_stream\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
			"data: {\"id\":\"chatcmpl_stream\",\"object\":\"chat.completion.chunk\",\"choices\":[],\"usage\":{\"prompt_tokens\":8,\"completion_tokens\":1,\"total_tokens\":9}}\n\n" +
			"data: [DONE]\n\n"))
	case RouteResponses:
		_, _ = w.Write([]byte("event: response.created\ndata: {\"type\":\"response.created\",\"sequence_number\":0,\"response\":{\"id\":\"resp_stream\",\"status\":\"in_progress\"}}\n\n" +
			"event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"sequence_number\":1,\"output_index\":0,\"item\":{\"id\":\"msg_1\",\"type\":\"message\"}}\n\n" +
			"event: response.content_part.added\ndata: {\"type\":\"response.content_part.added\",\"sequence_number\":2,\"output_index\":0,\"content_index\":0,\"part\":{\"type\":\"output_text\",\"text\":\"\"}}\n\n" +
			"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"sequence_number\":3,\"output_index\":0,\"content_index\":0,\"delta\":\"pong\"}\n\n" +
			"event: response.content_part.done\ndata: {\"type\":\"response.content_part.done\",\"sequence_number\":4,\"output_index\":0,\"content_index\":0,\"part\":{\"type\":\"output_text\",\"text\":\"pong\"}}\n\n" +
			"event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"sequence_number\":5,\"output_index\":0,\"item\":{\"id\":\"msg_1\",\"type\":\"message\"}}\n\n" +
			"event: response.completed\ndata: {\"type\":\"response.completed\",\"sequence_number\":6,\"response\":{\"id\":\"resp_stream\",\"status\":\"completed\"}}\n\n"))
	case RouteMessages:
		_, _ = w.Write([]byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_stream\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[]}}\n\n" +
			"event: ping\ndata: {\"type\":\"ping\"}\n\n" +
			"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"pong\"}}\n\n" +
			"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
			"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n" +
			"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
	}
}
