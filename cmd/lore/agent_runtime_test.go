package lore

import (
	"testing"

	"github.com/gbuehler/lore/internal/config"
)

func TestAgentSynthesisDisabledByProviderNone(t *testing.T) {
	if !agentSynthesisDisabled(config.AgentConfig{Provider: "none"}) {
		t.Fatal("expected provider none to disable synthesis")
	}
}

func TestAgentSynthesisDisabledByCommandNone(t *testing.T) {
	if !agentSynthesisDisabled(config.AgentConfig{Command: "none"}) {
		t.Fatal("expected command none to disable synthesis")
	}
}

func TestAgentSynthesisEnabledForCodex(t *testing.T) {
	if agentSynthesisDisabled(config.AgentConfig{Provider: "codex", Command: "codex"}) {
		t.Fatal("expected codex provider to enable synthesis")
	}
}
