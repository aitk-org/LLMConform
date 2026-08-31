package main

import (
	"encoding/json"
	"fmt"
	"math"
	"mime"
	"net/http"
	"strings"
)

type protocolSpec struct {
	ID   string
	Name string
	Path string
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

func (p protocolSpec) buildRequest(kind caseKind, model string) ([]byte, error) {
	if kind == caseRoute {
		return []byte(`{}`), nil
	}

	payload := p.basePayload(model)
	switch kind {
	case caseBasic:
	case caseSystem:
		p.addSystemInstruction(payload)
	case caseMultiTurn:
		p.addConversationHistory(payload)
	case caseStreamText:
		payload["stream"] = true
		if p.ID == RouteChat {
			payload["stream_options"] = map[string]any{"include_usage": true}
		}
	case caseToolsForced:
		p.addTool(payload)
	case caseMissingModel:
		delete(payload, "model")
	case caseMissingInput:
		delete(payload, inputField(p.ID))
	case caseInvalidRole:
		p.setInvalidRole(payload)
	case caseInvalidLimit:
		payload[tokenLimitField(p.ID)] = -1
	default:
		return nil, fmt.Errorf("unsupported case kind %q", kind)
	}
	return json.Marshal(payload)
}

func (p protocolSpec) addSystemInstruction(payload map[string]any) {
	system := "Reply briefly and follow the requested output format."
	switch p.ID {
	case RouteChat:
		payload["messages"] = []any{
			map[string]any{"role": "system", "content": system},
			map[string]any{"role": "user", "content": promptFor(false)},
		}
	case RouteResponses:
		payload["instructions"] = system
	case RouteMessages:
		payload["system"] = system
	}
}

func (p protocolSpec) addConversationHistory(payload map[string]any) {
	switch p.ID {
	case RouteChat, RouteMessages:
		payload["messages"] = []any{
			map[string]any{"role": "user", "content": "Reply with the word ready."},
			map[string]any{"role": "assistant", "content": "ready"},
			map[string]any{"role": "user", "content": promptFor(false)},
		}
	case RouteResponses:
		payload["input"] = []any{
			map[string]any{"role": "user", "content": "Reply with the word ready."},
			map[string]any{"role": "assistant", "content": "ready"},
			map[string]any{"role": "user", "content": promptFor(false)},
		}
	}
}

func (p protocolSpec) basePayload(model string) map[string]any {
	switch p.ID {
	case RouteChat:
		return map[string]any{
			"model":      model,
			"messages":   []any{map[string]any{"role": "user", "content": promptFor(false)}},
			"max_tokens": 32,
			"stream":     false,
		}
	case RouteResponses:
		return map[string]any{
			"model":             model,
			"input":             promptFor(false),
			"max_output_tokens": 32,
			"stream":            false,
		}
	case RouteMessages:
		return map[string]any{
			"model":      model,
			"max_tokens": 32,
			"messages":   []any{map[string]any{"role": "user", "content": promptFor(false)}},
			"stream":     false,
		}
	default:
		panic("unknown protocol: " + p.ID)
	}
}

func (p protocolSpec) addTool(payload map[string]any) {
	switch p.ID {
	case RouteChat:
		payload["messages"] = []any{map[string]any{"role": "user", "content": promptFor(true)}}
		payload["max_tokens"] = 64
		payload["tools"] = []any{map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "get_weather",
				"description": "Get the weather for a city.",
				"parameters":  toolSchema(),
				"strict":      true,
			},
		}}
		payload["tool_choice"] = map[string]any{"type": "function", "function": map[string]any{"name": "get_weather"}}
	case RouteResponses:
		payload["input"] = promptFor(true)
		payload["max_output_tokens"] = 64
		payload["tools"] = []any{map[string]any{
			"type":        "function",
			"name":        "get_weather",
			"description": "Get the weather for a city.",
			"parameters":  toolSchema(),
			"strict":      true,
		}}
		payload["tool_choice"] = map[string]any{"type": "function", "name": "get_weather"}
	case RouteMessages:
		payload["messages"] = []any{map[string]any{"role": "user", "content": promptFor(true)}}
		payload["max_tokens"] = 64
		payload["tools"] = []any{map[string]any{
			"name":         "get_weather",
			"description":  "Get the weather for a city.",
			"input_schema": toolSchema(),
			"strict":       true,
		}}
		payload["tool_choice"] = map[string]any{"type": "tool", "name": "get_weather"}
	}
}

