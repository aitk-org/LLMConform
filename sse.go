package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"mime"
	"strings"
)

type sseEvent struct {
	Event string
	Data  string
}

func parseSSE(body []byte) ([]sseEvent, error) {
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	events := make([]sseEvent, 0, 16)
	current := sseEvent{}
	flush := func() {
		if current.Event == "" && current.Data == "" {
			return
		}
		current.Data = strings.TrimSuffix(current.Data, "\n")
		events = append(events, current)
		current = sseEvent{}
	}
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			flush()
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, found := strings.Cut(line, ":")
		if found {
			value = strings.TrimPrefix(value, " ")
		}
		switch field {
		case "event":
			current.Event = value
		case "data":
			current.Data += value + "\n"
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read SSE: %w", err)
	}
	flush()
	return events, nil
}

func (p protocolSpec) validateStreamProbe(builder *assertionBuilder, probe probeResponse) {
	if probe.Err != nil {
		builder.check("http.success", false, "transport.or_sse.failed", "HTTP 2xx", probe.Err.Error())
		builder.check("http.sse", false, "stream.unavailable", "text/event-stream", probe.Headers.Get("Content-Type"))
		builder.check("sse.syntax", false, "sse.parse.failed", "parseable SSE events", probe.Err.Error())
		builder.blockUnset("stream.unavailable")
		return
	}
	success := probe.StatusCode >= 200 && probe.StatusCode < 300
	builder.check("http.success", success, "http.status.not_success", "HTTP 2xx", fmt.Sprintf("HTTP %d", probe.StatusCode))
	mediaType, _, err := mime.ParseMediaType(probe.Headers.Get("Content-Type"))
	isSSE := err == nil && mediaType == "text/event-stream"
	builder.check("http.sse", isSSE, "http.content_type.not_sse", "text/event-stream", probe.Headers.Get("Content-Type"))
	builder.check("sse.syntax", len(probe.Events) > 0, "sse.events.empty", "one or more SSE events", fmt.Sprintf("%d events", len(probe.Events)))
	if !success || !isSSE || len(probe.Events) == 0 {
		builder.blockUnset("stream.unavailable")
		return
	}
	switch p.ID {
	case RouteChat:
		validateChatStream(builder, probe.Events)
	case RouteResponses:
		validateResponsesStream(builder, probe.Events)
	case RouteMessages:
		validateMessagesStream(builder, probe.Events)
	}
}

func validateChatStream(builder *assertionBuilder, events []sseEvent) {
	seenChunk := false
	chunksValid := true
	seenContent := false
	seenFinish := false
	seenDone := false
	doneIsFinal := true
	seenUsage := false
	usageValid := true
	stableID := ""
	idValid := true

	for _, event := range events {
		if event.Data == "[DONE]" {
			if seenDone {
				doneIsFinal = false
			}
			seenDone = true
			continue
		}
		if event.Data == "" {
			continue
		}
		if seenDone {
			doneIsFinal = false
		}
		var value map[string]any
		if err := json.Unmarshal([]byte(event.Data), &value); err != nil || value == nil {
			chunksValid = false
			continue
		}
		seenChunk = true
		if value["object"] != "chat.completion.chunk" {
			chunksValid = false
		}
		id, ok := nonEmptyString(value["id"])
		if !ok {
			idValid = false
		} else if stableID == "" {
			stableID = id
		} else if stableID != id {
			idValid = false
		}
		choices, ok := value["choices"].([]any)
		if !ok {
			chunksValid = false
			continue
		}
		for _, rawChoice := range choices {
			choice, ok := rawChoice.(map[string]any)
			if !ok {
				chunksValid = false
				continue
			}
			if finish, ok := nonEmptyString(choice["finish_reason"]); ok && finish != "null" {
				seenFinish = true
			}
			delta, _ := choice["delta"].(map[string]any)
			if text, ok := delta["content"].(string); ok && text != "" {
				seenContent = true
			}
		}
		if rawUsage, exists := value["usage"]; exists && rawUsage != nil {
			seenUsage = true
			usage, ok := rawUsage.(map[string]any)
			if !ok {
				usageValid = false
				continue
			}
			prompt, promptOK := integerValue(usage["prompt_tokens"])
			completion, completionOK := integerValue(usage["completion_tokens"])
			total, totalOK := integerValue(usage["total_tokens"])
			usageValid = usageValid && promptOK && completionOK && totalOK && prompt >= 0 && completion >= 0 && total == prompt+completion
		}
	}

	builder.check("stream.chunk", seenChunk && chunksValid, "chat.stream.chunk.invalid", "valid chat.completion.chunk JSON events", fmt.Sprintf("%d events", len(events)))
	builder.check("stream.id", seenChunk && idValid && stableID != "", "chat.stream.id.invalid", "one stable non-empty response ID", stableID)
	builder.check("stream.content", seenContent, "chat.stream.content.missing", "one or more text deltas", "no non-empty delta.content")
	builder.check("stream.finish", seenFinish, "chat.stream.finish.missing", "non-empty finish_reason", "not observed")
	builder.check("stream.done", seenDone && doneIsFinal, "chat.stream.done.invalid", "one final [DONE] event", fmt.Sprintf("seen=%t final=%t", seenDone, doneIsFinal))
	builder.check("stream.usage", seenUsage && usageValid, "chat.stream.usage.invalid", "one valid final usage object", fmt.Sprintf("seen=%t valid=%t", seenUsage, usageValid))
}

