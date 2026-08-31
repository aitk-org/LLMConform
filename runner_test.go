package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRunnerChecksAllProtocols(t *testing.T) {
	t.Parallel()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(mockProvider))
	server.Listener = listener
	server.Start()
	defer server.Close()

	cfg := RunConfig{
		BaseURL: server.URL,
		APIKey:  "test-secret",
		Model:   "test-model",
		Routes:  allRouteIDs(),
		Timeout: 2 * time.Second,
	}
	report := NewRunner().Run(context.Background(), cfg, nil)
	if report.State != "COMPLETE" {
		t.Fatalf("state = %q, want COMPLETE", report.State)
	}
	if report.Summary.Pass != 15 || report.Summary.Fail != 0 || report.Summary.Warn != 0 {
		for _, route := range report.Routes {
			for _, check := range route.Checks {
				if check.Status != StatusPass {
					t.Logf("%s/%s: %s (%s)", route.ID, check.ID, check.Summary, check.Response)
				}
			}
		}
		t.Fatalf("summary = %+v, want 15 passes", report.Summary)
	}
	if report.Progress.Current != 15 || report.Progress.Total != 15 {
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

func TestParseSSEJoinsDataLines(t *testing.T) {
	t.Parallel()
	events, err := parseSSE([]byte("event: example\ndata: first\ndata: second\n\ndata: [DONE]\n\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("len(events) = %d, want 2", len(events))
	}
	if events[0].Event != "example" || events[0].Data != "first\nsecond" {
		t.Fatalf("first event = %+v", events[0])
	}
}

func mockProvider(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	var payload map[string]any
	_ = json.NewDecoder(r.Body).Decode(&payload)
	if len(payload) == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"type":"invalid_request_error","message":"model is required"}}`))
		return
	}
	stream, _ := payload["stream"].(bool)
	_, tools := payload["tools"]

	switch r.URL.Path {
	case "/v1/chat/completions":
		if r.Header.Get("Authorization") != "Bearer test-secret" {
			http.Error(w, "missing bearer token", http.StatusUnauthorized)
			return
		}
		if stream {
			writeMockSSE(w, "data: {\"choices\":[{\"delta\":{\"content\":\"pong\"}}]}\n\ndata: [DONE]\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if tools {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","tool_calls":[{"type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Shanghai\"}"}}]}}],"usage":{"prompt_tokens":8,"completion_tokens":4,"total_tokens":12}}`))
			return
		}
		_, _ = w.Write([]byte(`{"object":"chat.completion","choices":[{"message":{"role":"assistant","content":"pong"}}],"usage":{"prompt_tokens":8,"completion_tokens":1,"total_tokens":9}}`))
	case "/v1/responses":
		if stream {
			writeMockSSE(w, "event: response.created\ndata: {\"type\":\"response.created\"}\n\nevent: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"pong\"}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\"}\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if tools {
			_, _ = w.Write([]byte(`{"id":"resp_1","output":[{"type":"function_call","name":"get_weather","arguments":"{\"city\":\"Shanghai\"}"}],"usage":{"input_tokens":8,"output_tokens":4,"total_tokens":12}}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"resp_1","object":"response","output":[{"type":"message","content":[{"type":"output_text","text":"pong"}]}],"usage":{"input_tokens":8,"output_tokens":1,"total_tokens":9}}`))
	case "/v1/messages":
		if r.Header.Get("x-api-key") != "test-secret" || r.Header.Get("anthropic-version") == "" {
			http.Error(w, "missing anthropic headers", http.StatusUnauthorized)
			return
		}
		if stream {
			writeMockSSE(w, "event: message_start\ndata: {\"type\":\"message_start\"}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"pong\"}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if tools {
			_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"tool_use","name":"get_weather","input":{"city":"Shanghai"}}],"usage":{"input_tokens":8,"output_tokens":4}}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"pong"}],"usage":{"input_tokens":8,"output_tokens":1}}`))
	default:
		http.NotFound(w, r)
	}
}

func writeMockSSE(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = w.Write([]byte(body))
}
