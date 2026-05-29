package lore

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/gbuehler/lore/internal/config"
	"github.com/gbuehler/lore/internal/vault"
	"github.com/spf13/cobra"
)

var (
	initAdopt    bool
	initName     string
	initEmail    string
	initHost     string
	initEntities string
	initAgent    string
)

var initCmd = &cobra.Command{
	Use:   "init [path]",
	Short: "Initialize a new lore vault",
	Long: `Creates a new lore vault at the given path.

If no path is given, defaults to ~/Documents/lore/<username>.

Scaffolds a complete personal knowledge vault with:
  .claude/CLAUDE.md           Agent operating manual
  .claude/commands/           Slash commands (daily-log, weekly-digest, lint, wiki-update)
  .lore/config.yaml           CLI configuration
  .lore/skills/               Vault-level skills
  Wiki/                       Entity pages organized by type
  Wiki/index.md               Navigation index
  Templates/                  Document templates
  Daily Log/                  Journal entries
  Weekly Digest/              Weekly summaries
  sources/                    Raw source material
  log.md                      Append-only activity log

You can pass all options as flags for non-interactive use:
  lore vault init ~/vault --name "Jane Smith" --email jane@co.com --entities people,services,tooling
  lore vault init ~/vault --agent codex

Use --adopt to add lore to an existing directory (e.g., an Obsidian vault).
This creates only the .lore/ config and any missing scaffolding without
overwriting existing files.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path := defaultVaultPath()
		if len(args) > 0 {
			path = args[0]
		}

		opts := vault.ScaffoldOptions{
			Name:          initName,
			Email:         initEmail,
			Host:          initHost,
			Entities:      nil,
			Adopt:         initAdopt,
			AgentProvider: initAgent,
		}

		if initEntities != "" {
			opts.Entities = parseCSV(initEntities)
		}

		// Interactive prompts for missing values (skip if all flags provided)
		if !allFlagsProvided(opts) && isInteractive() {
			promptMissing(&opts)
		}

		// Apply defaults for anything still empty
		applyDefaults(&opts)

		if err := vault.Scaffold(path, opts); err != nil {
			return err
		}

		if opts.Adopt {
			fmt.Printf("Existing vault adopted at %s\n", path)
			fmt.Println("Created .lore/config.yaml (existing files untouched)")
		} else {
			fmt.Printf("Vault initialized at %s\n", path)
		}
		fmt.Println("\nNext steps:")
		switch opts.AgentProvider {
		case "codex":
			fmt.Println("  1. Review and customize AGENTS.md")
		case "none":
			fmt.Println("  1. Configure your preferred agent instructions if needed")
		default:
			fmt.Println("  1. Review and customize .claude/CLAUDE.md")
		}
		fmt.Println("  2. Run 'lore subscribe <org/repo>' to subscribe to a library")
		fmt.Println("  3. Run 'lore vault context' to generate agent context")
		return nil
	},
}

func parseCSV(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		t := strings.TrimSpace(part)
		if t != "" {
			out = append(out, t)
		}
	}
	return out
}

func allFlagsProvided(opts vault.ScaffoldOptions) bool {
	return opts.Name != "" && opts.Email != "" && len(opts.Entities) > 0
}

func isInteractive() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func promptMissing(opts *vault.ScaffoldOptions) {
	reader := bufio.NewReader(os.Stdin)

	if opts.Name == "" {
		opts.Name = prompt(reader, "Name", inferName())
	}
	if opts.Email == "" {
		opts.Email = prompt(reader, "Email", inferEmail())
	}
	if opts.Host == "" {
		detected := config.DetectHost()
		if detected != "" {
			opts.Host = prompt(reader, "Git host (GHE/GitLab/etc)", detected)
		} else {
			opts.Host = prompt(reader, "Git host (GHE/GitLab/etc, blank to skip)", "")
		}
	}
	if len(opts.Entities) == 0 {
		defaultEntities := "people, services, tooling, infrastructure, environments, organizations, customers, vendors, concepts"
		raw := prompt(reader, "Entity types (comma-separated)", defaultEntities)
		opts.Entities = parseCSV(raw)
	}
}

func prompt(reader *bufio.Reader, label, defaultVal string) string {
	if defaultVal != "" {
		fmt.Printf("  %s [%s]: ", label, defaultVal)
	} else {
		fmt.Printf("  %s: ", label)
	}
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return defaultVal
	}
	return line
}

func applyDefaults(opts *vault.ScaffoldOptions) {
	if opts.Name == "" {
		opts.Name = inferName()
	}
	if opts.Email == "" {
		opts.Email = inferEmail()
	}
	if opts.Host == "" {
		// Auto-detect from GH_HOST or ~/.config/gh/hosts.yml; empty is fine
		opts.Host = config.DetectHost()
	}
	if len(opts.Entities) == 0 {
		opts.Entities = []string{
			"people", "services", "tooling", "infrastructure",
			"environments", "organizations", "customers", "vendors", "concepts",
		}
	}
	if opts.AgentProvider == "" {
		opts.AgentProvider = "claude"
	} else {
		opts.AgentProvider = strings.ToLower(strings.TrimSpace(opts.AgentProvider))
	}
}

// defaultVaultPath returns ~/Documents/lore/<username> as the default vault location.
func defaultVaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	username := ""
	if u, err := user.Current(); err == nil {
		username = u.Username
	}
	if username == "" {
		return "."
	}
	return filepath.Join(home, "Documents", "lore", username)
}

// inferName tries git config user.name, then falls back to the OS user's display name.
func inferName() string {
	if out, err := exec.Command("git", "config", "user.name").Output(); err == nil {
		if name := strings.TrimSpace(string(out)); name != "" {
			return name
		}
	}
	if u, err := user.Current(); err == nil && u.Name != "" {
		return u.Name
	}
	return ""
}

// inferEmail tries git config user.email.
func inferEmail() string {
	if out, err := exec.Command("git", "config", "user.email").Output(); err == nil {
		if email := strings.TrimSpace(string(out)); email != "" {
			return email
		}
	}
	return ""
}

func init() {
	initCmd.Flags().BoolVar(&initAdopt, "adopt", false, "Adopt an existing directory as a lore vault")
	initCmd.Flags().StringVar(&initName, "name", "", "Your name (for config and templates)")
	initCmd.Flags().StringVar(&initEmail, "email", "", "Your email (for config)")
	initCmd.Flags().StringVar(&initHost, "host", "", "Git forge host (GHE, GitLab, etc). Auto-detected from gh CLI if available")
	initCmd.Flags().StringVar(&initEntities, "entities", "", "Entity types, comma-separated (e.g., people,services,tooling)")
	initCmd.Flags().StringVar(&initAgent, "agent", "claude", "Agent scaffold to create: claude, codex, or none")
}
