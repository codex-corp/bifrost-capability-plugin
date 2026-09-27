package main

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
)

func TestCapabilityClassification(t *testing.T) {
	cfg := defaultConfig()
	tests := []struct {
		name   string
		events []SignalEvent
		want   string
	}{
		{"architecture becomes orchestrate", []SignalEvent{{Kind: "user", Text: "Design a migration architecture and explain the trade-offs."}}, CapabilityOrchestrate},
		{"edit becomes implement", []SignalEvent{{Kind: "edit", Text: "apply_patch authentication middleware"}}, CapabilityImplement},
		{"successful command becomes tool loop", []SignalEvent{{Kind: "tool-result", Text: "go test ./... ok"}}, CapabilityToolLoop},
		{"failure becomes debug", []SignalEvent{{Kind: "tool-result", Text: "FAIL TestAuthentication expected 200 got 401 exit status 1", Failed: true}}, CapabilityDebug},
		{"exploration becomes explore", []SignalEvent{{Kind: "search", Text: "Find where authentication middleware is registered"}}, CapabilityExplore},
		{"final information becomes summarize", []SignalEvent{{Kind: "user", Text: "Summarize what changed and list the files modified."}}, CapabilitySummarize},
		{"summary plus fix stays debug", []SignalEvent{{Kind: "user", Text: "Summarize why these tests are failing and fix them."}}, CapabilityDebug},
		{"ambiguous becomes general", []SignalEvent{{Kind: "user", Text: "continue"}}, CapabilityGeneral},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := classify(SignalSnapshot{Events: test.events}, cfg)
			if got.Capability != test.want {
				t.Fatalf("capability=%q confidence=%.2f signals=%v, want %q", got.Capability, got.Confidence, got.Signals, test.want)
			}
		})
	}
}

func TestAgentLifecycleTransitions(t *testing.T) {
	cfg := defaultConfig()
	steps := []struct {
		event SignalEvent
		want  string
	}{
		{SignalEvent{Kind: "user", Text: "Design the solution architecture"}, CapabilityOrchestrate},
		{SignalEvent{Kind: "edit", Text: "Implement it with apply_patch"}, CapabilityImplement},
		{SignalEvent{Kind: "tool-result", Text: "go test ./... ok"}, CapabilityToolLoop},
		{SignalEvent{Kind: "tool-result", Text: "FAIL TestLogin exit status 1", Failed: true}, CapabilityDebug},
		{SignalEvent{Kind: "edit", Text: "Patch the diagnosed condition"}, CapabilityImplement},
		{SignalEvent{Kind: "user", Text: "Summarize what changed"}, CapabilitySummarize},
	}
	for i, step := range steps {
		got := classify(SignalSnapshot{Events: []SignalEvent{step.event}}, cfg)
		if got.Capability != step.want {
			t.Fatalf("step %d capability=%q, want %q", i+1, got.Capability, step.want)
		}
	}
}

func TestLaterActionMovesPastHistoricalFailure(t *testing.T) {
	cfg := defaultConfig()
	got := classify(SignalSnapshot{Events: []SignalEvent{
		{Kind: "tool-result", Text: "FAIL TestLogin exit status 1", Failed: true},
		{Kind: "edit", Text: "Patch the diagnosed condition"},
	}}, cfg)
	if got.Capability != CapabilityImplement {
		t.Fatalf("capability=%q, want %q", got.Capability, CapabilityImplement)
	}
}

func TestRoleAndBypassRules(t *testing.T) {
	cfg := defaultConfig()
	tests := []struct {
		model   string
		role    string
		managed bool
	}{
		{"agent-main-auto", "main", true},
		{"agent-worker-auto", "worker", true},
		{"agent-main-max", "", false},
		{"agent-main-cheap", "", false},
		{"codex-main", "", false},
		{"codex-worker-auto", "", false},
		{"bedrock/zai.glm-5", "", false},
	}
	for _, test := range tests {
		role, managed := roleForModel(test.model, cfg)
		if role != test.role || managed != test.managed {
			t.Errorf("model=%q got (%q,%t), want (%q,%t)", test.model, role, managed, test.role, test.managed)
		}
	}
}

