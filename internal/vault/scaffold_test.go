package vault

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gregbuehler/lore/internal/config"
)

func TestScaffoldDefaultsToClaudeProvider(t *testing.T) {
	vaultPath := filepath.Join(t.TempDir(), "vault")

	if err := Scaffold(vaultPath, ScaffoldOptions{
		Name:     "Test User",
		Email:    "test@example.com",
		Entities: []string{"services"},
	}); err != nil {
		t.Fatalf("Scaffold returned error: %v", err)
	}

	cfg := mustLoadConfig(t, vaultPath)
	if cfg.Agent.Provider != "claude" {
		t.Fatalf("agent.provider = %q, want claude", cfg.Agent.Provider)
	}
	if cfg.Agent.Command != "claude" {
		t.Fatalf("agent.command = %q, want claude", cfg.Agent.Command)
	}
	assertPathExists(t, filepath.Join(vaultPath, ".claude", "CLAUDE.md"))
	assertPathMissing(t, filepath.Join(vaultPath, "AGENTS.md"))
}

func TestScaffoldCodexProviderWritesAgentsWithoutClaudeFiles(t *testing.T) {
	vaultPath := filepath.Join(t.TempDir(), "vault")

	if err := Scaffold(vaultPath, ScaffoldOptions{
		Name:          "Test User",
		Email:         "test@example.com",
		Entities:      []string{"services"},
		AgentProvider: "codex",
	}); err != nil {
		t.Fatalf("Scaffold returned error: %v", err)
	}

	cfg := mustLoadConfig(t, vaultPath)
	if cfg.Agent.Provider != "codex" {
		t.Fatalf("agent.provider = %q, want codex", cfg.Agent.Provider)
	}
	if cfg.Agent.Command != "codex" {
		t.Fatalf("agent.command = %q, want codex", cfg.Agent.Command)
	}
	agentsPath := filepath.Join(vaultPath, "AGENTS.md")
	assertPathExists(t, agentsPath)
	assertPathMissing(t, filepath.Join(vaultPath, ".claude", "CLAUDE.md"))

	data, err := os.ReadFile(agentsPath)
	if err != nil {
		t.Fatalf("reading AGENTS.md: %v", err)
	}
	if !strings.Contains(string(data), "@.lore/LORE.md") {
		t.Fatalf("AGENTS.md does not reference .lore/LORE.md:\n%s", data)
	}
}

func TestScaffoldNoneProviderWritesNoAgentFiles(t *testing.T) {
	vaultPath := filepath.Join(t.TempDir(), "vault")

	if err := Scaffold(vaultPath, ScaffoldOptions{
		Name:          "Test User",
		Email:         "test@example.com",
		Entities:      []string{"services"},
		AgentProvider: "none",
	}); err != nil {
		t.Fatalf("Scaffold returned error: %v", err)
	}

	cfg := mustLoadConfig(t, vaultPath)
	if cfg.Agent.Provider != "none" {
		t.Fatalf("agent.provider = %q, want none", cfg.Agent.Provider)
	}
	if cfg.Agent.Command != "" {
		t.Fatalf("agent.command = %q, want empty", cfg.Agent.Command)
	}
	assertPathMissing(t, filepath.Join(vaultPath, ".claude", "CLAUDE.md"))
	assertPathMissing(t, filepath.Join(vaultPath, "AGENTS.md"))
}

func TestScaffoldRejectsCustomProvider(t *testing.T) {
	vaultPath := filepath.Join(t.TempDir(), "vault")

	err := Scaffold(vaultPath, ScaffoldOptions{
		Name:          "Test User",
		Email:         "test@example.com",
		Entities:      []string{"services"},
		AgentProvider: "custom",
	})
	if err == nil {
		t.Fatal("expected custom provider to fail")
	}
	if !strings.Contains(err.Error(), `unsupported agent provider "custom"`) {
		t.Fatalf("error = %q, want unsupported custom provider", err)
	}
	assertPathMissing(t, filepath.Join(vaultPath, config.LoreDir, config.ConfigFile))
}

func mustLoadConfig(t *testing.T, vaultPath string) *config.Config {
	t.Helper()
	cfg, err := config.Load(vaultPath)
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	return cfg
}

func assertPathExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("%s does not exist: %v", path, err)
	}
}

func assertPathMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("%s exists or stat failed unexpectedly: %v", path, err)
	}
}
