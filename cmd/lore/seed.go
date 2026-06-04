package lore

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/gbuehler/lore/internal/config"
	"github.com/gbuehler/lore/internal/pathutil"
	"github.com/spf13/cobra"
)

var seedDryRun bool

var seedCmd = &cobra.Command{
	Use:   "seed <source-dir> <library-name>",
	Short: "Seed library pages from vault content",
	Long: `Copies markdown pages from a vault directory into a subscribed library,
transforming them for shared use:

  - Strips vault-specific sections (Relevant Gestation Docs)
  - Transforms Change Log entries (removes Daily Log wikilinks, keeps dates and facts)
  - Adjusts frontmatter (removes vault-internal related links and tags)
  - Preserves operational content (What It Does, SRE Quick Reference, Known Issues)

Pages are written to the library's Wiki/ directory mirroring the source
subdirectory name. Use --dry-run to preview without writing.

Examples:
  lore seed ~/Documents/lore/jane/Wiki/Services services
  lore seed ~/Documents/lore/jane/Wiki/Services services --dry-run`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		sourceDir := args[0]
		libraryName := args[1]

		abs, err := filepath.Abs(sourceDir)
		if err != nil {
			return fmt.Errorf("resolving source path: %w", err)
		}
		sourceDir = abs

		info, err := os.Stat(sourceDir)
		if err != nil || !info.IsDir() {
			return fmt.Errorf("%s does not exist or is not a directory", sourceDir)
		}

		vaultPath, err := config.FindVault()
		if err != nil {
			return err
		}
		cfg, err := config.Load(vaultPath)
		if err != nil {
			return err
		}

		var libPath string
		for _, sub := range cfg.Subscriptions {
			if sub.Name == libraryName {
				libPath = sub.ContentPath()
				break
			}
		}
		if libPath == "" {
			return fmt.Errorf("library %q not found in subscriptions", libraryName)
		}

		// Collect .md files from source
		var files []string
		filepath.Walk(sourceDir, func(path string, fi os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if fi.IsDir() && strings.HasPrefix(fi.Name(), ".") {
				return filepath.SkipDir
			}
			if !fi.IsDir() && strings.HasSuffix(fi.Name(), ".md") {
				files = append(files, path)
			}
			return nil
		})

		if len(files) == 0 {
			return fmt.Errorf("no .md files found in %s", sourceDir)
		}

		// Target: library/Wiki/<source-dir-basename>/
		sourceName := filepath.Base(sourceDir)
		targetDir := filepath.Join(libPath, "Wiki", sourceName)

		if !seedDryRun {
			if err := os.MkdirAll(targetDir, 0755); err != nil {
				return fmt.Errorf("creating target directory: %w", err)
			}
		}

		fmt.Printf("Seeding %d pages from %s → %s\n\n", len(files), sourceDir, targetDir)

		seeded := 0
		skipped := 0
		for _, f := range files {
			raw, err := os.ReadFile(f)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  skip %s: %v\n", filepath.Base(f), err)
				skipped++
				continue
			}

			transformed := transformForLibrary(string(raw))
			dest := filepath.Join(targetDir, filepath.Base(f))

			if seedDryRun {
				fmt.Printf("  [dry-run] %s\n", filepath.Base(f))
			} else {
				if err := pathutil.AtomicWriteFile(dest, []byte(transformed), 0644); err != nil {
					fmt.Fprintf(os.Stderr, "  skip %s: %v\n", filepath.Base(f), err)
					skipped++
					continue
				}
				fmt.Printf("  %s\n", filepath.Base(f))
			}
			seeded++
		}

		fmt.Printf("\nSeeded: %d", seeded)
		if skipped > 0 {
			fmt.Printf(", skipped: %d", skipped)
		}
		fmt.Println()

		if !seedDryRun {
			fmt.Println()
			rebuildIndexes(cfg, libraryName)
		}

		return nil
	},
}

