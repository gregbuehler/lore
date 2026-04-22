package lore

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gbuehler/lore/internal/config"
	"github.com/spf13/cobra"
)

var contextCmd = &cobra.Command{
	Use:   "context",
	Short: "Generate .lore/LORE.md and wire it into .claude/CLAUDE.md",
	Long: `Generates .lore/LORE.md with library discovery context: subscribed
libraries, available skills, watched repos, and lore CLI commands.

Each library publishes its own excerpt.md (via 'lore library index').
This command embeds those excerpts into LORE.md.

If .claude/CLAUDE.md exists and doesn't already import LORE.md, the
import directive is appended so Claude Code loads it automatically.

Examples:
  lore vault context     # regenerate .lore/LORE.md`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		vaultPath, err := config.FindVault()
		if err != nil {
			return err
		}
		cfg, err := config.Load(vaultPath)
		if err != nil {
			return err
		}

		content := generateVaultContext(cfg)

		lorePath := filepath.Join(vaultPath, ".lore", "LORE.md")
		if err := os.WriteFile(lorePath, []byte(content), 0644); err != nil {
			return fmt.Errorf("writing LORE.md: %w", err)
		}
		fmt.Printf("Updated %s\n", lorePath)

		// Ensure .claude/CLAUDE.md imports LORE.md
		claudePath := filepath.Join(vaultPath, ".claude", "CLAUDE.md")
		if err := ensureLoreImport(claudePath); err != nil {
			fmt.Printf("Note: %v\n", err)
			fmt.Printf("Add this line to your CLAUDE.md to import lore context:\n")
			fmt.Printf("  @../.lore/LORE.md\n")
		}

		return nil
	},
}

func generateVaultContext(cfg *config.Config) string {
	var b strings.Builder

	b.WriteString("## Lore Libraries and Skills\n\n")
	b.WriteString("This vault has lore-managed shared library subscriptions.\n")
	b.WriteString("Libraries are team knowledge bases with entity pages, skills, and maintenance workflows.\n\n")

	// Libraries — embed excerpts with subscriber-local paths
	hasSkills := false
	if len(cfg.Subscriptions) > 0 {
		b.WriteString("## Subscribed Libraries\n\n")
		for _, sub := range cfg.Subscriptions {
			excerptPath := filepath.Join(sub.Path, "excerpt.md")
			excerpt, err := os.ReadFile(excerptPath)
			if err == nil {
				content := string(excerpt)

				// Bump all headings by two levels so they nest under
				// ## Subscribed Libraries: # → ###, ## → ####
				var indented []string
				for _, line := range strings.Split(content, "\n") {
					if strings.HasPrefix(line, "# ") {
						line = "##" + line
					} else if strings.HasPrefix(line, "## ") {
						line = "##" + line
					}
					indented = append(indented, line)
				}
				content = strings.Join(indented, "\n")

				// Inject subscriber-local paths after the heading line.
				// The excerpt's first line is "### <name>\n" after indenting.
				lines := strings.SplitN(content, "\n", 2)
				b.WriteString(lines[0] + "\n\n")
				b.WriteString(fmt.Sprintf("- **Path:** `%s`\n", sub.Path))
				b.WriteString(fmt.Sprintf("- **Agent instructions:** `%s/CLAUDE.md`\n", sub.Path))
				b.WriteString(fmt.Sprintf("- **Index:** `%s/Wiki/index.md`\n", sub.Path))
				if len(lines) > 1 {
					// Resolve relative skill File: paths to absolute
					rest := resolveSkillPaths(lines[1], sub.Path)
					b.WriteString(rest)
				}
				b.WriteString("\n")

				// Check if any library has skills
				if strings.Contains(content, "#### Skills") {
					hasSkills = true
				}
			} else {
				// No excerpt — fall back to basic info
				b.WriteString(fmt.Sprintf("### %s\n\n", sub.Name))
				b.WriteString(fmt.Sprintf("- **Path:** `%s`\n", sub.Path))
				b.WriteString(fmt.Sprintf("- **Access:** %s\n", sub.Access))
				b.WriteString(fmt.Sprintf("- **Agent instructions:** `%s/CLAUDE.md`\n\n", sub.Path))
			}
		}

		if hasSkills {
			b.WriteString("### Using Skills\n\n")
			b.WriteString("Skills are procedural knowledge curated in libraries. They teach you\n")
			b.WriteString("how to answer specific categories of questions about a domain.\n\n")
			b.WriteString("When a user asks a question that matches a skill's trigger, read the\n")
			b.WriteString("skill file and follow its procedure rather than guessing.\n\n")
		}
	}

	// Vault-level skills (from .lore/skills/)
	vaultSkills := loadVaultSkills(cfg.Vault.Path)
	if len(vaultSkills) > 0 {
		b.WriteString("### Vault Skills\n\n")
		b.WriteString("These skills are vault-level — they apply across all libraries.\n\n")
		for _, vs := range vaultSkills {
			b.WriteString(fmt.Sprintf("- **%s** — %s\n", vs.name, vs.description))
			b.WriteString(fmt.Sprintf("  Read: `%s`\n", vs.path))
			if vs.trigger != "" {
				b.WriteString(fmt.Sprintf("  Trigger: %s\n", vs.trigger))
			}
		}
		b.WriteString("\n")

		if !hasSkills {
			b.WriteString("### Using Skills\n\n")
			b.WriteString("Skills are procedural knowledge curated in libraries. They teach you\n")
			b.WriteString("how to answer specific categories of questions about a domain.\n\n")
			b.WriteString("When a user asks a question that matches a skill's trigger, read the\n")
			b.WriteString("skill file and follow its procedure rather than guessing.\n\n")
		}
	}

	// Maintenance workflows
	b.WriteString("### Maintenance Workflows\n\n")
	b.WriteString("```bash\n")
	b.WriteString("# Vault\n")
	b.WriteString("lore vault status                       # show vault and library status\n")
	b.WriteString("lore vault lint                         # check vault page health\n")
	b.WriteString("lore vault context                      # regenerate this file\n")
	b.WriteString("\n")
	b.WriteString("# Library curation\n")
	b.WriteString("lore library review <library>           # surface daily log evidence not yet in library\n")
	b.WriteString("lore library maintain <library>         # synthesize daily log evidence into library pages\n")
	b.WriteString("lore library watch <library>            # update pages from source repo changes\n")
	b.WriteString("lore library lint <library>             # check page health\n")
	b.WriteString("lore library lint <library> --fix       # auto-fix formatting issues\n")
	b.WriteString("lore library index <library>            # rebuild Wiki/index.md + excerpt\n")
	b.WriteString("lore library publish --to <library>     # publish a vault page to a library\n")
	b.WriteString("lore library skills                     # list available skills\n")
	b.WriteString("```\n\n")

	// Meta-index
	if cfg.MetaIndex != "" {
		b.WriteString("### Meta-Index\n\n")
		b.WriteString(fmt.Sprintf("Cross-library navigation: `%s`\n\n", cfg.MetaIndex))
	}

	b.WriteString(fmt.Sprintf("---\n*Generated by `lore vault context` on %s.*\n", time.Now().Format("2006-01-02")))

	return b.String()
}

