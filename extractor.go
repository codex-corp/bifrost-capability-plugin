package main

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/core/schemas"
)

const maxExtractedStringBytes = 4096

func extractAgentSignals(req *schemas.BifrostRequest, historyMessages int) SignalSnapshot {
	if req == nil {
		return SignalSnapshot{}
	}
	if req.ChatRequest != nil {
		input := req.ChatRequest.Input
		if len(input) > historyMessages {
			input = input[len(input)-historyMessages:]
		}
		events := make([]SignalEvent, 0, len(input)*2)
		for _, message := range input {
			text := textFromValue(message)
			kind := string(message.Role)
			if message.ChatToolMessage != nil || message.Role == schemas.ChatMessageRoleTool {
				kind = "tool-result"
			}
			if message.ChatAssistantMessage != nil && len(message.ChatAssistantMessage.ToolCalls) > 0 {
				events = append(events, SignalEvent{Kind: inferToolKind(text), Text: text})
				continue
			}
			events = append(events, SignalEvent{Kind: kind, Text: text, Failed: kind == "tool-result" && looksLikeFailure(text)})
		}
		return SignalSnapshot{Events: events}
	}
	if req.ResponsesRequest != nil {
		input := req.ResponsesRequest.Input
		if len(input) > historyMessages {
			input = input[len(input)-historyMessages:]
		}
		events := make([]SignalEvent, 0, len(input))
		for _, message := range input {
			text := textFromValue(message)
			kind := inferResponsesKind(message, text)
			events = append(events, SignalEvent{Kind: kind, Text: text, Failed: kind == "tool-result" && looksLikeFailure(text)})
		}
		return SignalSnapshot{Events: events}
	}
	return SignalSnapshot{}
}

func inferResponsesKind(message schemas.ResponsesMessage, text string) string {
	b, _ := json.Marshal(message)
	lower := strings.ToLower(string(b))
	switch {
	case strings.Contains(lower, "function_call_output"), strings.Contains(lower, "tool_result"):
		return "tool-result"
	case strings.Contains(lower, "function_call"), strings.Contains(lower, "tool_call"):
		return inferToolKind(text)
	case strings.Contains(lower, `"role":"user"`):
		return "user"
	case strings.Contains(lower, `"role":"assistant"`):
		return "assistant"
	default:
		return "context"
	}
}

func inferToolKind(text string) string {
	lower := strings.ToLower(text)
	if containsAny(lower, "edit", "write", "patch", "apply_patch", "create_file", "replace") {
		return "edit"
	}
	if containsAny(lower, "read", "grep", "glob", "search", "find") {
		return "search"
	}
	return "tool-call"
}

func looksLikeFailure(text string) bool {
	lower := " " + strings.ToLower(text)
	return containsAny(lower,
		" fail", "failed", "failure", "panic", "exception", "traceback",
		"segmentation fault", "exit code 1", "exit status 1", "assertion failed",
		"compile error", "type error", "test failed",
	)
}

func textFromValue(value any) string {
	b, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	var decoded any
	if err := json.Unmarshal(b, &decoded); err != nil {
		return ""
	}
	parts := make([]string, 0, 8)
	collectStrings(decoded, "", &parts)
	return strings.Join(parts, " ")
}

func collectStrings(value any, key string, parts *[]string) {
	switch typed := value.(type) {
	case string:
		if typed == "" || len(typed) > maxExtractedStringBytes || isOpaqueField(key, typed) {
			return
		}
		*parts = append(*parts, typed)
	case []any:
		for _, item := range typed {
			collectStrings(item, key, parts)
		}
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for childKey := range typed {
			keys = append(keys, childKey)
		}
		sort.Strings(keys)
		for _, childKey := range keys {
			collectStrings(typed[childKey], childKey, parts)
		}
	}
}

func isOpaqueField(key, value string) bool {
	lowerKey := strings.ToLower(key)
	lowerValue := strings.ToLower(value)
	return lowerKey == "file_data" || lowerKey == "image_url" || lowerKey == "data" ||
		strings.HasPrefix(lowerValue, "data:image/") || strings.HasPrefix(lowerValue, "data:application/")
}
