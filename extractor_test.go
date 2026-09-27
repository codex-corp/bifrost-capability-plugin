package main

import (
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
)

func TestExtractChatFailure(t *testing.T) {
	content := schemas.ChatMessageContent{ContentStr: schemas.Ptr("FAIL TestLogin exit status 1")}
	toolID := "call-1"
	req := &schemas.BifrostRequest{ChatRequest: &schemas.BifrostChatRequest{
		Model: "agent-main-auto",
		Input: []schemas.ChatMessage{{
			Role:            schemas.ChatMessageRoleTool,
			Content:         &content,
			ChatToolMessage: &schemas.ChatToolMessage{ToolCallID: &toolID},
		}},
	}}
	snapshot := extractAgentSignals(req, 8)
	if len(snapshot.Events) != 1 || snapshot.Events[0].Kind != "tool-result" || !snapshot.Events[0].Failed {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}
}

func TestExtractChatEdit(t *testing.T) {
	name := "apply_patch"
	req := &schemas.BifrostRequest{ChatRequest: &schemas.BifrostChatRequest{
		Model: "agent-worker-auto",
		Input: []schemas.ChatMessage{{
			Role: schemas.ChatMessageRoleAssistant,
			ChatAssistantMessage: &schemas.ChatAssistantMessage{ToolCalls: []schemas.ChatAssistantMessageToolCall{{
				Function: schemas.ChatAssistantMessageToolCallFunction{Name: &name, Arguments: "{}"},
			}}},
		}},
	}}
	snapshot := extractAgentSignals(req, 8)
	if len(snapshot.Events) != 1 || snapshot.Events[0].Kind != "edit" {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}
}

func TestOpaquePayloadIsNotClassified(t *testing.T) {
	text := textFromValue(map[string]any{
		"content":   "inspect the config",
		"file_data": "implement debug architecture should be ignored",
	})
	if text != "inspect the config" {
		t.Fatalf("unexpected extracted text: %q", text)
	}
}

func TestExtractResponsesRequestContextUsesLatestUser(t *testing.T) {
	user := schemas.ResponsesInputMessageRoleUser
	assistant := schemas.ResponsesInputMessageRoleAssistant
	oldTask := schemas.ResponsesMessageContent{ContentStr: schemas.Ptr("Audit the repository.")}
	progress := schemas.ResponsesMessageContent{ContentStr: schemas.Ptr("Repository-wide progress note.")}
	currentTask := schemas.ResponsesMessageContent{ContentStr: schemas.Ptr("Implement this focused function.")}
	instructions := "Follow the project instructions."
	req := &schemas.BifrostRequest{ResponsesRequest: &schemas.BifrostResponsesRequest{
		Model: "agent-worker-auto",
		Input: []schemas.ResponsesMessage{
			{Role: &user, Content: &oldTask},
			{Role: &assistant, Content: &progress},
			{Role: &user, Content: &currentTask},
		},
		Params: &schemas.ResponsesParameters{Instructions: &instructions},
	}}

	context := extractRequestContext(req)
	if context.LatestUserTask != "Implement this focused function." {
		t.Fatalf("latest user task=%q", context.LatestUserTask)
	}
	if context.EstimatedInputTokens == 0 {
		t.Fatal("expected a non-zero token estimate")
	}
}

func TestTokenEstimateIgnoresOpaquePayloads(t *testing.T) {
	bytes := textByteCount(map[string]any{
		"content": "abcd",
		"image_url": map[string]any{
			"url": "https://example.test/whole-codebase.png",
		},
		"file_data": "refactor the whole project",
	})
	if bytes != len("contentabcd") {
		t.Fatalf("bytes=%d, want %d", bytes, len("contentabcd"))
	}
}

func TestTokenEstimateUsesUTF8Bytes(t *testing.T) {
	if got := estimateInputTokens("abcd"); got != 1 {
		t.Fatalf("ASCII estimate=%d, want 1", got)
	}
	if got := estimateInputTokens("مرحبا"); got != 3 {
		t.Fatalf("Arabic estimate=%d, want 3", got)
	}
}

func TestTokenEstimateIncludesToolSchemaKeys(t *testing.T) {
	schema := map[string]any{
		"properties": map[string]any{
			"repository_path": map[string]any{"description": "x"},
		},
	}
	wantBytes := len("properties") + len("repository_path") + len("description") + len("x")
	if got := textByteCount(schema); got != wantBytes {
		t.Fatalf("schema bytes=%d, want %d", got, wantBytes)
	}
}

func TestTokenEstimateIncludesRequestParameters(t *testing.T) {
	req := &schemas.BifrostRequest{ChatRequest: &schemas.BifrostChatRequest{
		Model: "agent-worker-auto",
		Input: []schemas.ChatMessage{chatMessage(schemas.ChatMessageRoleUser, "x")},
		Params: &schemas.ChatParameters{ExtraParams: map[string]any{
			"tools": []any{map[string]any{
				"description": "inspect repository files",
				"arguments":   "focused arguments",
			}},
		}},
	}}

	context := extractRequestContext(req)
	minimumBytes := len("xinspect repository filesfocused arguments")
	minimumTokens := (minimumBytes + estimatedBytesPerToken - 1) / estimatedBytesPerToken
	if context.EstimatedInputTokens < minimumTokens {
		t.Fatalf("estimated tokens=%d, want at least %d", context.EstimatedInputTokens, minimumTokens)
	}
}
