package main

import (
	"math"
	"strings"
)

const (
	CapabilityOrchestrate = "orchestrate"
	CapabilityImplement   = "implement"
	CapabilityDebug       = "debug"
	CapabilityToolLoop    = "tool-loop"
	CapabilityExplore     = "explore"
	CapabilitySummarize   = "summarize"
	CapabilityGeneral     = "general"
)

var capabilityPriority = []string{
	CapabilityDebug,
	CapabilityImplement,
	CapabilityToolLoop,
	CapabilityOrchestrate,
	CapabilityExplore,
	CapabilitySummarize,
}

type SignalEvent struct {
	Kind   string
	Text   string
	Failed bool
}

type SignalSnapshot struct {
	Events []SignalEvent
}

type Classification struct {
	Capability string
	Confidence float64
	Signals    []string
}

func defaultKeywords() map[string][]string {
	return map[string][]string{
		CapabilityDebug: {
			" fail", "failed", "failure", "panic", "exception", "traceback",
			"segmentation fault", "exit code", "exit status", "assertion failed",
			"compile error", "type error", "test failed", "root cause", "debug",
		},
		CapabilityImplement: {
			"implement", "refactor", "add ", "modify", "change ", "edit ", "write ", "patch",
		},
		CapabilityToolLoop: {
			"go test", "npm test", "pnpm test", "yarn test", "pytest", "phpunit",
			"cargo test", "docker ", "git ", "make ", "npm ", "pnpm ", "yarn ", "bash ", "shell ",
		},
		CapabilityOrchestrate: {
			"architecture", "architect", "design ", "plan ", "planning", "trade-off",
			"tradeoff", "approach", "migration strategy", "decomposition", "multiple services",
		},
		CapabilityExplore: {
			"find ", "where ", "locate", "search ", "grep ", "glob ", "inspect ", "investigate repository", "read ",
		},
		CapabilitySummarize: {
			"summarize", "summary", "handoff", "status report", "what changed", "give me the results", "final explanation",
		},
	}
}

func classify(snapshot SignalSnapshot, cfg Config) Classification {
	scores := make(map[string]int, len(capabilityPriority))
	signals := make(map[string][]string, len(capabilityPriority))
	if len(snapshot.Events) > 0 && snapshot.Events[len(snapshot.Events)-1].Failed {
		return Classification{Capability: CapabilityDebug, Confidence: 1, Signals: []string{"latest-explicit-failure"}}
	}

	for i, event := range snapshot.Events {
		text := " " + strings.ToLower(event.Text)
		recency := 1
		switch len(snapshot.Events) - 1 - i {
		case 0:
			recency = 3
		case 1:
			recency = 2
		}
		for capability, words := range cfg.Keywords {
			for _, word := range words {
				if word != "" && strings.Contains(text, strings.ToLower(word)) {
					weight := keywordWeight(capability) * recency
					scores[capability] += weight
					signals[capability] = appendUnique(signals[capability], "keyword:"+word)
				}
			}
		}
		switch event.Kind {
		case "edit":
			scores[CapabilityImplement] += 8 * recency
			signals[CapabilityImplement] = appendUnique(signals[CapabilityImplement], "recent-edit")
		case "command", "tool-call":
			scores[CapabilityToolLoop] += 8 * recency
			signals[CapabilityToolLoop] = appendUnique(signals[CapabilityToolLoop], "recent-tool-execution")
		case "tool-result":
			scores[CapabilityToolLoop] += 7 * recency
			signals[CapabilityToolLoop] = appendUnique(signals[CapabilityToolLoop], "recent-successful-tool-result")
		case "read", "search":
			scores[CapabilityExplore] += 8 * recency
			signals[CapabilityExplore] = appendUnique(signals[CapabilityExplore], "recent-repository-read")
		}
	}

	last := latestUserText(snapshot)
	if containsAny(last, "fail", "error", "broken", "exception", "panic") && containsAny(last, "fix", "solve", "debug") {
		return Classification{Capability: CapabilityDebug, Confidence: 1, Signals: []string{"explicit-failure-resolution"}}
	}
	// Output-only summary requests should not lose to incidental historical edit/tool words.
	if isOutputOnlySummary(last) {
		return Classification{Capability: CapabilitySummarize, Confidence: 1, Signals: []string{"explicit-output-only-summary"}}
	}

	best, bestScore := CapabilityGeneral, 0
	for _, capability := range capabilityPriority {
		if scores[capability] > bestScore {
			best, bestScore = capability, scores[capability]
		}
	}
	if bestScore == 0 {
		return Classification{Capability: CapabilityGeneral, Confidence: 0}
	}
	confidence := math.Min(1, float64(bestScore)/10)
	return Classification{Capability: best, Confidence: confidence, Signals: signals[best]}
}

func keywordWeight(capability string) int {
	switch capability {
	case CapabilityDebug:
		return 8
	case CapabilityImplement, CapabilityOrchestrate, CapabilityExplore:
		return 7
	case CapabilitySummarize:
		return 9
	case CapabilityToolLoop:
		return 6
	default:
		return 0
	}
}

func latestUserText(snapshot SignalSnapshot) string {
	for i := len(snapshot.Events) - 1; i >= 0; i-- {
		if snapshot.Events[i].Kind == "user" {
			return strings.ToLower(snapshot.Events[i].Text)
		}
	}
	return ""
}

func isOutputOnlySummary(text string) bool {
	if !containsAny(text, "summarize", "summary", "handoff", "what changed", "give me the results", "status report") {
		return false
	}
	return !containsAny(text, "fix", "solve", "implement", "change", "edit", "debug", "investigate")
}

func containsAny(text string, values ...string) bool {
	for _, value := range values {
		if strings.Contains(text, value) {
			return true
		}
	}
	return false
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
