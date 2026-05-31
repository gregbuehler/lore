package lore

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/gregbuehler/lore/internal/config"
	"github.com/gregbuehler/lore/internal/pathutil"
	"github.com/gregbuehler/lore/internal/resolve"
	"github.com/spf13/cobra"
)

var fixLinksDryRun bool

// wikilinkRe matches [[target]] and [[target|display]] wikilinks.
var wikilinkRe = regexp.MustCompile(`\[\[([^\]]+)\]\]`)

var fixLinksCmd = &cobra.Command{
	Use:   "fix-links",
	Short: "Rewrite stale wikilinks in markdown files",
	Long: `Walks all markdown files across the vault and subscribed libraries,
resolves broken wikilinks using the same strategies as the daemon, and
rewrites them in-place.

Resolution strategies (in order):
  1. Exact match — link already resolves, no change needed
  2. Folder expansion — Threads/Foo → Threads/Foo/Foo
  3. Short-name lookup — bare names → closest matching file
  4. Relative path resolution — try prefixing with ancestor directories
  5. Basename fallback — when a parent folder was renamed

Display text (after |) and anchors (after #) are preserved.

Examples:
  lore fix-links            # rewrite broken links in-place
  lore fix-links --dry-run  # preview what would change`,
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

		// Collect all root paths: vault + subscribed libraries.
		roots := []string{cfg.Vault.Path}
		for _, sub := range cfg.Subscriptions {
			if sub.Path != "" {
				roots = append(roots, sub.Path)
			}
		}

		// Build resolver from all roots.
		resolver := resolve.New()
		for _, root := range roots {
			resolver.Scan(root)
		}

		if fixLinksDryRun {
			fmt.Println("[dry-run] No files will be modified.")
		}

		totalFixed := 0
		totalFiles := 0

		for _, root := range roots {
			n, err := fixLinksInRoot(root, resolver, fixLinksDryRun)
			if err != nil {
				return fmt.Errorf("fix-links: %w", err)
			}
			totalFixed += n
			if n > 0 {
				totalFiles++
			}
		}

		fmt.Println()
		if totalFixed == 0 {
			fmt.Println("No broken links found.")
		} else if fixLinksDryRun {
			fmt.Printf("%d broken link(s) found across %d file(s) — dry run, no changes written.\n", totalFixed, totalFiles)
		} else {
			fmt.Printf("Fixed %d broken link(s) across %d file(s).\n", totalFixed, totalFiles)
		}
		return nil
	},
}

// fixLinksInRoot walks a root directory and rewrites broken wikilinks.
// Returns the number of links fixed (or that would be fixed in dry-run mode).
func fixLinksInRoot(root string, resolver *resolve.Resolver, dryRun bool) (int, error) {
	fixed := 0

	err := filepath.Walk(root, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if fi.IsDir() {
			if strings.HasPrefix(fi.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(fi.Name(), ".md") {
			return nil
		}
		if resolve.SkipIndexFile(fi.Name()) {
			return nil
		}

		rel, _ := filepath.Rel(root, path)
		n, err := fixLinksInFile(path, rel, resolver, dryRun)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  [error] %s: %v\n", rel, err)
			return nil // non-fatal
		}
		fixed += n
		return nil
	})

	return fixed, err
}

// fixLinksInFile rewrites broken wikilinks in a single markdown file.
// rel is the file's path relative to its root (without .md suffix) for
// source-context resolution.
func fixLinksInFile(path, rel string, resolver *resolve.Resolver, dryRun bool) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	original := string(data)

	// relKey is the source context passed to the resolver (no .md suffix).
	relKey := strings.TrimSuffix(rel, ".md")

	fixed := 0
	result := wikilinkRe.ReplaceAllStringFunc(original, func(match string) string {
		// match is the full [[...]] token
		inner := match[2 : len(match)-2] // strip [[ and ]]

		// Split on | to separate target from display text
		target, display, hasDisplay := strings.Cut(inner, "|")

		// Strip anchor from target (preserve for output)
		anchor := ""
		if idx := strings.Index(target, "#"); idx >= 0 {
			anchor = target[idx:] // includes the #
			target = target[:idx]
		}

		// Normalize target
		target = strings.TrimSpace(target)
		target = strings.TrimSuffix(target, ".md")
		target = strings.TrimRight(target, "\\/")

		// Skip templates (contain < or YYYY placeholder)
		if strings.Contains(target, "<") || strings.Contains(target, "YYYY") {
			return match
		}

		// Skip emoji shortcodes like :foo:
		if isEmojiShortcode(target) {
			return match
		}

		// Skip empty targets
		if target == "" {
			return match
		}

		// Try to resolve a broken link
		resolved := resolver.Resolve(target, relKey)
		if resolved == "" {
			// Either target already exists or can't be resolved — no change
			return match
		}

		// Build the rewritten wikilink
		fixed++
		newInner := resolved + anchor
		if hasDisplay {
			newInner = resolved + anchor + "|" + display
		}
		return "[[" + newInner + "]]"
	})

	if fixed == 0 {
		return 0, nil
	}

	// Report what changed
	// Print each changed link (brief)
	// Re-run the regex to collect per-link details for reporting
	reportLinkFixes(rel, original, result)

	if !dryRun {
		if err := pathutil.AtomicWriteFile(path, []byte(result), fi_mode(path)); err != nil {
			return 0, fmt.Errorf("writing %s: %w", path, err)
		}
	}

	return fixed, nil
}

// reportLinkFixes prints a diff of old vs new wikilinks for the given file.
func reportLinkFixes(rel, original, result string) {
	fmt.Printf("  %s\n", rel)

	// Find all changed wikilinks by comparing original and result
	origLinks := wikilinkRe.FindAllString(original, -1)
	resultLinks := wikilinkRe.FindAllString(result, -1)

	for i, orig := range origLinks {
		if i < len(resultLinks) && orig != resultLinks[i] {
			fmt.Printf("    %s  →  %s\n", orig, resultLinks[i])
		}
	}
}

// isEmojiShortcode returns true if s looks like a bare emoji shortcode, e.g. ":foo:".
func isEmojiShortcode(s string) bool {
	return strings.HasPrefix(s, ":") && strings.HasSuffix(s, ":") && !strings.Contains(s, " ")
}

// fi_mode returns the existing file mode (permissions) for a path.
func fi_mode(path string) os.FileMode {
	if info, err := os.Stat(path); err == nil {
		return info.Mode()
	}
	return 0o644
}

func init() {
	fixLinksCmd.Flags().BoolVar(&fixLinksDryRun, "dry-run", false, "Preview changes without writing files")
}
