package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

const maxModelListBytes = 2 * 1024 * 1024

func fetchModels(ctx context.Context, client *http.Client, request ModelListRequest) ([]ModelSummary, error) {
	baseURL, err := normalizeBaseURL(request.BaseURL)
	if err != nil {
		return nil, err
	}
	endpoint := endpointURL(baseURL, "/v1/models")
	models, status, err := requestModels(ctx, client, endpoint, request.APIKey, false)
	if err == nil {
		return models, nil
	}
	if request.APIKey != "" && (status == http.StatusUnauthorized || status == http.StatusForbidden) {
		return requestModelsWithoutStatus(ctx, client, endpoint, request.APIKey, true)
	}
	return nil, err
}

func requestModelsWithoutStatus(ctx context.Context, client *http.Client, endpoint, apiKey string, anthropicAuth bool) ([]ModelSummary, error) {
	models, _, err := requestModels(ctx, client, endpoint, apiKey, anthropicAuth)
	return models, err
}

func requestModels(ctx context.Context, client *http.Client, endpoint, apiKey string, anthropicAuth bool) ([]ModelSummary, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", "application/json")
	if apiKey != "" {
		if anthropicAuth {
			req.Header.Set("x-api-key", apiKey)
			req.Header.Set("anthropic-version", "2023-06-01")
		} else {
			req.Header.Set("Authorization", "Bearer "+apiKey)
		}
	}
	response, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("获取模型列表失败：%w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxModelListBytes+1))
	if err != nil {
		return nil, response.StatusCode, fmt.Errorf("读取模型列表失败：%w", err)
	}
	if len(body) > maxModelListBytes {
		return nil, response.StatusCode, fmt.Errorf("模型列表响应超过 2 MiB")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, response.StatusCode, fmt.Errorf("模型列表接口返回 HTTP %d%s", response.StatusCode, modelErrorMessage(body))
	}
	var payload struct {
		Data []ModelSummary `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, response.StatusCode, fmt.Errorf("模型列表不是有效 JSON：%w", err)
	}
	seen := make(map[string]bool, len(payload.Data))
	models := make([]ModelSummary, 0, len(payload.Data))
	for _, model := range payload.Data {
		model.ID = strings.TrimSpace(model.ID)
		if model.ID == "" || seen[model.ID] {
			continue
		}
		seen[model.ID] = true
		models = append(models, model)
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	if len(models) == 0 {
		return nil, response.StatusCode, fmt.Errorf("模型列表中没有可用的 model id")
	}
	return models, response.StatusCode, nil
}

func modelErrorMessage(body []byte) string {
	var payload struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &payload) == nil && payload.Error.Message != "" {
		return "：" + payload.Error.Message
	}
	return ""
}
