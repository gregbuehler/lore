package agent

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/gregbuehler/lore/internal/config"
)

const (
	ProviderClaude = "claude"
	ProviderCodex  = "codex"
	ProviderCustom = "custom"
	ProviderNone   = "none"
)

type Options struct {
	DangerouslySkipPermissions bool
}

type Invocation struct {
	Command string
	Args    []string
	Dir     string
	Stdin   string
}

func BuildInvocation(cfg config.AgentConfig, workDir, prompt string, opts Options) (Invocation, error) {
	provider := normalizeProvider(cfg)
	if provider == ProviderNone {
		return Invocation{}, fmt.Errorf("agent provider is none")
	}

	switch provider {
	case ProviderClaude:
		return buildClaudeInvocation(cfg, workDir, prompt, opts), nil
	case ProviderCodex:
		return buildCodexInvocation(cfg, workDir, prompt, opts), nil
	case ProviderCustom:
		return buildCustomInvocation(cfg, workDir, prompt, opts)
	default:
		return Invocation{}, fmt.Errorf("unknown agent provider %q", cfg.Provider)
	}
}

func Label(cfg config.AgentConfig) string {
	provider := normalizeProvider(cfg)
	if cfg.Command != "" {
		return cfg.Command
	}
	switch provider {
	case ProviderCodex:
		return "codex"
	case ProviderCustom:
		return "custom"
	case ProviderNone:
		return "none"
	default:
		return "claude"
	}
}

func Run(cfg config.AgentConfig, workDir, prompt string, opts Options, stdout, stderr io.Writer) error {
	inv, err := BuildInvocation(cfg, workDir, prompt, opts)
	if err != nil {
		return err
	}
	cmd := exec.Command(inv.Command, inv.Args...)
	cmd.Dir = inv.Dir
	if inv.Stdin != "" {
		cmd.Stdin = strings.NewReader(inv.Stdin)
	}
	if stdout == nil {
		stdout = os.Stdout
	}
	if stderr == nil {
		stderr = os.Stderr
	}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

func normalizeProvider(cfg config.AgentConfig) string {
	provider := strings.ToLower(strings.TrimSpace(cfg.Provider))
	if provider != "" {
		return provider
	}
	command := strings.ToLower(filepath.Base(cfg.Command))
	switch {
	case strings.Contains(command, "codex"):
		return ProviderCodex
	case strings.Contains(command, "claude"):
		return ProviderClaude
	case cfg.Command != "":
		return ProviderCustom
	default:
		return ProviderClaude
	}
}

func buildClaudeInvocation(cfg config.AgentConfig, workDir, prompt string, opts Options) Invocation {
	command := cfg.Command
	if command == "" {
		command = "claude"
	}
	args := append([]string{}, cfg.Args...)
	if opts.DangerouslySkipPermissions {
		args = append(args, "--dangerously-skip-permissions")
	}
	args = append(args, "-p", prompt)
	return Invocation{Command: command, Args: args, Dir: workDir}
}

func buildCodexInvocation(cfg config.AgentConfig, workDir, prompt string, opts Options) Invocation {
	command := cfg.Command
	if command == "" {
		command = "codex"
	}
	sandbox := cfg.Sandbox
	if sandbox == "" {
		sandbox = "workspace-write"
	}
	approval := cfg.Approval
	if approval == "" {
		approval = "never"
	}

	args := append([]string{"exec"}, cfg.Args...)
	args = append(args, "--cd", workDir, "--sandbox", sandbox, "--ask-for-approval", approval)
	if opts.DangerouslySkipPermissions {
		args = append(args, "--dangerously-bypass-approvals-and-sandbox")
	}
	args = append(args, "-")
	return Invocation{Command: command, Args: args, Dir: workDir, Stdin: prompt}
}

func buildCustomInvocation(cfg config.AgentConfig, workDir, prompt string, opts Options) (Invocation, error) {
	if cfg.Command == "" {
		return Invocation{}, fmt.Errorf("custom agent command is required")
	}
	if opts.DangerouslySkipPermissions {
		return Invocation{}, fmt.Errorf("custom agent does not support dangerous permission bypass; configure explicit custom args instead")
	}
	args := append([]string{}, cfg.Args...)
	stdin := prompt
	for i, arg := range args {
		arg = strings.ReplaceAll(arg, "{workdir}", workDir)
		if strings.Contains(arg, "{prompt}") {
			arg = strings.ReplaceAll(arg, "{prompt}", prompt)
			stdin = ""
		}
		args[i] = arg
	}
	return Invocation{Command: cfg.Command, Args: args, Dir: workDir, Stdin: stdin}, nil
}
