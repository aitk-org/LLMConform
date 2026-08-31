package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type protocolSpec struct {
	ID   string
	Name string
	Path string
}

type validation struct {
	Status   string
	Summary  string
	Expected string
}

func protocolByID(id string) protocolSpec {
	switch id {
	case RouteChat:
		return protocolSpec{ID: RouteChat, Name: "Chat Completions", Path: "/v1/chat/completions"}
	case RouteResponses:
		return protocolSpec{ID: RouteResponses, Name: "Responses", Path: "/v1/responses"}
	case RouteMessages:
		return protocolSpec{ID: RouteMessages, Name: "Messages", Path: "/v1/messages"}
	default:
		panic("unknown protocol: " + id)
	}
}

func (p protocolSpec) buildRequest(check, model string) ([]byte, error) {
	if check == CheckErrors {
		return []byte(`{}`), nil
	}
	stream := check == CheckStream
	tools := check == CheckTools

	var payload map[string]any
	switch p.ID {
	case RouteChat:
		payload = map[string]any{
			"model":      model,
			"messages":   []any{map[string]any{"role": "user", "content": promptFor(tools)}},
			"max_tokens": 32,
			"stream":     stream,
		}
		if tools {
			payload["tools"] = []any{map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        "get_weather",
					"description": "Get the weather for a city.",
					"parameters":  toolSchema(),
				},
			}}
			payload["tool_choice"] = map[string]any{"type": "function", "function": map[string]any{"name": "get_weather"}}
		}
	case RouteResponses:
		payload = map[string]any{
			"model":             model,
			"input":             promptFor(tools),
			"max_output_tokens": 32,
			"stream":            stream,
		}
		if tools {
			payload["tools"] = []any{map[string]any{
				"type":        "function",
				"name":        "get_weather",
				"description": "Get the weather for a city.",
				"parameters":  toolSchema(),
			}}
			payload["tool_choice"] = map[string]any{"type": "function", "name": "get_weather"}
		}
	case RouteMessages:
		payload = map[string]any{
			"model":      model,
			"max_tokens": 32,
			"messages":   []any{map[string]any{"role": "user", "content": promptFor(tools)}},
			"stream":     stream,
		}
		if tools {
			payload["tools"] = []any{map[string]any{
				"name":         "get_weather",
				"description":  "Get the weather for a city.",
				"input_schema": toolSchema(),
			}}
			payload["tool_choice"] = map[string]any{"type": "tool", "name": "get_weather"}
		}
	}
	return json.Marshal(payload)
}

func promptFor(tools bool) string {
	if tools {
		return "Use the get_weather tool for Shanghai. Do not answer directly."
	}
	return "Reply with the single word pong."
}

func toolSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"properties":           map[string]any{"city": map[string]any{"type": "string"}},
		"required":             []string{"city"},
		"additionalProperties": false,
	}
}

func (p protocolSpec) applyHeaders(header http.Header, apiKey string, stream bool) {
	header.Set("Content-Type", "application/json")
	if stream {
		header.Set("Accept", "text/event-stream")
	} else {
		header.Set("Accept", "application/json")
	}
	if apiKey == "" {
		return
	}
	header.Set("Authorization", "Bearer "+apiKey)
	if p.ID == RouteMessages {
		header.Set("x-api-key", apiKey)
		header.Set("anthropic-version", "2023-06-01")
	}
}

func (p protocolSpec) validate(check string, status int, header http.Header, body []byte, events []sseEvent) validation {
	if check == CheckErrors {
		return p.validateError(status, body)
	}
	if status < 200 || status >= 300 {
		return validation{
			Status:   StatusFail,
			Summary:  fmt.Sprintf("服务返回 HTTP %d，而不是成功状态。", status),
			Expected: "期望：有效请求返回 2xx 状态码。",
		}
	}
	if check == CheckStream {
		contentType := header.Get("Content-Type")
		if !strings.Contains(strings.ToLower(contentType), "text/event-stream") {
			return validation{Status: StatusFail, Summary: "响应不是 SSE 事件流。", Expected: "期望：Content-Type 包含 text/event-stream。"}
		}
		return p.validateStream(events)
	}

	value, err := decodeObject(body)
	if err != nil {
		return validation{Status: StatusFail, Summary: "响应不是有效的 JSON 对象：" + err.Error(), Expected: "期望：返回可解析的 JSON 对象。"}
	}
	switch check {
	case CheckBasic:
		return p.validateBasic(value)
	case CheckTools:
		return p.validateTools(value)
	case CheckUsage:
		return p.validateUsage(value)
	default:
		return validation{Status: StatusFail, Summary: "未知检查项。"}
	}
}

