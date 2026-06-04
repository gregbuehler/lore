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

func TestWriteConfigFileWritesYAMLData(t *testing.T) {
	path := filepath.Join(t.TempDir(), LoreDir, ConfigFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}

	if err := writeConfigFile(path, []byte("vault:\n  path: /tmp/vault\n")); err != nil {
		t.Fatalf("writeConfigFile returned error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if string(data) != "vault:\n  path: /tmp/vault\n" {
		t.Fatalf("config content = %q", data)
	}
}

func TestSubscriptionContentPathDefaultsToCheckoutPath(t *testing.T) {
	sub := SubscriptionConfig{Path: "/repo/library"}

	if got := sub.ContentRoot(); got != "." {
		t.Fatalf("ContentRoot() = %q, want .", got)
	}
	if got := sub.ContentPath(); got != "/repo/library" {
		t.Fatalf("ContentPath() = %q, want checkout path", got)
	}
}

func TestSubscriptionContentPathUsesRelativeRoot(t *testing.T) {
	sub := SubscriptionConfig{Path: "/repo/citizen", Root: "./docs"}

	if got := sub.ContentRoot(); got != "docs" {
		t.Fatalf("ContentRoot() = %q, want docs", got)
	}
	if got := sub.ContentPath(); got != "/repo/citizen/docs" {
		t.Fatalf("ContentPath() = %q, want docs path", got)
	}
}
