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