func (p protocolSpec) validateBasic(value map[string]any) validation {
	switch p.ID {
	case RouteChat:
		if object, _ := value["object"].(string); object != "chat.completion" {
			return missing("object 不是 chat.completion。", "期望：返回标准 Chat Completion 对象。")
		}
		choices, ok := nonEmptyArray(value["choices"])
		if !ok {
			return missing("缺少非空 choices 数组。", "期望：choices[0].message 包含模型输出。")
		}
		choice, ok := choices[0].(map[string]any)
		message, messageOK := choice["message"].(map[string]any)
		if !ok || !messageOK {
			return missing("choices[0] 缺少 message 对象。", "期望：choices[0].message 包含模型输出。")
		}
		if message["role"] != "assistant" || contentText(message["content"]) == "" {
			return missing("assistant message 缺少文本内容。", "期望：assistant message.role=assistant 且 content 非空。")
		}
	case RouteResponses:
		if object, _ := value["object"].(string); object != "response" {
			return missing("object 不是 response。", "期望：返回标准 Responses 对象。")
		}
		if _, ok := stringValue(value["id"]); !ok {
			return missing("缺少字符串 id。", "期望：Responses 对象包含 id 和 output。")
		}
		if _, ok := value["output"].([]any); !ok || responseOutputText(value["output"]) == "" {
			return missing("缺少 output 数组。", "期望：Responses 对象包含 id 和 output。")
		}
	case RouteMessages:
		if objectType, _ := value["type"].(string); objectType != "message" {
			return missing("type 不是 message。", "期望：返回标准 Claude Message 对象。")
		}
		if value["role"] != "assistant" {
			return missing("role 不是 assistant。", "期望：Message 对象的 role 为 assistant，并包含 content 数组。")
		}
		if _, ok := value["content"].([]any); !ok || contentText(value["content"]) == "" {
			return missing("缺少 content 数组。", "期望：Message 对象的 role 为 assistant，并包含 content 数组。")
		}
	}
	return validation{Status: StatusPass, Summary: "状态码和必要响应结构符合协议。", Expected: "已验证必要对象、字段和类型。"}
}

func (p protocolSpec) validateTools(value map[string]any) validation {
	var arguments any
	switch p.ID {
	case RouteChat:
		choices, ok := nonEmptyArray(value["choices"])
		if !ok {
			return missing("缺少 choices 数组。", "期望：message.tool_calls 至少包含一次工具调用。")
		}
		choice, _ := choices[0].(map[string]any)
		message, _ := choice["message"].(map[string]any)
		calls, ok := nonEmptyArray(message["tool_calls"])
		if !ok {
			return missing("message.tool_calls 为空。", "期望：message.tool_calls 至少包含一次工具调用。")
		}
		call, _ := calls[0].(map[string]any)
		function, _ := call["function"].(map[string]any)
		if function["name"] != "get_weather" {
			return missing("工具名称不是 get_weather。", "期望：调用测试请求指定的工具。")
		}
		arguments = function["arguments"]
	case RouteResponses:
		output, ok := nonEmptyArray(value["output"])
		if !ok {
			return missing("output 为空。", "期望：output 中包含 type=function_call 的项目。")
		}
		for _, item := range output {
			object, _ := item.(map[string]any)
			if object["type"] == "function_call" && object["name"] == "get_weather" {
				arguments = object["arguments"]
				break
			}
		}
		if arguments == nil {
			return missing("output 中没有 function_call。", "期望：output 中包含 type=function_call 的项目。")
		}
	case RouteMessages:
		content, ok := nonEmptyArray(value["content"])
		if !ok {
			return missing("content 为空。", "期望：content 中包含 type=tool_use 的内容块。")
		}
		for _, item := range content {
			object, _ := item.(map[string]any)
			if object["type"] == "tool_use" && object["name"] == "get_weather" {
				arguments = object["input"]
				break
			}
		}
		if arguments == nil {
			return missing("content 中没有 tool_use。", "期望：content 中包含 type=tool_use 的内容块。")
		}
	}

	if text, ok := arguments.(string); ok {
		var parsed map[string]any
		if err := json.Unmarshal([]byte(text), &parsed); err != nil || parsed == nil {
			return validation{Status: StatusFail, Summary: "工具调用 arguments 不是有效 JSON。", Expected: "期望：工具参数为合法 JSON 对象。"}
		}
	} else if parsed, ok := arguments.(map[string]any); !ok || parsed == nil {
		return validation{Status: StatusFail, Summary: "工具调用参数不是字符串或对象。", Expected: "期望：工具参数为合法 JSON 对象。"}
	}
	return validation{Status: StatusPass, Summary: "工具调用结构和参数 JSON 符合协议。", Expected: "已验证工具调用类型、名称和参数。"}
}

