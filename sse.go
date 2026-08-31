package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
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
		if current.Event != "" || current.Data != "" {
			current.Data = strings.TrimSuffix(current.Data, "\n")
			events = append(events, current)
			current = sseEvent{}
		}
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

func (p protocolSpec) validateStream(events []sseEvent) validation {
	if len(events) == 0 {
		return validation{Status: StatusFail, Summary: "没有收到任何 SSE 事件。", Expected: "期望：至少收到一个内容事件和一个终止事件。"}
	}
	switch p.ID {
	case RouteChat:
		seenChunk := false
		seenContent := false
		seenDone := false
		for _, event := range events {
			if event.Data == "[DONE]" {
				seenDone = true
				continue
			}
			var value map[string]any
			if json.Unmarshal([]byte(event.Data), &value) == nil {
				if _, ok := value["choices"].([]any); ok {
					seenChunk = true
					choices, _ := value["choices"].([]any)
					for _, item := range choices {
						choice, _ := item.(map[string]any)
						delta, _ := choice["delta"].(map[string]any)
						if text, ok := delta["content"].(string); ok && text != "" {
							seenContent = true
						}
					}
				}
			}
		}
		if !seenChunk {
			return missing("事件流中没有有效的 chat completion chunk。", "期望：data JSON 包含 choices 数组。")
		}
		if !seenContent {
			return missing("事件流中没有文本增量。", "期望：至少有一个 choices[].delta.content 文本片段。")
		}
		if !seenDone {
			return missing("事件流缺少 [DONE] 终止标记。", "期望：Chat Completions 流以 data: [DONE] 结束。")
		}
	case RouteResponses:
		seenCreated := false
		seenCompleted := false
		seenContent := false
		for _, event := range events {
			typeName := event.Event
			if typeName == "" {
				var value map[string]any
				if json.Unmarshal([]byte(event.Data), &value) == nil {
					typeName, _ = value["type"].(string)
				}
			}
			seenCreated = seenCreated || typeName == "response.created"
			seenCompleted = seenCompleted || typeName == "response.completed"
			seenContent = seenContent || typeName == "response.output_text.delta"
		}
		if !seenCreated {
			return missing("事件流缺少 response.created。", "期望：Responses 流包含 response.created 和 response.completed。")
		}
		if !seenCompleted {
			return missing("事件流缺少 response.completed。", "期望：Responses 流以 response.completed 正常完成。")
		}
		if !seenContent {
			return missing("事件流中没有 response.output_text.delta。", "期望：至少有一个输出文本增量事件。")
		}
	case RouteMessages:
		seenStart := false
		seenStop := false
		seenContent := false
		for _, event := range events {
			typeName := event.Event
			if typeName == "" {
				var value map[string]any
				if json.Unmarshal([]byte(event.Data), &value) == nil {
					typeName, _ = value["type"].(string)
				}
			}
			seenStart = seenStart || typeName == "message_start"
			seenStop = seenStop || typeName == "message_stop"
			if typeName == "content_block_delta" {
				var value map[string]any
				if json.Unmarshal([]byte(event.Data), &value) == nil {
					delta, _ := value["delta"].(map[string]any)
					seenContent = seenContent || delta["type"] == "text_delta"
				}
			}
		}
		if !seenStart {
			return missing("事件流缺少 message_start。", "期望：Messages 流包含 message_start 和 message_stop。")
		}
		if !seenStop {
			return missing("事件流缺少 message_stop。", "期望：Messages 流以 message_stop 正常完成。")
		}
		if !seenContent {
			return missing("事件流中没有 text_delta。", "期望：至少有一个 content_block_delta.text_delta 事件。")
		}
	}
	return validation{Status: StatusPass, Summary: fmt.Sprintf("收到 %d 个 SSE 事件且正常结束。", len(events)), Expected: "已验证事件格式、关键事件和终止事件。"}
}