func (p protocolSpec) setInvalidRole(payload map[string]any) {
	field := inputField(p.ID)
	if p.ID == RouteResponses {
		payload[field] = []any{map[string]any{
			"role":    42,
			"content": []any{map[string]any{"type": "input_text", "text": "hello"}},
		}}
		return
	}
	payload[field] = []any{map[string]any{"role": 42, "content": "hello"}}
}

func inputField(routeID string) string {
	if routeID == RouteResponses {
		return "input"
	}
	return "messages"
}

func tokenLimitField(routeID string) string {
	if routeID == RouteResponses {
		return "max_output_tokens"
	}
	return "max_tokens"
}

func promptFor(tools bool) string {
	if tools {
		return "Use the get_weather tool for Shanghai. Do not answer directly."
	}
	return "Reply briefly with the word pong."
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
	if p.ID == RouteMessages {
		header.Set("x-api-key", apiKey)
		header.Set("anthropic-version", "2023-06-01")
		return
	}
	header.Set("Authorization", "Bearer "+apiKey)
}

type assertionBuilder struct {
	results []AssertionResult
	index   map[string]int
}

func newAssertionBuilder(definitions []assertionDefinition) *assertionBuilder {
	builder := &assertionBuilder{index: make(map[string]int, len(definitions))}
	for _, definition := range definitions {
		builder.index[definition.ID] = len(builder.results)
		builder.results = append(builder.results, AssertionResult{
			ID:       definition.ID,
			Name:     definition.Name,
			Severity: definition.Severity,
			Status:   StatusBlocked,
		})
	}
	return builder
}

func (b *assertionBuilder) check(id string, ok bool, reasonCode, expected, observed string) {
	index, exists := b.index[id]
	if !exists {
		return
	}
	result := &b.results[index]
	result.Expected = expected
	result.Observed = observed
	if ok {
		result.Status = StatusPass
		return
	}
	result.ReasonCode = reasonCode
	if result.Severity == SeverityAdvisory {
		result.Status = StatusWarn
	} else {
		result.Status = StatusFail
	}
}

func (b *assertionBuilder) blockUnset(reasonCode string) {
	for index := range b.results {
		if b.results[index].Status == StatusBlocked && b.results[index].ReasonCode == "" {
			b.results[index].ReasonCode = reasonCode
		}
	}
}

func (p protocolSpec) validate(definition caseDefinition, probe probeResponse) []AssertionResult {
	builder := newAssertionBuilder(definition.Assertions)
	switch definition.Kind {
	case caseRoute:
		ok := probe.Err == nil && probe.StatusCode != http.StatusNotFound && probe.StatusCode != http.StatusMethodNotAllowed
		observed := probeErrorOrStatus(probe)
		builder.check("http.route_exists", ok, "route.not_found", "HTTP status other than 404/405", observed)
	case caseBasic, caseSystem, caseMultiTurn:
		p.validateBasic(builder, probe)
	case caseStreamText:
		p.validateStreamProbe(builder, probe)
	case caseToolsForced:
		p.validateTools(builder, probe)
	case caseMissingModel, caseMissingInput, caseInvalidRole, caseInvalidLimit:
		p.validateError(builder, probe)
	default:
		builder.blockUnset("runner.unsupported_case")
	}
	return builder.results
}

