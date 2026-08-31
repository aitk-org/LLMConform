package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestFetchModelsUsesOpenAIListRoute(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"z-model","owned_by":"team"},{"id":"a-model"},{"id":"z-model"}]}`))
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	models, err := fetchModels(ctx, server.Client(), ModelListRequest{BaseURL: server.URL + "/v1", APIKey: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0].ID != "a-model" || models[1].ID != "z-model" {
		t.Fatalf("models = %+v", models)
	}
}

func TestFetchModelsFallsBackToAnthropicAuth(t *testing.T) {
	t.Parallel()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("x-api-key") == "secret" && r.Header.Get("anthropic-version") != "" {
			_, _ = w.Write([]byte(`{"data":[{"id":"claude-test"}]}`))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"use x-api-key"}}`))
	}))
	defer server.Close()

	models, err := fetchModels(context.Background(), server.Client(), ModelListRequest{BaseURL: server.URL, APIKey: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 || len(models) != 1 || models[0].ID != "claude-test" {
		t.Fatalf("requests = %d, models = %+v", requests, models)
	}
}