// vaultSkill represents a skill found in the vault's .lore/skills/ directory.
type vaultSkill struct {
	name        string
	description string
	trigger     string
	path        string // absolute path to the skill file
}

// loadVaultSkills scans .lore/skills/ for skill markdown files with frontmatter.
func loadVaultSkills(vaultPath string) []vaultSkill {
	skillsDir := filepath.Join(vaultPath, ".lore", "skills")
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		return nil
	}

	var skills []vaultSkill
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(skillsDir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		content := string(data)
		if !strings.HasPrefix(content, "---\n") {
			continue
		}
		end := strings.Index(content[4:], "\n---")
		if end < 0 {
			continue
		}
		block := content[4 : 4+end]

		vs := vaultSkill{path: path}
		for _, line := range strings.Split(block, "\n") {
			if strings.HasPrefix(line, "name:") {
				vs.name = stripYAMLQuotes(strings.TrimPrefix(line, "name:"))
			} else if strings.HasPrefix(line, "description:") {
				vs.description = stripYAMLQuotes(strings.TrimPrefix(line, "description:"))
			} else if strings.HasPrefix(line, "trigger:") {
				vs.trigger = stripYAMLQuotes(strings.TrimPrefix(line, "trigger:"))
			}
		}
		if vs.name != "" {
			skills = append(skills, vs)
		}
	}
	return skills
}

// resolveSkillPaths replaces relative "File: `skills/...`" references in an
// excerpt with absolute paths for the subscriber's local checkout.
func resolveSkillPaths(content, libPath string) string {
	var out []string
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "File: `") && strings.HasSuffix(trimmed, "`") {
			relPath := trimmed[len("File: `") : len(trimmed)-1]
			absPath := filepath.Join(libPath, relPath)
			line = strings.Replace(line, trimmed, fmt.Sprintf("Read: `%s`", absPath), 1)
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

const loreImportDirective = "@../.lore/LORE.md"

// ensureLoreImport checks if .claude/CLAUDE.md already imports LORE.md.
// If not, appends the import directive.
func ensureLoreImport(claudePath string) error {
	data, err := os.ReadFile(claudePath)
	if err != nil {
		return fmt.Errorf("could not read %s: %w", claudePath, err)
	}

	content := string(data)
	if strings.Contains(content, loreImportDirective) {
		return nil
	}

	// Append import at the end
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += "\n" + loreImportDirective + "\n"

	if err := os.WriteFile(claudePath, []byte(content), 0644); err != nil {
		return fmt.Errorf("could not update %s: %w", claudePath, err)
	}

	fmt.Printf("Added lore import to %s\n", claudePath)
	return nil
}

func init() {
}
