package lore

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gbuehler/lore/internal/config"
	"github.com/spf13/cobra"
)

var skillsCmd = &cobra.Command{
	Use:   "skills [library]",
	Short: "List or read library skills",
	Long: `Shows available skills from a library or all subscribed libraries.

Skills are procedural knowledge — they teach agents and humans how to
answer specific categories of questions about a domain. Unlike entity
pages (which store facts), skills encode retrieval paths and operational
recipes.

Skills live in each library's skills/ directory and are declared in
library.yaml.

Examples:
  lore skills                          # list all skills from all libraries
  lore skills my-environments          # list skills in one library
  lore skills my-environments deployed-versions   # show a skill`,
	Args: cobra.MaximumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		vaultPath, err := config.FindVault()
		if err != nil {
			return err
		}
		cfg, err := config.Load(vaultPath)
		if err != nil {
			return err
		}

		if len(args) == 2 {
			// Show a specific skill
			return showSkill(cfg, args[0], args[1])
		}

		if len(args) == 1 {
			// List skills from one library
			sub := findSubscription(cfg, args[0])
			if sub == nil {
				return fmt.Errorf("library %q not found in subscriptions", args[0])
			}
			return listLibrarySkills(sub.Name, sub.Path)
		}

		// List skills from all libraries
		for _, sub := range cfg.Subscriptions {
			if err := listLibrarySkills(sub.Name, sub.Path); err != nil {
				fmt.Printf("  warning: %s: %v\n", sub.Name, err)
			}
		}
		return nil
	},
}

// skillDef represents a skill declaration from library.yaml.
type skillDef struct {
	Name        string
	File        string
	Description string
}

// loadSkillDefs parses the skills section from library.yaml.
func loadSkillDefs(libPath string) []skillDef {
	raw, err := os.ReadFile(filepath.Join(libPath, "library.yaml"))
	if err != nil {
		return nil
	}

	var skills []skillDef
	var current *skillDef
	inSkills := false

	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)

		if trimmed == "skills:" {
			inSkills = true
			continue
		}

		// Exit on next top-level key
		if inSkills && len(line) > 0 && line[0] != ' ' && line[0] != '\t' && !strings.HasPrefix(trimmed, "#") {
			break
		}

		if !inSkills {
			continue
		}

		if strings.HasPrefix(trimmed, "- name:") {
			if current != nil {
				skills = append(skills, *current)
			}
			current = &skillDef{
				Name: stripYAMLQuotes(strings.TrimPrefix(trimmed, "- name:")),
			}
			continue
		}

		if current == nil {
			continue
		}

		if strings.HasPrefix(trimmed, "file:") {
			current.File = stripYAMLQuotes(strings.TrimPrefix(trimmed, "file:"))
			continue
		}

		if strings.HasPrefix(trimmed, "description:") {
			current.Description = stripYAMLQuotes(strings.TrimPrefix(trimmed, "description:"))
			continue
		}
	}

	if current != nil {
		skills = append(skills, *current)
	}

	return skills
}

func listLibrarySkills(name, libPath string) error {
	skills := loadSkillDefs(libPath)
	if len(skills) == 0 {
		fmt.Printf("%s: no skills configured\n", name)
		return nil
	}

	fmt.Printf("%s:\n", name)
	for _, s := range skills {
		fmt.Printf("  %-25s %s\n", s.Name, s.Description)
	}
	fmt.Println()
	return nil
}

func showSkill(cfg *config.Config, libraryName, skillName string) error {
	sub := findSubscription(cfg, libraryName)
	if sub == nil {
		return fmt.Errorf("library %q not found in subscriptions", libraryName)
	}

	skills := loadSkillDefs(sub.Path)
	for _, s := range skills {
		if s.Name == skillName {
			skillPath := filepath.Join(sub.Path, s.File)
			data, err := os.ReadFile(skillPath)
			if err != nil {
				return fmt.Errorf("reading skill %s: %w", s.Name, err)
			}
			fmt.Println(string(data))
			return nil
		}
	}

	return fmt.Errorf("skill %q not found in library %q", skillName, libraryName)
}

// buildSkillsReference generates a markdown block listing all available skills
// for inclusion in CLAUDE.md or context packages. This is what teaches an agent
// what capabilities are available.
func buildSkillsReference(cfg *config.Config) string {
	var b strings.Builder
	any := false

	for _, sub := range cfg.Subscriptions {
		skills := loadSkillDefs(sub.Path)
		if len(skills) == 0 {
			continue
		}

		if !any {
			b.WriteString("## Available Skills\n\n")
			b.WriteString("These skills encode procedural knowledge for answering domain-specific questions.\n")
			b.WriteString("Read the skill file for the full retrieval procedure.\n\n")
			any = true
		}

		b.WriteString(fmt.Sprintf("### %s\n\n", sub.Name))
		for _, s := range skills {
			skillPath := filepath.Join(sub.Path, s.File)
			b.WriteString(fmt.Sprintf("- **%s** — %s\n", s.Name, s.Description))
			b.WriteString(fmt.Sprintf("  `%s`\n", skillPath))
		}
		b.WriteString("\n")
	}

	return b.String()
}

func init() {
	// No subcommands — skills is a top-level command
}
