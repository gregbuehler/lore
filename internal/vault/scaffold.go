package vault

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gregbuehler/lore/internal/config"
	"github.com/gregbuehler/lore/internal/pathutil"
)

// ScaffoldOptions contains the user-provided configuration for vault init.
type ScaffoldOptions struct {
	Name          string
	Email         string
	Host          string
	Entities      []string // e.g., ["people", "services", "tooling", "infrastructure"]
	Adopt         bool
	AgentProvider string // claude, codex, none
}

// Scaffold creates a new vault directory structure.
// If Adopt is true, it adds lore config to an existing directory without
// overwriting any files that already exist.
func Scaffold(vaultPath string, opts ScaffoldOptions) error {
	abs, err := filepath.Abs(vaultPath)
	if err != nil {
		return fmt.Errorf("resolving path: %w", err)
	}

	if _, err := os.Stat(config.ConfigPath(abs)); err == nil {
		return fmt.Errorf("vault already initialized at %s (found .lore/config.yaml)", abs)
	}

	if opts.Adopt {
		info, err := os.Stat(abs)
		if err != nil || !info.IsDir() {
			return fmt.Errorf("%s does not exist or is not a directory", abs)
		}
	}

	agentProvider := normalizeAgentProvider(opts.AgentProvider)
	if agentProvider == "" {
		return fmt.Errorf("unsupported agent provider %q (use claude, codex, or none)", opts.AgentProvider)
	}

	dirs := []string{
		filepath.Join(abs, "Daily Log"),
		filepath.Join(abs, "Weekly Digest"),
		filepath.Join(abs, "Threads"),
		filepath.Join(abs, "Templates"),
		filepath.Join(abs, "sources"),
		filepath.Join(abs, config.LoreDir),
		filepath.Join(abs, config.LoreDir, "skills"),
	}
	if agentProvider == "claude" {
		dirs = append(dirs, filepath.Join(abs, ".claude", "commands"))
	}
	dirs = append(dirs, filepath.Join(abs, "Wiki"))
	for _, et := range opts.Entities {
		dirs = append(dirs, filepath.Join(abs, "Wiki", entityDirName(et)))
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("creating %s: %w", d, err)
		}
	}

	today := time.Now().Format("2006-01-02")

	cfg := &config.Config{
		Vault: config.VaultConfig{
			Path: abs,
		},
		DefaultHost: opts.Host,
		MetaIndex:   filepath.Join(config.LibrariesDir(), "meta-index.md"),
		Agent:       defaultAgentConfig(agentProvider),
	}
	if opts.Name != "" || opts.Email != "" {
		cfg.Identity = config.IdentityConfig{
			Name:  opts.Name,
			Email: opts.Email,
		}
	}
	if err := cfg.Save(abs); err != nil {
		return err
	}

	if agentProvider == "claude" {
		if err := writeIfNotExists(filepath.Join(abs, ".claude", "CLAUDE.md"),
			generateClaudeMD(opts, today)); err != nil {
			return err
		}
		if err := writeIfNotExists(filepath.Join(abs, ".claude", "commands", "daily-log.md"),
			generateDailyLogCommand(opts)); err != nil {
			return err
		}
		if err := writeIfNotExists(filepath.Join(abs, ".claude", "commands", "weekly-digest.md"),
			generateWeeklyDigestCommand()); err != nil {
			return err
		}
		if err := writeIfNotExists(filepath.Join(abs, ".claude", "commands", "lint.md"),
			generateLintCommand(opts)); err != nil {
			return err
		}
		if err := writeIfNotExists(filepath.Join(abs, ".claude", "commands", "wiki-update.md"),
			generateWikiUpdateCommand(opts)); err != nil {
			return err
		}
		if err := writeIfNotExists(filepath.Join(abs, ".claude", "commands", "capture.md"),
			generateCaptureCommand()); err != nil {
			return err
		}
	}
	if agentProvider == "codex" {
		if err := writeIfNotExists(filepath.Join(abs, "AGENTS.md"),
			generateAgentsMD(opts, today)); err != nil {
			return err
		}
	}

	if err := writeIfNotExists(filepath.Join(abs, "Templates", "Daily Log Template.md"),
		generateDailyLogTemplate()); err != nil {
		return err
	}
	if err := writeIfNotExists(filepath.Join(abs, "Templates", "Thread Template.md"),
		generateThreadTemplate()); err != nil {
		return err
	}
	if err := writeIfNotExists(filepath.Join(abs, "Wiki", "index.md"),
		generateWikiIndex(opts, today)); err != nil {
		return err
	}
	if err := writeIfNotExists(filepath.Join(abs, "README.md"),
		generateReadme(opts)); err != nil {
		return err
	}
	if err := writeIfNotExists(filepath.Join(abs, "log.md"),
		"# Vault Activity Log\n\nAppend-only record of lore operations.\n\n---\n"); err != nil {
		return err
	}
	if err := writeIfNotExists(filepath.Join(abs, config.LoreDir, "skills", "contribute-knowledge.md"),
		generateContributeSkill()); err != nil {
		return err
	}

	return nil
}

func normalizeAgentProvider(provider string) string {
	normalized := strings.ToLower(strings.TrimSpace(provider))
	switch normalized {
	case "":
		return "claude"
	case "claude", "codex", "none":
		return normalized
	default:
		return ""
	}
}

func defaultAgentConfig(provider string) config.AgentConfig {
	switch provider {
	case "claude":
		return config.AgentConfig{Provider: "claude", Command: "claude"}
	case "codex":
		return config.AgentConfig{
			Provider: "codex",
			Command:  "codex",
			Sandbox:  "workspace-write",
			Approval: "never",
		}
	case "none":
		return config.AgentConfig{Provider: "none"}
	default:
		return config.AgentConfig{}
	}
}

func writeIfNotExists(path, content string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	return pathutil.AtomicWriteFile(path, []byte(content), 0o644)
}

// entityDirName returns the Wiki subdirectory name for an entity type.
func entityDirName(entityType string) string {
	et := strings.TrimSpace(entityType)
	switch et {
	case "person", "people":
		return "People"
	case "service", "services":
		return "Services"
	case "tool", "tooling":
		return "Tooling"
	case "infrastructure":
		return "Infrastructure"
	case "environment", "environments":
		return "Environments"
	case "customer", "customers":
		return "Customers"
	case "vendor", "vendors":
		return "Vendors"
	case "organization", "organizations":
		return "Organizations"
	case "concept", "concepts":
		return "Concepts"
	default:
		if len(et) == 0 {
			return et
		}
		return strings.ToUpper(et[:1]) + et[1:]
	}
}

// entityTypeName returns the singular entity_type value for frontmatter.
func entityTypeName(entityType string) string {
	et := strings.TrimSpace(entityType)
	switch et {
	case "people", "person":
		return "person"
	case "services", "service":
		return "service"
	case "tooling", "tool":
		return "tool"
	case "infrastructure":
		return "infrastructure"
	case "environments", "environment":
		return "environment"
	case "customers", "customer":
		return "customer"
	case "vendors", "vendor":
		return "vendor"
	case "organizations", "organization":
		return "organization"
	case "concepts", "concept":
		return "concept"
	default:
		return et
	}
}