func TestContextScopeLanePrecedence(t *testing.T) {
	cfg := defaultConfig()
	tests := []struct {
		name     string
		role     string
		messages []schemas.ChatMessage
		want     string
	}{
		{
			name:     "normal worker implementation keeps capability lane",
			role:     "worker",
			messages: []schemas.ChatMessage{chatMessage(schemas.ChatMessageRoleUser, "Implement this focused function.")},
			want:     "agent-worker-implement",
		},
		{
			name:     "repo wide worker implementation becomes large",
			role:     "worker",
			messages: []schemas.ChatMessage{chatMessage(schemas.ChatMessageRoleUser, "Implement this migration across the entire repository.")},
			want:     "agent-worker-large",
		},
		{
			name: "latest user task remains anchor outside capability history",
			role: "worker",
			messages: append(
				[]schemas.ChatMessage{chatMessage(schemas.ChatMessageRoleUser, "Audit the repository and migrate all usages.")},
				repeatedChatMessages(schemas.ChatMessageRoleAssistant, "tool loop progress", cfg.HistoryMessages+2)...,
			),
			want: "agent-worker-large",
		},
		{
			name: "new focused user task replaces old repo wide task",
			role: "worker",
			messages: []schemas.ChatMessage{
				chatMessage(schemas.ChatMessageRoleUser, "Refactor the whole project."),
				chatMessage(schemas.ChatMessageRoleAssistant, "The repository-wide task is complete."),
				chatMessage(schemas.ChatMessageRoleUser, "Implement this focused function."),
			},
			want: "agent-worker-implement",
		},
		{
			name: "assistant and tool mentions do not create large scope",
			role: "worker",
			messages: []schemas.ChatMessage{
				chatMessage(schemas.ChatMessageRoleUser, "Implement this focused function."),
				chatMessage(schemas.ChatMessageRoleAssistant, "The phrase whole codebase is only incidental."),
				chatMessage(schemas.ChatMessageRoleTool, "Checked all files successfully."),
			},
			want: "agent-worker-tool-loop",
		},
		{
			name:     "huge worker input wins over large scope",
			role:     "worker",
			messages: []schemas.ChatMessage{chatMessage(schemas.ChatMessageRoleUser, "Refactor the whole project. "+strings.Repeat("x", cfg.HugeTokenThreshold*estimatedBytesPerToken))},
			want:     "agent-worker-huge",
		},
		{
			name:     "main large uses main lane",
			role:     "main",
			messages: []schemas.ChatMessage{chatMessage(schemas.ChatMessageRoleUser, "Audit the repository for authorization flaws.")},
			want:     "agent-main-large",
		},
		{
			name:     "main huge uses main lane",
			role:     "main",
			messages: []schemas.ChatMessage{chatMessage(schemas.ChatMessageRoleUser, strings.Repeat("x", cfg.HugeTokenThreshold*estimatedBytesPerToken))},
			want:     "agent-main-huge",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := &schemas.BifrostRequest{ChatRequest: &schemas.BifrostChatRequest{
				Model: "agent-" + test.role + "-auto",
				Input: test.messages,
			}}
			lane, _, _, _ := laneForRequest(test.role, req, cfg)
			if lane != test.want {
				t.Fatalf("lane=%q, want %q", lane, test.want)
			}
		})
	}
}

func TestCustomHugeTokenThreshold(t *testing.T) {
	cfg := defaultConfig()
	cfg.HugeTokenThreshold = 4
	req := &schemas.BifrostRequest{ChatRequest: &schemas.BifrostChatRequest{
		Model: "agent-worker-auto",
		Input: []schemas.ChatMessage{chatMessage(schemas.ChatMessageRoleUser, "A focused task")},
	}}
	lane, _, estimated, _ := laneForRequest("worker", req, cfg)
	if lane != "agent-worker-huge" || estimated < cfg.HugeTokenThreshold {
		t.Fatalf("lane=%q estimated=%d, want huge at threshold %d", lane, estimated, cfg.HugeTokenThreshold)
	}
}

func chatMessage(role schemas.ChatMessageRole, text string) schemas.ChatMessage {
	content := schemas.ChatMessageContent{ContentStr: schemas.Ptr(text)}
	return schemas.ChatMessage{Role: role, Content: &content}
}

func repeatedChatMessages(role schemas.ChatMessageRole, text string, count int) []schemas.ChatMessage {
	messages := make([]schemas.ChatMessage, count)
	for i := range messages {
		messages[i] = chatMessage(role, text)
	}
	return messages
}
