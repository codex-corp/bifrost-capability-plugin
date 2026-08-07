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