func validateResponsesStream(builder *assertionBuilder, events []sseEvent) {
	firstType := ""
	seenCreated := false
	seenContent := false
	seenCompleted := false
	completedIsFinal := true
	terminalValid := true
	jsonValid := true
	lifecycleValid := true
	seenItemAdded := false
	seenPartAdded := false
	seenItemDone := false
	seenPartDone := false
	items := make(map[int]bool)
	parts := make(map[string]bool)
	sequenceSeen := false
	sequenceValid := true
	var lastSequence int64 = -1

	for _, event := range events {
		if event.Data == "" {
			continue
		}
		var value map[string]any
		if err := json.Unmarshal([]byte(event.Data), &value); err != nil || value == nil {
			jsonValid = false
			continue
		}
		typeName, _ := value["type"].(string)
		if typeName == "" {
			typeName = event.Event
		}
		if event.Event != "" && typeName != event.Event {
			jsonValid = false
		}
		if firstType == "" && typeName != "" {
			firstType = typeName
		}
		if seenCompleted && typeName != "" {
			completedIsFinal = false
		}
		if sequence, ok := integerValue(value["sequence_number"]); ok {
			sequenceSeen = true
			if sequence <= lastSequence {
				sequenceValid = false
			}
			lastSequence = sequence
		}

		switch typeName {
		case "response.created":
			seenCreated = true
		case "response.output_item.added":
			index, ok := integerValue(value["output_index"])
			lifecycleValid = lifecycleValid && ok && index >= 0 && !items[int(index)]
			items[int(index)] = true
			seenItemAdded = true
		case "response.content_part.added":
			outputIndex, outputOK := integerValue(value["output_index"])
			contentIndex, contentOK := integerValue(value["content_index"])
			key := fmt.Sprintf("%d:%d", outputIndex, contentIndex)
			lifecycleValid = lifecycleValid && outputOK && contentOK && items[int(outputIndex)] && !parts[key]
			parts[key] = true
			seenPartAdded = true
		case "response.output_text.delta":
			delta, deltaOK := value["delta"].(string)
			seenContent = seenContent || deltaOK && delta != ""
			if _, hasOutput := value["output_index"]; hasOutput {
				outputIndex, outputOK := integerValue(value["output_index"])
				contentIndex, contentOK := integerValue(value["content_index"])
				key := fmt.Sprintf("%d:%d", outputIndex, contentIndex)
				lifecycleValid = lifecycleValid && outputOK && contentOK && parts[key]
			}
		case "response.content_part.done":
			outputIndex, outputOK := integerValue(value["output_index"])
			contentIndex, contentOK := integerValue(value["content_index"])
			key := fmt.Sprintf("%d:%d", outputIndex, contentIndex)
			lifecycleValid = lifecycleValid && outputOK && contentOK && parts[key]
			delete(parts, key)
			seenPartDone = true
		case "response.output_item.done":
			index, ok := integerValue(value["output_index"])
			lifecycleValid = lifecycleValid && ok && items[int(index)]
			delete(items, int(index))
			seenItemDone = true
		case "response.completed":
			seenCompleted = true
			response, _ := value["response"].(map[string]any)
			terminalValid = terminalValid && response["status"] == "completed"
		case "response.failed", "response.incomplete", "response.cancelled", "error":
			terminalValid = false
		}
	}

	lifecycleComplete := jsonValid && lifecycleValid && seenItemAdded && seenPartAdded && seenItemDone && seenPartDone && len(items) == 0 && len(parts) == 0
	builder.check("stream.created", seenCreated && firstType == "response.created", "responses.stream.created.invalid", "response.created as first event", firstType)
	builder.check("stream.lifecycle", lifecycleComplete, "responses.stream.lifecycle.incomplete", "balanced output item and content part lifecycle", fmt.Sprintf("item=%t/%t part=%t/%t valid=%t", seenItemAdded, seenItemDone, seenPartAdded, seenPartDone, lifecycleValid))
	builder.check("stream.content", seenContent, "responses.stream.content.missing", "one or more response.output_text.delta events", "not observed")
	builder.check("stream.sequence", sequenceSeen && sequenceValid, "responses.stream.sequence.invalid", "strictly increasing sequence_number", fmt.Sprintf("seen=%t valid=%t", sequenceSeen, sequenceValid))
	builder.check("stream.completed", seenCompleted && completedIsFinal, "responses.stream.completed.invalid", "final response.completed", fmt.Sprintf("seen=%t final=%t", seenCompleted, completedIsFinal))
	builder.check("stream.terminal", terminalValid, "responses.stream.terminal.failed", "completed terminal response with no error terminal", fmt.Sprintf("valid=%t", terminalValid))
}

