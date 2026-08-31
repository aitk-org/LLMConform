package main

import (
	"net/http"
	"testing"
)

func TestBasicUsageRejectsFractionalTokens(t *testing.T) {
	t.Parallel()
	definition := mustCaseDefinition(t, RouteChat, RouteChat+".basic")
	probe := jsonProbe(http.StatusOK, `{
		"id":"chat_1","object":"chat.completion",
		"choices":[{"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}],
		"usage":{"prompt_tokens":1.5,"completion_tokens":1,"total_tokens":2.5}
	}`)

	results := protocolByID(RouteChat).validate(definition, probe)
	assertAssertionStatus(t, results, "usage.fields", StatusFail)
}

func TestToolValidationRequiresCallID(t *testing.T) {
	t.Parallel()
	definition := mustCaseDefinition(t, RouteResponses, RouteResponses+".tools.forced")
	probe := jsonProbe(http.StatusOK, `{
		"id":"resp_1","object":"response","status":"completed",
		"output":[{"type":"function_call","id":"fc_1","name":"get_weather","arguments":"{\"city\":\"Shanghai\"}"}]
	}`)

	results := protocolByID(RouteResponses).validate(definition, probe)
	assertAssertionStatus(t, results, "tool.call_id", StatusFail)
}

func jsonProbe(status int, body string) probeResponse {
	return probeResponse{
		StatusCode: status,
		Headers:    http.Header{"Content-Type": []string{"application/json"}},
		Body:       []byte(body),
	}
}

func mustCaseDefinition(t *testing.T, routeID, caseID string) caseDefinition {
	t.Helper()
	definition, ok := findCaseDefinition(routeID, caseID)
	if !ok {
		t.Fatalf("case %q not found", caseID)
	}
	return definition
}

func assertAssertionStatus(t *testing.T, results []AssertionResult, id, want string) {
	t.Helper()
	for _, result := range results {
		if result.ID == id {
			if result.Status != want {
				t.Fatalf("assertion %s status = %s, want %s (observed %q)", id, result.Status, want, result.Observed)
			}
			return
		}
	}
	t.Fatalf("assertion %q not found", id)
}
