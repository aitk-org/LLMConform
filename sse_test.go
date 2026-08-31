package main

import (
	"net/http"
	"testing"
)

func TestChatStreamRejectsEventsAfterDone(t *testing.T) {
	t.Parallel()
	results := validateStreamEvents(t, RouteChat, []sseEvent{
		{Data: `{"id":"chat_1","object":"chat.completion.chunk","choices":[{"delta":{"content":"pong"},"finish_reason":null}]}`},
		{Data: `{"id":"chat_1","object":"chat.completion.chunk","choices":[{"delta":{},"finish_reason":"stop"}]}`},
		{Data: `[DONE]`},
		{Data: `{"id":"chat_1","object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`},
	})
	assertAssertionStatus(t, results, "stream.done", StatusFail)
}

func TestResponsesStreamRequiresCompletedTerminal(t *testing.T) {
	t.Parallel()
	results := validateStreamEvents(t, RouteResponses, []sseEvent{
		{Event: "response.created", Data: `{"type":"response.created","sequence_number":0}`},
		{Event: "response.failed", Data: `{"type":"response.failed","sequence_number":1,"response":{"status":"failed"}}`},
	})
	assertAssertionStatus(t, results, "stream.completed", StatusFail)
	assertAssertionStatus(t, results, "stream.terminal", StatusFail)
}

func TestResponsesStreamDoesNotTreatMissingDeltaAsContent(t *testing.T) {
	t.Parallel()
	results := validateStreamEvents(t, RouteResponses, []sseEvent{
		{Event: "response.created", Data: `{"type":"response.created","sequence_number":0}`},
		{Event: "response.output_text.delta", Data: `{"type":"response.output_text.delta","sequence_number":1}`},
		{Event: "response.completed", Data: `{"type":"response.completed","sequence_number":2,"response":{"status":"completed"}}`},
	})
	assertAssertionStatus(t, results, "stream.content", StatusFail)
}

func TestMessagesStreamRejectsDeltaBeforeBlockStart(t *testing.T) {
	t.Parallel()
	results := validateStreamEvents(t, RouteMessages, []sseEvent{
		{Event: "message_start", Data: `{"type":"message_start","message":{"id":"msg_1"}}`},
		{Event: "content_block_delta", Data: `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"pong"}}`},
		{Event: "message_stop", Data: `{"type":"message_stop"}`},
	})
	assertAssertionStatus(t, results, "stream.blocks", StatusFail)
}

func validateStreamEvents(t *testing.T, routeID string, events []sseEvent) []AssertionResult {
	t.Helper()
	definition := mustCaseDefinition(t, routeID, routeID+".stream.text")
	probe := probeResponse{
		StatusCode: http.StatusOK,
		Headers:    http.Header{"Content-Type": []string{"text/event-stream"}},
		Events:     events,
	}
	return protocolByID(routeID).validate(definition, probe)
}
