package main

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/core/schemas"
)

const maxExtractedStringBytes = 4096

const estimatedBytesPerToken = 4

type RequestContext struct {
	EstimatedInputTokens int
	LatestUserTask       string
}

func extractRequestContext(req *schemas.BifrostRequest) RequestContext {
	if req == nil {
		return RequestContext{}
	}

	var payload any
	context := RequestContext{}
	switch {
	case req.ChatRequest != nil:
		var extra map[string]any
		if req.ChatRequest.Params != nil {
			extra = req.ChatRequest.Params.ExtraParams
		}
		payload = struct {
			Input  []schemas.ChatMessage   `json:"input"`
			Params *schemas.ChatParameters `json:"params,omitempty"`
			Extra  map[string]any          `json:"extra_params,omitempty"`
		}{Input: req.ChatRequest.Input, Params: req.ChatRequest.Params, Extra: extra}
		for i := len(req.ChatRequest.Input) - 1; i >= 0; i-- {
			message := req.ChatRequest.Input[i]
			if message.Role != schemas.ChatMessageRoleUser || message.Content == nil {
				continue
			}
			text := strings.TrimSpace(fullTextFromValue(message.Content))
			if text != "" {
				context.LatestUserTask = text
				break
			}
		}
	case req.ResponsesRequest != nil:
		var extra map[string]any
		if req.ResponsesRequest.Params != nil {
			extra = req.ResponsesRequest.Params.ExtraParams
		}
		payload = struct {
			Input  []schemas.ResponsesMessage   `json:"input"`
			Params *schemas.ResponsesParameters `json:"params,omitempty"`
			Extra  map[string]any               `json:"extra_params,omitempty"`
		}{Input: req.ResponsesRequest.Input, Params: req.ResponsesRequest.Params, Extra: extra}
		for i := len(req.ResponsesRequest.Input) - 1; i >= 0; i-- {
			message := req.ResponsesRequest.Input[i]
			if message.Role == nil || *message.Role != schemas.ResponsesInputMessageRoleUser || message.Content == nil {
				continue
			}
			text := strings.TrimSpace(fullTextFromValue(message.Content))
			if text != "" {
				context.LatestUserTask = text
				break
			}
		}
	default:
		return context
	}

	context.EstimatedInputTokens = estimateInputTokens(payload)
	return context
}

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
	return textFromValueWithLimit(value, maxExtractedStringBytes)
}

func fullTextFromValue(value any) string {
	return textFromValueWithLimit(value, 0)
}

func textFromValueWithLimit(value any, maxStringBytes int) string {
	b, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	var decoded any
	if err := json.Unmarshal(b, &decoded); err != nil {
		return ""
	}
	parts := make([]string, 0, 8)
	collectStrings(decoded, "", maxStringBytes, &parts)
	return strings.Join(parts, " ")
}

func collectStrings(value any, key string, maxStringBytes int, parts *[]string) {
	switch typed := value.(type) {
	case string:
		if typed == "" || (maxStringBytes > 0 && len(typed) > maxStringBytes) || isOpaqueField(key, typed) {
			return
		}
		*parts = append(*parts, typed)
	case []any:
		for _, item := range typed {
			collectStrings(item, key, maxStringBytes, parts)
		}
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for childKey := range typed {
			keys = append(keys, childKey)
		}
		sort.Strings(keys)
		for _, childKey := range keys {
			if isOpaqueKey(childKey) {
				continue
			}
			collectStrings(typed[childKey], childKey, maxStringBytes, parts)
		}
	}
}

func estimateInputTokens(value any) int {
	bytes := textByteCount(value)
	return (bytes + estimatedBytesPerToken - 1) / estimatedBytesPerToken
}

func textByteCount(value any) int {
	b, err := json.Marshal(value)
	if err != nil {
		return 0
	}
	var decoded any
	if err := json.Unmarshal(b, &decoded); err != nil {
		return 0
	}
	return countTextBytes(decoded, "")
}

func countTextBytes(value any, key string) int {
	switch typed := value.(type) {
	case string:
		if typed == "" || isOpaqueField(key, typed) {
			return 0
		}
		return len(typed)
	case []any:
		total := 0
		for _, item := range typed {
			total += countTextBytes(item, key)
		}
		return total
	case map[string]any:
		total := 0
		for childKey, item := range typed {
			if isOpaqueKey(childKey) {
				continue
			}
			total += len(childKey) + countTextBytes(item, childKey)
		}
		return total
	default:
		return 0
	}
}

func isOpaqueField(key, value string) bool {
	lowerValue := strings.ToLower(value)
	return isOpaqueKey(key) ||
		strings.HasPrefix(lowerValue, "data:image/") || strings.HasPrefix(lowerValue, "data:application/")
}

func isOpaqueKey(key string) bool {
	switch strings.ToLower(key) {
	case "file_data", "image_url", "data":
		return true
	default:
		return false
	}
}
