package lore

import (
	"strings"
	"testing"
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
