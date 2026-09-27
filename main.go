package main

import (
	"fmt"
	"strings"
	"sync"

	"github.com/maximhq/bifrost/core/schemas"
)

var (
	configMu     sync.RWMutex
	pluginConfig Config
	initialized  bool
)

func Init(raw any) error {
	cfg, err := parseConfig(raw)
	if err != nil {
		return err
	}
	configMu.Lock()
	pluginConfig = cfg
	initialized = true
	configMu.Unlock()
	return nil
}

func GetName() string { return pluginName }

func Cleanup() error { return nil }

func PreRequestHook(ctx *schemas.BifrostContext, req *schemas.BifrostRequest) error {
	configMu.RLock()
	cfg := pluginConfig
	ready := initialized
	configMu.RUnlock()
	if !ready {
		return nil
	}

	_, requestedModel, _ := req.GetRequestFields()
	role, managed := roleForModel(requestedModel, cfg)
	if !managed || !cfg.ActiveRoles[role] {
		return nil
	}

	lane, scope, estimatedInputTokens, classification := laneForRequest(role, req, cfg)
	capability := classification.Capability
	if capability == "" {
		capability = "none"
	}
	message := fmt.Sprintf(
		"requested=%s role=%s scope=%s estimated_input_tokens=%d capability=%s confidence=%.2f lane=%s shadow=%t signals=%s",
		requestedModel,
		role,
		scope,
		estimatedInputTokens,
		capability,
		classification.Confidence,
		lane,
		cfg.ShadowMode,
		strings.Join(classification.Signals, ","),
	)
	ctx.Log(schemas.LogLevelInfo, message)
	ctx.AppendRoutingEngineLog(schemas.RoutingEngineRoutingRule, schemas.LogLevelInfo, "agent-capability-router "+message)
	if cfg.ShadowMode {
		return nil
	}
	req.SetModel(lane)
	schemas.AppendToContextList(ctx, schemas.BifrostContextKeyRoutingEnginesUsed, schemas.RoutingEngineRoutingRule)
	return nil
}

func laneForRequest(role string, req *schemas.BifrostRequest, cfg Config) (string, string, int, Classification) {
	context := extractRequestContext(req)
	if context.EstimatedInputTokens >= cfg.HugeTokenThreshold {
		return "agent-" + role + "-huge", "huge", context.EstimatedInputTokens, Classification{
			Signals: []string{"estimated-input-threshold"},
		}
	}
	if isLargeScopeTask(context.LatestUserTask) {
		return "agent-" + role + "-large", "large", context.EstimatedInputTokens, Classification{
			Signals: []string{"latest-user-task-repository-wide"},
		}
	}

	classification := classify(extractAgentSignals(req, cfg.HistoryMessages), cfg)
	capability := classification.Capability
	if classification.Confidence < cfg.ConfidenceThreshold {
		capability = CapabilityGeneral
	}
	classification.Capability = capability
	return "agent-" + role + "-" + capability, "capability", context.EstimatedInputTokens, classification
}

func roleForModel(model string, cfg Config) (string, bool) {
	switch model {
	case cfg.Aliases.Main:
		return "main", true
	case cfg.Aliases.Worker:
		return "worker", true
	case cfg.Aliases.Max, cfg.Aliases.Cheap:
		return "", false
	default:
		return "", false
	}
}