func (p protocolSpec) validateBasic(builder *assertionBuilder, probe probeResponse) {
	value, ok := validateJSONResponse(builder, probe)
	if !ok {
		builder.blockUnset("response.unavailable")
		return
	}
	switch p.ID {
	case RouteChat:
		builder.check("response.object", value["object"] == "chat.completion", "chat.object.invalid", "chat.completion", stringObserved(value["object"]))
		_, idOK := nonEmptyString(value["id"])
		builder.check("response.id", idOK, "chat.id.missing", "non-empty string", stringObserved(value["id"]))
		choices, choicesOK := nonEmptyArray(value["choices"])
		builder.check("response.choices", choicesOK, "chat.choices.missing", "non-empty array", typeObserved(value["choices"]))
		if !choicesOK {
			builder.blockUnset("chat.choices.unavailable")
			return
		}
		choice, _ := choices[0].(map[string]any)
		message, _ := choice["message"].(map[string]any)
		builder.check("message.role", message["role"] == "assistant", "chat.role.invalid", "assistant", stringObserved(message["role"]))
		text := contentText(message["content"])
		builder.check("message.content", strings.TrimSpace(text) != "", "chat.content.empty", "non-empty assistant text", text)
		finish, finishOK := nonEmptyString(choice["finish_reason"])
		builder.check("response.finish_reason", finishOK, "chat.finish_reason.missing", "non-empty string", finish)
		validateUsage(builder, value["usage"], []string{"prompt_tokens", "completion_tokens", "total_tokens"}, "prompt_tokens", "completion_tokens")
	case RouteResponses:
		builder.check("response.object", value["object"] == "response", "responses.object.invalid", "response", stringObserved(value["object"]))
		_, idOK := nonEmptyString(value["id"])
		builder.check("response.id", idOK, "responses.id.missing", "non-empty string", stringObserved(value["id"]))
		builder.check("response.status", value["status"] == "completed", "responses.status.not_completed", "completed", stringObserved(value["status"]))
		text := responseOutputText(value["output"])
		builder.check("response.output_text", strings.TrimSpace(text) != "", "responses.output_text.empty", "non-empty output text", text)
		validateUsage(builder, value["usage"], []string{"input_tokens", "output_tokens", "total_tokens"}, "input_tokens", "output_tokens")
	case RouteMessages:
		builder.check("response.type", value["type"] == "message", "messages.type.invalid", "message", stringObserved(value["type"]))
		_, idOK := nonEmptyString(value["id"])
		builder.check("response.id", idOK, "messages.id.missing", "non-empty string", stringObserved(value["id"]))
		builder.check("message.role", value["role"] == "assistant", "messages.role.invalid", "assistant", stringObserved(value["role"]))
		text := contentText(value["content"])
		builder.check("message.content", strings.TrimSpace(text) != "", "messages.content.empty", "non-empty text block", text)
		stopReason, stopOK := nonEmptyString(value["stop_reason"])
		builder.check("response.stop_reason", stopOK, "messages.stop_reason.missing", "non-empty string", stopReason)
		validateUsage(builder, value["usage"], []string{"input_tokens", "output_tokens"}, "", "")
	}
}

