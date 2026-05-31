package agent

import (
	"reflect"
	"testing"

	"github.com/gregbuehler/lore/internal/config"
)

func TestBuildInvocationClaude(t *testing.T) {
	inv, err := BuildInvocation(config.AgentConfig{Provider: "claude"}, "/repo", "prompt", Options{})
	if err != nil {
		t.Fatalf("BuildInvocation returned error: %v", err)
	}
	wantArgs := []string{"-p", "prompt"}
	if inv.Command != "claude" || !reflect.DeepEqual(inv.Args, wantArgs) || inv.Stdin != "" {
		t.Fatalf("invocation = %#v", inv)
	}
}

func TestBuildInvocationCodexUsesExecAndStdin(t *testing.T) {
	inv, err := BuildInvocation(config.AgentConfig{Provider: "codex"}, "/repo", "prompt", Options{})
	if err != nil {
		t.Fatalf("BuildInvocation returned error: %v", err)
	}
	wantArgs := []string{"exec", "--cd", "/repo", "--sandbox", "workspace-write", "--ask-for-approval", "never", "-"}
	if inv.Command != "codex" || !reflect.DeepEqual(inv.Args, wantArgs) || inv.Stdin != "prompt" {
		t.Fatalf("invocation = %#v, want args %#v with stdin prompt", inv, wantArgs)
	}
}

func TestBuildInvocationCodexDangerousFlag(t *testing.T) {
	inv, err := BuildInvocation(config.AgentConfig{Provider: "codex"}, "/repo", "prompt", Options{DangerouslySkipPermissions: true})
	if err != nil {
		t.Fatalf("BuildInvocation returned error: %v", err)
	}
	if !contains(inv.Args, "--dangerously-bypass-approvals-and-sandbox") {
		t.Fatalf("codex args missing dangerous flag: %#v", inv.Args)
	}
}

func TestBuildInvocationInfersCodexFromCommand(t *testing.T) {
	inv, err := BuildInvocation(config.AgentConfig{Command: "codex"}, "/repo", "prompt", Options{})
	if err != nil {
		t.Fatalf("BuildInvocation returned error: %v", err)
	}
	if inv.Args[0] != "exec" {
		t.Fatalf("expected codex exec args, got %#v", inv.Args)
	}
}

func TestBuildInvocationCustomRejectsDangerousBypass(t *testing.T) {
	_, err := BuildInvocation(
		config.AgentConfig{Provider: "custom", Command: "runner"},
		"/repo",
		"prompt",
		Options{DangerouslySkipPermissions: true},
	)
	if err == nil {
		t.Fatal("expected custom agent dangerous bypass to be rejected")
	}
}

func TestBuildInvocationProviderUsesProviderDefaultWhenCommandEmpty(t *testing.T) {
	inv, err := BuildInvocation(config.AgentConfig{Provider: "codex"}, "/repo", "prompt", Options{})
	if err != nil {
		t.Fatalf("BuildInvocation returned error: %v", err)
	}
	if inv.Command != "codex" {
		t.Fatalf("Command = %q, want codex", inv.Command)
	}
}

func contains(items []string, needle string) bool {
	for _, item := range items {
		if item == needle {
			return true
		}
	}
	return false
}
