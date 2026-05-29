package lore

import (
	"strings"

	"github.com/gbuehler/lore/internal/config"
)

func agentSynthesisDisabled(agentCfg config.AgentConfig) bool {
	provider := strings.ToLower(strings.TrimSpace(agentCfg.Provider))
	command := strings.ToLower(strings.TrimSpace(agentCfg.Command))
	return provider == "none" || command == "none"
}
