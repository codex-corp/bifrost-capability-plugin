package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/google/cel-go/cel"
)

type routingRuleFixture struct {
	Name          string `json:"name"`
	CelExpression string `json:"cel_expression"`
	Scope         string `json:"scope"`
	ScopeID       string `json:"scope_id"`
	Priority      int    `json:"priority"`
	Targets       []struct {
		Provider string  `json:"provider"`
		Model    string  `json:"model"`
		Weight   float64 `json:"weight"`
	} `json:"targets"`
	Fallbacks []string `json:"fallbacks"`
}

func TestRoutingConfiguration(t *testing.T) {
	modelsBytes, err := os.ReadFile("config/models.json")
	if err != nil {
		t.Fatal(err)
	}
	var modelConfig struct {
		Scope struct {
			ID string `json:"id"`
		} `json:"scope"`
		Models map[string]string `json:"models"`
	}
	if err := json.Unmarshal(modelsBytes, &modelConfig); err != nil {
		t.Fatal(err)
	}
	allowed := make(map[string]bool, len(modelConfig.Models))
	for _, model := range modelConfig.Models {
		allowed[model] = true
	}

	rulesBytes, err := os.ReadFile("config/routing-rules.json")
	if err != nil {
		t.Fatal(err)
	}
	var rules []routingRuleFixture
	if err := json.Unmarshal(rulesBytes, &rules); err != nil {
		t.Fatal(err)
	}
	if len(rules) == 0 {
		t.Fatal("routing rule set is empty")
	}
	seenNames := map[string]bool{}
	seenPriorities := map[int]bool{}
	env, err := cel.NewEnv(
		cel.Variable("model", cel.StringType),
		cel.Variable("complexity_tier", cel.StringType),
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range rules {
		if !strings.HasPrefix(rule.Name, "Agent CR ") {
			t.Errorf("rule %q does not use isolated name prefix", rule.Name)
		}
		if seenNames[rule.Name] || seenPriorities[rule.Priority] {
			t.Errorf("duplicate rule name or priority: %q / %d", rule.Name, rule.Priority)
		}
		seenNames[rule.Name], seenPriorities[rule.Priority] = true, true
		if rule.Scope != "virtual_key" || rule.ScopeID != modelConfig.Scope.ID {
			t.Errorf("rule %q has wrong scope", rule.Name)
		}
		if !strings.Contains(rule.CelExpression, "agent-") || strings.Contains(rule.CelExpression, "codex-") {
			t.Errorf("rule %q is not isolated to agent aliases", rule.Name)
		}
		if _, issues := env.Compile(rule.CelExpression); issues != nil && issues.Err() != nil {
			t.Errorf("rule %q has invalid CEL: %v", rule.Name, issues.Err())
		}
		if len(rule.Targets) == 0 {
			t.Errorf("rule %q has no targets", rule.Name)
		}
		weight := 0.0
		for _, target := range rule.Targets {
			weight += target.Weight
			if !allowed[target.Provider+"/"+target.Model] {
				t.Errorf("rule %q uses unknown target %s/%s", rule.Name, target.Provider, target.Model)
			}
		}
		if weight != 1.0 {
			t.Errorf("rule %q target weights total %v", rule.Name, weight)
		}
		for _, fallback := range rule.Fallbacks {
			if !allowed[fallback] {
				t.Errorf("rule %q uses unknown fallback %q", rule.Name, fallback)
			}
		}
	}
	if !seenNames["Agent CR 000 main max"] || !seenNames["Agent CR 010 main cheap"] {
		t.Fatal("deterministic max/cheap rules are missing")
	}
}
