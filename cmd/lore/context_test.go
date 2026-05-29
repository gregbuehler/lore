package lore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureCodexImportCreatesAgentsMD(t *testing.T) {
	vaultPath := t.TempDir()

	if err := ensureCodexImport(vaultPath); err != nil {
		t.Fatalf("ensureCodexImport returned error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(vaultPath, "AGENTS.md"))
	if err != nil {
		t.Fatalf("reading AGENTS.md: %v", err)
	}
	if !strings.Contains(string(data), "@.lore/LORE.md") {
		t.Fatalf("AGENTS.md does not reference .lore/LORE.md:\n%s", data)
	}
}

func TestEnsureCodexImportIsIdempotent(t *testing.T) {
	vaultPath := t.TempDir()
	agentsPath := filepath.Join(vaultPath, "AGENTS.md")
	if err := os.WriteFile(agentsPath, []byte("# Project Instructions\n\nExisting guidance.\n"), 0o644); err != nil {
		t.Fatalf("writing AGENTS.md: %v", err)
	}

	if err := ensureCodexImport(vaultPath); err != nil {
		t.Fatalf("first ensureCodexImport returned error: %v", err)
	}
	if err := ensureCodexImport(vaultPath); err != nil {
		t.Fatalf("second ensureCodexImport returned error: %v", err)
	}

	data, err := os.ReadFile(agentsPath)
	if err != nil {
		t.Fatalf("reading AGENTS.md: %v", err)
	}
	if got := strings.Count(string(data), "@.lore/LORE.md"); got != 1 {
		t.Fatalf("AGENTS.md has %d lore imports, want 1:\n%s", got, data)
	}
	if !strings.Contains(string(data), "Existing guidance.") {
		t.Fatalf("AGENTS.md did not preserve existing content:\n%s", data)
	}
}

func TestResolveContextAgentTargetsAutoUsesExistingProviderFiles(t *testing.T) {
	vaultPath := t.TempDir()
	if err := os.MkdirAll(filepath.Join(vaultPath, ".claude"), 0o755); err != nil {
		t.Fatalf("mkdir .claude: %v", err)
	}
	if err := os.WriteFile(filepath.Join(vaultPath, ".claude", "CLAUDE.md"), []byte("# Claude\n"), 0o644); err != nil {
		t.Fatalf("write CLAUDE.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(vaultPath, "AGENTS.md"), []byte("# Codex\n"), 0o644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}

	targets, err := resolveContextAgentTargets(vaultPath, "codex", "auto")
	if err != nil {
		t.Fatalf("resolveContextAgentTargets: %v", err)
	}
	if got := strings.Join(targets, ","); got != "claude,codex" {
		t.Fatalf("targets = %q, want claude,codex", got)
	}
}

func TestResolveContextAgentTargetsAutoFallsBackToEffectiveProvider(t *testing.T) {
	vaultPath := t.TempDir()

	targets, err := resolveContextAgentTargets(vaultPath, "codex", "auto")
	if err != nil {
		t.Fatalf("resolveContextAgentTargets: %v", err)
	}
	if got := strings.Join(targets, ","); got != "codex" {
		t.Fatalf("targets = %q, want codex", got)
	}
}

func TestResolveContextAgentTargetsExplicitAll(t *testing.T) {
	targets, err := resolveContextAgentTargets(t.TempDir(), "none", "all")
	if err != nil {
		t.Fatalf("resolveContextAgentTargets: %v", err)
	}
	if got := strings.Join(targets, ","); got != "claude,codex" {
		t.Fatalf("targets = %q, want claude,codex", got)
	}
}