func validateMessagesStream(builder *assertionBuilder, events []sseEvent) {
	firstType := ""
	seenStart := false
	seenStop := false
	stopIsFinal := true
	seenContent := false
	seenUsage := false
	usageValid := true
	noError := true
	blocksValid := true
	nextIndex := 0
	active := make(map[int]string)

	for _, event := range events {
		if event.Data == "" {
			continue
		}
		var value map[string]any
		if err := json.Unmarshal([]byte(event.Data), &value); err != nil || value == nil {
			blocksValid = false
			continue
		}
		typeName, _ := value["type"].(string)
		if typeName == "" {
			typeName = event.Event
		}
		if event.Event != "" && typeName != event.Event {
			blocksValid = false
		}
		if typeName != "ping" && firstType == "" {
			firstType = typeName
		}
		if seenStop && typeName != "ping" {
			stopIsFinal = false
		}

		switch typeName {
		case "message_start":
			seenStart = true
		case "content_block_start":
			index, ok := integerValue(value["index"])
			contentBlock, _ := value["content_block"].(map[string]any)
			blockType, _ := contentBlock["type"].(string)
			intIndex := int(index)
			blocksValid = blocksValid && ok && intIndex == nextIndex && blockType != "" && active[intIndex] == ""
			active[intIndex] = blockType
			nextIndex++
		case "content_block_delta":
			index, ok := integerValue(value["index"])
			blockType := active[int(index)]
			delta, _ := value["delta"].(map[string]any)
			deltaType, _ := delta["type"].(string)
			blocksValid = blocksValid && ok && blockType != "" && deltaType != ""
			if deltaType == "text_delta" {
				text, _ := delta["text"].(string)
				seenContent = seenContent || text != ""
			}
		case "content_block_stop":
			index, ok := integerValue(value["index"])
			intIndex := int(index)
			blocksValid = blocksValid && ok && active[intIndex] != ""
			delete(active, intIndex)
		case "message_delta":
			if rawUsage, ok := value["usage"]; ok {
				seenUsage = true
				usage, usageOK := rawUsage.(map[string]any)
				output, outputOK := integerValue(usage["output_tokens"])
				usageValid = usageValid && usageOK && outputOK && output >= 0
			}
		case "message_stop":
			seenStop = true
			blocksValid = blocksValid && len(active) == 0
		case "error":
			noError = false
		}
	}

	builder.check("stream.message_start", seenStart && firstType == "message_start", "messages.stream.start.invalid", "message_start as first non-ping event", firstType)
	builder.check("stream.blocks", blocksValid && len(active) == 0, "messages.stream.blocks.invalid", "balanced contiguous content block lifecycle", fmt.Sprintf("valid=%t active=%d", blocksValid, len(active)))
	builder.check("stream.content", seenContent, "messages.stream.content.missing", "one or more non-empty text_delta events", "not observed")
	builder.check("stream.usage", seenUsage && usageValid, "messages.stream.usage.invalid", "cumulative non-negative message_delta usage", fmt.Sprintf("seen=%t valid=%t", seenUsage, usageValid))
	builder.check("stream.message_stop", seenStop && stopIsFinal, "messages.stream.stop.invalid", "final message_stop", fmt.Sprintf("seen=%t final=%t", seenStop, stopIsFinal))
	builder.check("stream.no_error", noError, "messages.stream.error", "no error event", fmt.Sprintf("valid=%t", noError))
}
