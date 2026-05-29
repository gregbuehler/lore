package lore

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gbuehler/lore/internal/agent"
	"github.com/gbuehler/lore/internal/config"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var agentLocalForce bool

var agentCmd = &cobra.Command{
	Use:   "agent",
	Short: "Manage local agent configuration",
}

var agentLocalCmd = &cobra.Command{
	Use:   "local <provider|status>",
	Short: "Write or inspect machine-local agent preferences",
	Long: `Writes .lore/local.yaml for this machine without changing shared
.lore/config.yaml. Use this when the same vault should run Codex on one
machine and Claude on another.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		vaultPath, err := config.FindVault()
		if err != nil {
			return err
		}
		provider := strings.ToLower(strings.TrimSpace(args[0]))
		if provider == "status" {
			return printAgentLocalStatus(vaultPath)
		}
		content, err := buildAgentLocalYAML(provider)
		if err != nil {
			return err
		}
		localPath := config.LocalPath(vaultPath)
		if !agentLocalForce {
			if _, err := os.Stat(localPath); err == nil {
				return fmt.Errorf("%s already exists (use --force to overwrite)", localPath)
			}
		}
		if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
			return fmt.Errorf("creating .lore directory: %w", err)
		}
		if err := os.WriteFile(localPath, []byte(content), 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", localPath, err)
		}
		fmt.Printf("Wrote %s\n", localPath)
		return nil
	},
}

func buildAgentLocalYAML(provider string) (string, error) {
	local := config.LocalConfig{}
	switch provider {
	case agent.ProviderCodex:
		local.Agent = config.AgentConfig{
			Provider: agent.ProviderCodex,
			Command:  "codex",
			Sandbox:  "workspace-write",
			Approval: "never",
		}
	case agent.ProviderClaude:
		local.Agent = config.AgentConfig{
			Provider: agent.ProviderClaude,
			Command:  "claude",
		}
	case agent.ProviderNone:
		local.Agent = config.AgentConfig{Provider: agent.ProviderNone}
	default:
		return "", fmt.Errorf("unknown agent provider %q (use claude, codex, or none)", provider)
	}
	data, err := yaml.Marshal(local)
	if err != nil {
		return "", fmt.Errorf("marshaling local config: %w", err)
	}
	return string(data), nil
}

func printAgentLocalStatus(vaultPath string) error {
	shared, err := config.Load(vaultPath)
	if err != nil {
		return err
	}
	effective, err := config.LoadWithLocal(vaultPath)
	if err != nil {
		return err
	}
	fmt.Printf("Shared:    %s\n", describeAgent(shared.Agent))
	if _, err := os.Stat(config.LocalPath(vaultPath)); err == nil {
		var local config.LocalConfig
		data, err := os.ReadFile(config.LocalPath(vaultPath))
		if err != nil {
			return err
		}
		if err := yaml.Unmarshal(data, &local); err != nil {
			return err
		}
		fmt.Printf("Local:     %s\n", describeAgent(local.Agent))
	} else {
		fmt.Println("Local:     none")
	}
	fmt.Printf("Effective: %s\n", describeAgent(effective.Agent))
	return nil
}

func describeAgent(agentCfg config.AgentConfig) string {
	label := agent.Label(agentCfg)
	if label == "none" {
		return "none"
	}
	provider := strings.TrimSpace(agentCfg.Provider)
	if provider == "" {
		provider = "inferred"
	}
	return fmt.Sprintf("%s (%s)", provider, label)
}

func init() {
	agentLocalCmd.Flags().BoolVar(&agentLocalForce, "force", false, "Overwrite existing .lore/local.yaml")
	agentCmd.AddCommand(agentLocalCmd)
}
