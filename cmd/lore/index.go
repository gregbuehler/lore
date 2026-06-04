package lore

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gregbuehler/lore/internal/config"
	"github.com/gregbuehler/lore/internal/index"
	"github.com/gregbuehler/lore/internal/pathutil"
	"github.com/spf13/cobra"
)

var indexCmd = &cobra.Command{
	Use:   "index [library]",
	Short: "Rebuild library and meta indexes",
	Long: `Regenerates Wiki/index.md for the named library (or all libraries if
no name is given), then rebuilds the meta-index.

The library index is an auto-generated catalog of every page under Wiki/,
grouped by subdirectory, with entity type and aliases extracted from
frontmatter. Agents read this file to discover available pages.

Examples:
  lore index              # rebuild all library indexes + meta-index
  lore index services     # rebuild just the services library index + meta-index`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		vaultPath, err := config.FindVault()
		if err != nil {
			return err
		}
		cfg, err := config.Load(vaultPath)
		if err != nil {
			return err
		}

		if len(args) == 1 {
			// Rebuild a single library
			name := args[0]
			sub := findSubscription(cfg, name)
			if sub == nil {
				return fmt.Errorf("library %q not found in subscriptions", name)
			}
			if err := rebuildLibraryIndex(sub.Name, sub.ContentPath()); err != nil {
				return err
			}
		} else {
			// Rebuild all libraries
			for _, sub := range cfg.Subscriptions {
				if err := rebuildLibraryIndex(sub.Name, sub.ContentPath()); err != nil {
					fmt.Printf("  warning: %s: %v\n", sub.Name, err)
				}
			}
		}

		// Always rebuild meta-index
		if metaPath, err := index.BuildMetaIndex(cfg); err != nil {
			fmt.Printf("Warning: failed to rebuild meta-index: %v\n", err)
		} else {
			fmt.Printf("Meta-index: %s\n", metaPath)
		}

		return nil
	},
}

func rebuildLibraryIndex(name, libPath string) error {
	if err := index.BuildLibraryIndex(libPath); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	if err := buildExcerpt(libPath); err != nil {
		fmt.Printf("  warning: %s excerpt: %v\n", name, err)
	}
	fmt.Printf("Indexed: %s\n", name)
	return nil
}

func findSubscription(cfg *config.Config, name string) *config.SubscriptionConfig {
	for i := range cfg.Subscriptions {
		if cfg.Subscriptions[i].Name == name {
			return &cfg.Subscriptions[i]
		}
	}
	return nil
}

// buildExcerpt generates excerpt.md — a self-contained summary of the library
// for consumption by vault context and other agents. The library owns this
// file; vault context embeds it and injects subscriber-local paths.
//
// Excerpts are portable: they use no absolute filesystem paths, so they can
// be committed to git and consumed by any subscriber.
func buildExcerpt(libPath string) error {
	var b strings.Builder

	// Read library name and description
	name := filepath.Base(libPath)
	desc := ""
	raw, err := os.ReadFile(filepath.Join(libPath, "library.yaml"))
	if err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.HasPrefix(line, "name:") {
				val := stripYAMLQuotes(strings.TrimPrefix(line, "name:"))
				if val != "" {
					name = val
				}
			}
			if strings.HasPrefix(line, "description:") {
				desc = stripYAMLQuotes(strings.TrimPrefix(line, "description:"))
				break
			}
		}
	}

	b.WriteString(fmt.Sprintf("# %s\n\n", name))
	if desc != "" {
		b.WriteString(desc + "\n\n")
	}

	// Page count
	pageCount := 0
	wikiDir := filepath.Join(libPath, "Wiki")
	filepath.Walk(wikiDir, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if fi.IsDir() && strings.HasPrefix(fi.Name(), ".") {
			return filepath.SkipDir
		}
		if !fi.IsDir() && strings.HasSuffix(fi.Name(), ".md") && fi.Name() != "index.md" {
			pageCount++
		}
		return nil
	})
	b.WriteString(fmt.Sprintf("- **Pages:** %d\n", pageCount))

	// Skills — use relative paths (e.g. skills/deployed-versions.md)
	skills := loadSkillDefs(libPath)
	if len(skills) > 0 {
		b.WriteString("\n## Skills\n\n")
		for _, s := range skills {
			b.WriteString(fmt.Sprintf("- **%s** — %s\n", s.Name, s.Description))
			b.WriteString(fmt.Sprintf("  File: `%s`\n", s.File))

			trigger := readSkillTrigger(filepath.Join(libPath, s.File))
			if trigger != "" {
				b.WriteString(fmt.Sprintf("  Trigger: %s\n", trigger))
			}
		}
	}

	// Sources
	sources := loadSources(libPath)
	if len(sources) > 0 {
		b.WriteString("\n## Watched Repos\n\n")
		for _, src := range sources {
			b.WriteString(fmt.Sprintf("- `%s`\n", src.Repo))
		}
	}

	excerptPath := filepath.Join(libPath, "excerpt.md")
	return pathutil.AtomicWriteFile(excerptPath, []byte(b.String()), 0644)
}

// readSkillTrigger extracts the trigger field from a skill's frontmatter.
func readSkillTrigger(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	content := string(data)
	if !strings.HasPrefix(content, "---\n") {
		return ""
	}
	end := strings.Index(content[4:], "\n---")
	if end < 0 {
		return ""
	}
	block := content[4 : 4+end]
	for _, line := range strings.Split(block, "\n") {
		if strings.HasPrefix(line, "trigger:") {
			return stripYAMLQuotes(strings.TrimPrefix(line, "trigger:"))
		}
	}
	return ""
}

// rebuildIndexes rebuilds library index + meta-index. Called by seed and publish.
func rebuildIndexes(cfg *config.Config, libraryName string) {
	sub := findSubscription(cfg, libraryName)
	if sub != nil {
		if err := index.BuildLibraryIndex(sub.ContentPath()); err != nil {
			fmt.Printf("Warning: failed to rebuild library index: %v\n", err)
		}
	}
	if _, err := index.BuildMetaIndex(cfg); err != nil {
		fmt.Printf("Warning: failed to rebuild meta-index: %v\n", err)
	}
}