func (p protocolSpec) validateTools(builder *assertionBuilder, probe probeResponse) {
	value, ok := validateJSONResponse(builder, probe)
	if !ok {
		builder.blockUnset("response.unavailable")
		return
	}
	var callType, callID, callID2, name string
	var arguments any
	var finish any
	switch p.ID {
	case RouteChat:
		choices, exists := nonEmptyArray(value["choices"])
		if exists {
			choice, _ := choices[0].(map[string]any)
			finish = choice["finish_reason"]
			message, _ := choice["message"].(map[string]any)
			calls, _ := nonEmptyArray(message["tool_calls"])
			if len(calls) > 0 {
				call, _ := calls[0].(map[string]any)
				callType, _ = call["type"].(string)
				callID, _ = call["id"].(string)
				function, _ := call["function"].(map[string]any)
				name, _ = function["name"].(string)
				arguments = function["arguments"]
			}
		}
	case RouteResponses:
		finish = value["status"]
		output, _ := nonEmptyArray(value["output"])
		for _, item := range output {
			call, _ := item.(map[string]any)
			if call["type"] != "function_call" {
				continue
			}
			callType, _ = call["type"].(string)
			callID, _ = call["id"].(string)
			callID2, _ = call["call_id"].(string)
			name, _ = call["name"].(string)
			arguments = call["arguments"]
			break
		}
	case RouteMessages:
		finish = value["stop_reason"]
		content, _ := nonEmptyArray(value["content"])
		for _, item := range content {
			call, _ := item.(map[string]any)
			if call["type"] != "tool_use" {
				continue
			}
			callType, _ = call["type"].(string)
			callID, _ = call["id"].(string)
			name, _ = call["name"].(string)
			arguments = call["input"]
			break
		}
	}

	wantType := map[string]string{RouteChat: "function", RouteResponses: "function_call", RouteMessages: "tool_use"}[p.ID]
	builder.check("tool.type", callType == wantType, p.ID+".tool.type.invalid", wantType, callType)
	builder.check("tool.id", strings.TrimSpace(callID) != "", p.ID+".tool.id.missing", "non-empty string", callID)
	if p.ID == RouteResponses {
		builder.check("tool.call_id", strings.TrimSpace(callID2) != "", "responses.tool.call_id.missing", "non-empty string", callID2)
	}
	builder.check("tool.name", name == "get_weather", p.ID+".tool.name.invalid", "get_weather", name)
	parsed, argumentsOK := parseToolArguments(arguments)
	builder.check("tool.arguments", argumentsOK, p.ID+".tool.arguments.invalid", "JSON object", typeObserved(arguments))
	city, cityOK := parsed["city"].(string)
	schemaOK := argumentsOK && cityOK && strings.TrimSpace(city) != "" && len(parsed) == 1
	builder.check("tool.schema", schemaOK, p.ID+".tool.schema.invalid", `{"city":"non-empty string"}`, jsonObserved(parsed))
	wantFinish := map[string]string{RouteChat: "tool_calls", RouteResponses: "completed", RouteMessages: "tool_use"}[p.ID]
	builder.check("tool.finish", finish == wantFinish, p.ID+".tool.finish.invalid", wantFinish, stringObserved(finish))
}

func (p protocolSpec) validateError(builder *assertionBuilder, probe probeResponse) {
	if probe.Err != nil {
		builder.check("http.error_status", false, "transport.request_failed", "HTTP 400", probe.Err.Error())
		builder.blockUnset("response.unavailable")
		return
	}
	builder.check("http.error_status", probe.StatusCode == http.StatusBadRequest, "error.status.invalid", "HTTP 400", fmt.Sprintf("HTTP %d", probe.StatusCode))
	mediaType, _, err := mime.ParseMediaType(probe.Headers.Get("Content-Type"))
	isJSON := err == nil && (mediaType == "application/json" || strings.HasSuffix(mediaType, "+json"))
	builder.check("http.json", isJSON, "error.content_type.invalid", "application/json", probe.Headers.Get("Content-Type"))
	value, decodeErr := decodeObject(probe.Body)
	if decodeErr != nil {
		builder.check("error.envelope", false, "error.body.invalid_json", "JSON error object", decodeErr.Error())
		builder.blockUnset("error.envelope.unavailable")
		return
	}
	errorValue, envelopeOK := value["error"].(map[string]any)
	if p.ID == RouteMessages {
		envelopeOK = envelopeOK && value["type"] == "error"
	}
	builder.check("error.envelope", envelopeOK, p.ID+".error.envelope.invalid", errorEnvelopeExpected(p.ID), jsonObserved(value))
	if !envelopeOK {
		builder.blockUnset("error.envelope.unavailable")
		return
	}
	errorType, typeOK := nonEmptyString(errorValue["type"])
	builder.check("error.type", typeOK, p.ID+".error.type.missing", "non-empty string", errorType)
	message, messageOK := nonEmptyString(errorValue["message"])
	builder.check("error.message", messageOK, p.ID+".error.message.missing", "non-empty string", message)
}