func (p protocolSpec) validateUsage(value map[string]any) validation {
	usage, ok := value["usage"].(map[string]any)
	if !ok {
		return missing("响应缺少 usage 对象。", "期望：响应包含协议规定的 token 用量字段。")
	}
	var fields []string
	switch p.ID {
	case RouteChat:
		fields = []string{"prompt_tokens", "completion_tokens", "total_tokens"}
	case RouteResponses:
		fields = []string{"input_tokens", "output_tokens", "total_tokens"}
	case RouteMessages:
		fields = []string{"input_tokens", "output_tokens"}
	}
	for _, field := range fields {
		if number, ok := numberValue(usage[field]); !ok || number < 0 {
			return validation{Status: StatusFail, Summary: "usage 缺少有效数值字段 " + field + "。", Expected: "期望：提供完整的协议标准 token 用量字段。"}
		}
	}
	return validation{Status: StatusPass, Summary: "Usage 字段完整且类型正确。", Expected: "已验证输入、输出和合计 token 字段。"}
}

func (p protocolSpec) validateError(status int, body []byte) validation {
	if status < 400 || status >= 500 {
		return validation{Status: StatusFail, Summary: fmt.Sprintf("无效请求返回 HTTP %d。", status), Expected: "期望：缺少 model 的请求返回 4xx。"}
	}
	value, err := decodeObject(body)
	if err != nil {
		return validation{Status: StatusFail, Summary: "错误响应不是有效 JSON 对象。", Expected: "期望：4xx 响应包含结构化错误对象。"}
	}
	errorValue, ok := value["error"].(map[string]any)
	if !ok {
		return validation{Status: StatusFail, Summary: "返回了正确的 4xx，但缺少 error 对象。", Expected: "期望：错误响应包含结构化 error 对象。"}
	}
	if _, ok := stringValue(errorValue["type"]); !ok {
		return validation{Status: StatusFail, Summary: "error 缺少 type。", Expected: "期望：error.type 和 error.message 均为非空字符串。"}
	}
	if _, ok := stringValue(errorValue["message"]); !ok {
		return validation{Status: StatusFail, Summary: "error 缺少 type 或 message。", Expected: "期望：error.type 和 error.message 均为非空字符串。"}
	}
	return validation{Status: StatusPass, Summary: "无效请求返回 4xx 和结构化错误对象。", Expected: "已验证错误状态与错误结构。"}
}

func missing(summary, expected string) validation {
	return validation{Status: StatusFail, Summary: summary, Expected: expected}
}

func decodeObject(body []byte) (map[string]any, error) {
	var value map[string]any
	if err := json.Unmarshal(body, &value); err != nil {
		return nil, err
	}
	if value == nil {
		return nil, fmt.Errorf("value is null")
	}
	return value, nil
}

func nonEmptyArray(value any) ([]any, bool) {
	items, ok := value.([]any)
	return items, ok && len(items) > 0
}

func stringValue(value any) (string, bool) {
	text, ok := value.(string)
	return text, ok && text != ""
}

func numberValue(value any) (float64, bool) {
	number, ok := value.(float64)
	return number, ok
}

func contentText(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	items, ok := value.([]any)
	if !ok {
		return ""
	}
	var builder strings.Builder
	for _, item := range items {
		block, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if text, ok := block["text"].(string); ok {
			builder.WriteString(text)
		}
	}
	return builder.String()
}

func responseOutputText(value any) string {
	items, ok := value.([]any)
	if !ok {
		return ""
	}
	var builder strings.Builder
	for _, item := range items {
		object, ok := item.(map[string]any)
		if !ok || object["type"] != "message" {
			continue
		}
		builder.WriteString(contentText(object["content"]))
	}
	return builder.String()
}
