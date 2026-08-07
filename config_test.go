package main

import "testing"

func TestConfigDefaultsAndOverrides(t *testing.T) {
	cfg, err := parseConfig(map[string]any{
		"shadow_mode":          false,
		"confidence_threshold": 0.8,
		"history_messages":     6,
		"active_roles":         map[string]any{"main": true, "worker": false},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ShadowMode || cfg.ConfidenceThreshold != 0.8 || cfg.HistoryMessages != 6 {
		t.Fatalf("unexpected config: %#v", cfg)
	}
	if cfg.ActiveRoles["worker"] {
		t.Fatal("worker override was not retained")
	}
	if cfg.Aliases.Main != "agent-main-auto" {
		t.Fatal("default aliases were not retained")
	}
}

func TestInvalidConfig(t *testing.T) {
	_, err := parseConfig(map[string]any{"confidence_threshold": 2.0})
	if err == nil {
		t.Fatal("expected invalid confidence threshold")
	}
}

func TestOmittedShadowModeKeepsSafeDefault(t *testing.T) {
	cfg, err := parseConfig(map[string]any{"history_messages": 4})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.ShadowMode {
		t.Fatal("omitted shadow_mode must remain enabled")
	}
}
