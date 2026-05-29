package lore

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gbuehler/lore/internal/config"
)

func TestBuildAgentLocalYAMLCodex(t *testing.T) {
	content, err := buildAgentLocalYAML("codex")
	if err != nil {
		t.Fatalf("buildAgentLocalYAML returned error: %v", err)
	}
	for _, want := range []string{
		"provider: codex",
		"command: codex",
		"sandbox: workspace-write",
		"approval: never",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("content missing %q:\n%s", want, content)
		}
	}
}

func TestBuildAgentLocalYAMLClaude(t *testing.T) {
	content, err := buildAgentLocalYAML("claude")
	if err != nil {
		t.Fatalf("buildAgentLocalYAML returned error: %v", err)
	}
	for _, want := range []string{"provider: claude", "command: claude"} {
		if !strings.Contains(content, want) {
			t.Fatalf("content missing %q:\n%s", want, content)
		}
	}
}

func TestBuildAgentLocalYAMLRejectsUnknownProvider(t *testing.T) {
	if _, err := buildAgentLocalYAML("wat"); err == nil {
		t.Fatal("expected unknown provider to fail")
	}
}

func TestBuildAgentLocalYAMLRejectsCustomProvider(t *testing.T) {
	if _, err := buildAgentLocalYAML("custom"); err == nil {
		t.Fatal("expected custom provider to fail")
	}
}

func TestPrintAgentLocalStatusShowsEnvironmentOverrideAndEffectiveConfig(t *testing.T) {
	vault := t.TempDir()
	if err := os.MkdirAll(filepath.Join(vault, config.LoreDir), 0o755); err != nil {
		t.Fatalf("mkdir .lore: %v", err)
	}
	if err := os.WriteFile(filepath.Join(vault, config.LoreDir, config.ConfigFile), []byte("vault:\n  path: "+vault+"\nagent:\n  provider: claude\n  command: claude\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(config.LocalPath(vault), []byte("agent:\n  provider: codex\n  command: codex\n"), 0o644); err != nil {
		t.Fatalf("write local config: %v", err)
	}
	t.Setenv("LORE_AGENT_PROVIDER", "custom")
	t.Setenv("LORE_AGENT_COMMAND", "local-agent")

	output := captureStdout(t, func() {
		if err := printAgentLocalStatus(vault); err != nil {
			t.Fatalf("printAgentLocalStatus: %v", err)
		}
	})

	for _, want := range []string{
		"Shared:      claude (claude)",
		"Local:       codex (codex)",
		"Environment: custom (local-agent)",
		"Effective:   custom (local-agent)",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stdout: %v", err)
	}
	os.Stdout = w
	defer func() {
		os.Stdout = oldStdout
	}()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close stdout writer: %v", err)
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("close stdout reader: %v", err)
	}
	return buf.String()
}
