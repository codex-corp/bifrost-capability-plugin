package main

import (
	"encoding/json"
	"fmt"
)

const pluginName = "agent-capability-router"

type AliasConfig struct {
	Main   string `json:"main"`
	Worker string `json:"worker"`
	Max    string `json:"max"`
	Cheap  string `json:"cheap"`
}

type Config struct {
	ShadowMode          bool                `json:"shadow_mode"`
	ConfidenceThreshold float64             `json:"confidence_threshold"`
	HistoryMessages     int                 `json:"history_messages"`
	Aliases             AliasConfig         `json:"aliases"`
	ActiveRoles         map[string]bool     `json:"active_roles"`
	Keywords            map[string][]string `json:"keywords"`
}

func defaultConfig() Config {
	return Config{
		ShadowMode:          true,
		ConfidenceThreshold: 0.70,
		HistoryMessages:     8,
		Aliases: AliasConfig{
			Main:   "agent-main-auto",
			Worker: "agent-worker-auto",
			Max:    "agent-main-max",
			Cheap:  "agent-main-cheap",
		},
		ActiveRoles: map[string]bool{"main": true, "worker": true},
		Keywords:    defaultKeywords(),
	}
}

func parseConfig(raw any) (Config, error) {
	cfg := defaultConfig()
	if raw == nil {
		return cfg, nil
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return Config{}, fmt.Errorf("marshal config: %w", err)
	}
	// Decode onto defaults, matching Bifrost's built-in analyzer pattern: the
	// resolved config is immutable after Init and omitted fields keep defaults.
	if err := json.Unmarshal(b, &cfg); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) validate() error {
	if c.ConfidenceThreshold <= 0 || c.ConfidenceThreshold > 1 {
		return fmt.Errorf("confidence_threshold must be > 0 and <= 1")
	}
	if c.HistoryMessages < 1 || c.HistoryMessages > 32 {
		return fmt.Errorf("history_messages must be between 1 and 32")
	}
	if c.Aliases.Main == "" || c.Aliases.Worker == "" || c.Aliases.Max == "" || c.Aliases.Cheap == "" {
		return fmt.Errorf("all aliases are required")
	}
	return nil
}