func validateJSONResponse(builder *assertionBuilder, probe probeResponse) (map[string]any, bool) {
	if probe.Err != nil {
		builder.check("http.success", false, "transport.request_failed", "HTTP 2xx", probe.Err.Error())
		builder.blockUnset("response.unavailable")
		return nil, false
	}
	success := probe.StatusCode >= 200 && probe.StatusCode < 300
	builder.check("http.success", success, "http.status.not_success", "HTTP 2xx", fmt.Sprintf("HTTP %d", probe.StatusCode))
	mediaType, _, err := mime.ParseMediaType(probe.Headers.Get("Content-Type"))
	isJSON := err == nil && (mediaType == "application/json" || strings.HasSuffix(mediaType, "+json"))
	builder.check("http.json", isJSON, "http.content_type.not_json", "application/json", probe.Headers.Get("Content-Type"))
	value, decodeErr := decodeObject(probe.Body)
	builder.check("json.object", decodeErr == nil, "json.object.invalid", "JSON object", errorObserved(decodeErr))
	return value, success && isJSON && decodeErr == nil
}

func validateUsage(builder *assertionBuilder, raw any, fields []string, inputField, outputField string) {
	usage, ok := raw.(map[string]any)
	fieldsOK := ok
	values := make(map[string]int64, len(fields))
	for _, field := range fields {
		value, valid := integerValue(usage[field])
		if !valid || value < 0 {
			fieldsOK = false
		}
		values[field] = value
	}
	builder.check("usage.fields", fieldsOK, "usage.fields.invalid", "non-negative integer token fields", jsonObserved(raw))
	if inputField == "" || outputField == "" {
		return
	}
	totalOK := fieldsOK && values["total_tokens"] == values[inputField]+values[outputField]
	builder.check("usage.total", totalOK, "usage.total.mismatch", "total_tokens = input + output", jsonObserved(raw))
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

func nonEmptyString(value any) (string, bool) {
	text, ok := value.(string)
	return text, ok && strings.TrimSpace(text) != ""
}

func integerValue(value any) (int64, bool) {
	number, ok := value.(float64)
	if !ok || math.IsNaN(number) || math.IsInf(number, 0) || math.Trunc(number) != number {
		return 0, false
	}
	return int64(number), true
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

func parseToolArguments(value any) (map[string]any, bool) {
	if object, ok := value.(map[string]any); ok && object != nil {
		return object, true
	}
	text, ok := value.(string)
	if !ok {
		return nil, false
	}
	var object map[string]any
	if err := json.Unmarshal([]byte(text), &object); err != nil || object == nil {
		return nil, false
	}
	return object, true
}

func errorEnvelopeExpected(routeID string) string {
	if routeID == RouteMessages {
		return `{"type":"error","error":{"type":"...","message":"..."}}`
	}
	return `{"error":{"type":"...","message":"..."}}`
}

func probeErrorOrStatus(probe probeResponse) string {
	if probe.Err != nil {
		return probe.Err.Error()
	}
	return fmt.Sprintf("HTTP %d", probe.StatusCode)
}

func typeObserved(value any) string {
	if value == nil {
		return "null"
	}
	return fmt.Sprintf("%T", value)
}

func stringObserved(value any) string {
	if value == nil {
		return "null"
	}
	return fmt.Sprint(value)
}

func jsonObserved(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return typeObserved(value)
	}
	return truncate(string(encoded), 600)
}

func errorObserved(err error) string {
	if err == nil {
		return "valid JSON object"
	}
	return err.Error()
}