// transformForLibrary strips vault-specific content and adjusts frontmatter
// for use in a shared library.
func transformForLibrary(content string) string {
	fm, body := splitFrontmatter(content)
	fm = transformFrontmatter(fm)
	body = stripSection(body, "Relevant Gestation Docs")
	body = transformChangeLog(body)
	body = strings.TrimRight(body, "\n") + "\n"
	return fm + body
}

// splitFrontmatter separates YAML frontmatter (including delimiters) from body.
func splitFrontmatter(content string) (string, string) {
	if !strings.HasPrefix(content, "---\n") {
		return "", content
	}
	end := strings.Index(content[4:], "\n---\n")
	if end < 0 {
		return "", content
	}
	fmEnd := 4 + end + 4 // includes closing ---\n
	return content[:fmEnd], content[fmEnd:]
}

// transformFrontmatter removes vault-internal fields (related, tags).
func transformFrontmatter(fm string) string {
	if fm == "" {
		return fm
	}

	var lines []string
	skip := false

	for _, line := range strings.Split(fm, "\n") {
		// Always keep --- delimiters
		if line == "---" {
			skip = false
			lines = append(lines, line)
			continue
		}

		// Top-level YAML key: starts at column 0
		if len(line) > 0 && line[0] != ' ' && line[0] != '\t' {
			skip = false
			if strings.HasPrefix(line, "related:") || strings.HasPrefix(line, "tags:") {
				skip = true
				continue
			}
		}

		if skip {
			continue
		}

		lines = append(lines, line)
	}

	return strings.Join(lines, "\n")
}

// stripSection removes an entire ## section (heading through next ## or EOF).
func stripSection(body, heading string) string {
	target := "## " + heading
	idx := -1

	if strings.HasPrefix(body, target) {
		idx = 0
	} else {
		pos := strings.Index(body, "\n"+target)
		if pos >= 0 {
			idx = pos + 1
		}
	}

	if idx < 0 {
		return body
	}

	afterHeading := body[idx+len(target):]
	nextSection := strings.Index(afterHeading, "\n## ")

	if nextSection < 0 {
		// Last section — trim trailing newlines
		return strings.TrimRight(body[:idx], "\n") + "\n"
	}

	return body[:idx] + afterHeading[nextSection+1:]
}

var dailyLogLink = regexp.MustCompile(`\[\[Daily Log/\d{4}-\d{2}/(\d{4}-\d{2}-\d{2})\]\]`)

// transformChangeLog either strips an empty/comments-only Change Log section,
// or transforms Daily Log wikilinks into plain dates.
func transformChangeLog(body string) string {
	hasChangeLog := strings.Contains(body, "\n## Change Log") || strings.HasPrefix(body, "## Change Log")
	if !hasChangeLog {
		return body
	}

	// Locate the section to inspect its content
	marker := "\n## Change Log"
	idx := strings.Index(body, marker)
	sectionStart := 0
	if idx >= 0 {
		sectionStart = idx + len(marker)
	} else {
		sectionStart = len("## Change Log")
	}

	rest := body[sectionStart:]
	nextIdx := strings.Index(rest, "\n## ")

	var sectionContent string
	if nextIdx < 0 {
		sectionContent = rest
	} else {
		sectionContent = rest[:nextIdx]
	}

	trimmed := strings.TrimSpace(sectionContent)

	// Empty or HTML-comment-only section → strip entirely
	if trimmed == "" || (strings.HasPrefix(trimmed, "<!--") && strings.HasSuffix(trimmed, "-->")) {
		return stripSection(body, "Change Log")
	}

	// Real entries — transform [[Daily Log/YYYY-MM/YYYY-MM-DD]] → YYYY-MM-DD
	return dailyLogLink.ReplaceAllString(body, "$1")
}

func init() {
	seedCmd.Flags().BoolVar(&seedDryRun, "dry-run", false, "Preview without writing files")
}
