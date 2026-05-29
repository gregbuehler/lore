package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadWithLocalAppliesMachineLocalAgentOverride(t *testing.T) {
	vault := t.TempDir()
	if err := os.MkdirAll(filepath.Join(vault, LoreDir), 0o755); err != nil {
		t.Fatalf("mkdir .lore: %v", err)
	}
	if err := os.WriteFile(filepath.Join(vault, LoreDir, ConfigFile), []byte("vault:\n  path: "+vault+"\nagent:\n  provider: claude\n  command: claude\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(vault, LoreDir, "local.yaml"), []byte("agent:\n  provider: codex\n  command: codex\n  sandbox: workspace-write\n"), 0o644); err != nil {
		t.Fatalf("write local config: %v", err)
	}

	cfg, err := LoadWithLocal(vault)
	if err != nil {
		t.Fatalf("LoadWithLocal: %v", err)
	}
	if cfg.Agent.Provider != "codex" || cfg.Agent.Command != "codex" || cfg.Agent.Sandbox != "workspace-write" {
		t.Fatalf("agent config = %#v", cfg.Agent)
	}
}

func TestLoadWithLocalAppliesEnvironmentAgentOverride(t *testing.T) {
	vault := t.TempDir()
	if err := os.MkdirAll(filepath.Join(vault, LoreDir), 0o755); err != nil {
		t.Fatalf("mkdir .lore: %v", err)
	}
	if err := os.WriteFile(filepath.Join(vault, LoreDir, ConfigFile), []byte("vault:\n  path: "+vault+"\nagent:\n  provider: claude\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("LORE_AGENT_PROVIDER", "codex")
	t.Setenv("LORE_AGENT_COMMAND", "codex")

	cfg, err := LoadWithLocal(vault)
	if err != nil {
		t.Fatalf("LoadWithLocal: %v", err)
	}
	if cfg.Agent.Provider != "codex" || cfg.Agent.Command != "codex" {
		t.Fatalf("agent config = %#v", cfg.Agent)
	}
}

func TestLoadWithLocalReturnsMalformedLocalConfigError(t *testing.T) {
	vault := t.TempDir()
	if err := os.MkdirAll(filepath.Join(vault, LoreDir), 0o755); err != nil {
		t.Fatalf("mkdir .lore: %v", err)
	}
	if err := os.WriteFile(filepath.Join(vault, LoreDir, ConfigFile), []byte("vault:\n  path: "+vault+"\nagent:\n  provider: claude\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(vault, LoreDir, "local.yaml"), []byte("agent:\n  provider: [\n"), 0o644); err != nil {
		t.Fatalf("write local config: %v", err)
	}

	_, err := LoadWithLocal(vault)
	if err == nil {
		t.Fatal("expected malformed local config to fail")
	}
	if !strings.Contains(err.Error(), "parsing local config") {
		t.Fatalf("error = %v, want parsing local config", err)
	}
}
