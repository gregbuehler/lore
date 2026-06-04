package index

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gregbuehler/lore/internal/config"
	gitpkg "github.com/gregbuehler/lore/internal/git"
	"github.com/gregbuehler/lore/internal/pathutil"
)

// BuildMetaIndex generates the meta-index.md from subscribed libraries.
func BuildMetaIndex(cfg *config.Config) (string, error) {
	var b strings.Builder
	b.WriteString("# Lore Meta-Index\n\n")
	b.WriteString(fmt.Sprintf("Generated: %s\n\n", time.Now().Format("2006-01-02 15:04")))
	b.WriteString("This file lists all subscribed libraries and their contents.\n")
	b.WriteString("The agent should read this first, then drill into relevant library indexes.\n\n")

	b.WriteString("## Vault\n\n")
	b.WriteString(fmt.Sprintf("- Path: `%s`\n", cfg.Vault.Path))
	vaultPages := countMarkdown(cfg.Vault.Path)
	b.WriteString(fmt.Sprintf("- Pages: %d\n\n", vaultPages))

	if len(cfg.Subscriptions) == 0 {
		b.WriteString("## Libraries\n\nNo subscribed libraries.\n")
	} else {
		b.WriteString("## Libraries\n\n")
		for _, sub := range cfg.Subscriptions {
			contentPath := sub.ContentPath()
			pages := countMarkdown(contentPath)
			lastUpdate := "unknown"
			if t, err := gitpkg.LastCommitTime(sub.Path); err == nil {
				lastUpdate = t
			}
			b.WriteString(fmt.Sprintf("### %s\n\n", sub.Name))
			b.WriteString(fmt.Sprintf("- Path: `%s`\n", sub.Path))
			if sub.ContentRoot() != "." {
				b.WriteString(fmt.Sprintf("- Root: `%s`\n", sub.ContentRoot()))
				b.WriteString(fmt.Sprintf("- Content path: `%s`\n", contentPath))
			}
			b.WriteString(fmt.Sprintf("- Repo: `%s`\n", sub.Repo))
			b.WriteString(fmt.Sprintf("- Access: %s\n", sub.Access))
			b.WriteString(fmt.Sprintf("- Pages: %d\n", pages))
			b.WriteString(fmt.Sprintf("- Last updated: %s\n\n", lastUpdate))
		}
	}

	// Write meta-index file
	metaPath := cfg.MetaIndex
	if metaPath == "" {
		metaPath = filepath.Join(config.LibrariesDir(), "meta-index.md")
	}
	if err := os.MkdirAll(filepath.Dir(metaPath), 0o755); err != nil {
		return "", fmt.Errorf("creating meta-index dir: %w", err)
	}
	if err := pathutil.AtomicWriteFile(metaPath, []byte(b.String()), 0o644); err != nil {
		return "", fmt.Errorf("writing meta-index: %w", err)
	}
	return metaPath, nil
}

func countMarkdown(dir string) int {
	count := 0
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		// Skip hidden directories
		if info.IsDir() && strings.HasPrefix(info.Name(), ".") {
			return filepath.SkipDir
		}
		if !info.IsDir() && strings.HasSuffix(info.Name(), ".md") {
			count++
		}
		return nil
	})
	return count
}
