package lore

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gbuehler/lore/internal/config"
	"github.com/spf13/cobra"
)

var reviewCmd = &cobra.Command{
	Use:   "review <library>",
	Short: "Show vault learnings not yet in a library",
	Long: `Scans your daily logs for mentions of entities in the named library,
filtering to entries newer than each library page's last_updated date.

This surfaces what you've learned since the library was last updated,
so you can decide what's worth pushing upstream.

Examples:
  lore review services
  lore review my-environments`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		libraryName := args[0]

		vaultPath, err := config.FindVault()
		if err != nil {
			return err
		}
		cfg, err := config.Load(vaultPath)
		if err != nil {
			return err
		}

		sub := findSubscription(cfg, libraryName)
		if sub == nil {
			return fmt.Errorf("library %q not found in subscriptions", libraryName)
		}

		// Build entity index from library
		entities := buildEntityIndex(sub.Path)
		if len(entities) == 0 {
			fmt.Println("No entity pages found in library.")
			return nil
		}

		// Find daily log directory
		logDir := filepath.Join(cfg.Vault.Path, "Daily Log")
		if _, err := os.Stat(logDir); err != nil {
			return fmt.Errorf("no Daily Log directory found at %s", logDir)
		}

		// Scan daily logs for entity mentions
		findings := scanDailyLogs(logDir, entities)

		if len(findings) == 0 {
			fmt.Printf("No new learnings found for %s since last library update.\n", libraryName)
			return nil
		}

		// Sort entities by number of findings (most first)
		type entityFindings struct {
			name     string
			updated  string
			items    []finding
		}
		var sorted []entityFindings
		for name, items := range findings {
			sorted = append(sorted, entityFindings{
				name:    name,
				updated: entities[name].lastUpdated,
				items:   items,
			})
		}
		sort.Slice(sorted, func(i, j int) bool {
			return len(sorted[i].items) > len(sorted[j].items)
		})

		total := 0
		for _, ef := range sorted {
			total += len(ef.items)
		}
		fmt.Printf("# Review: %s\n\n", libraryName)
		fmt.Printf("%d new entries across %d entities (since each page's last_updated)\n\n", total, len(sorted))

		for _, ef := range sorted {
			fmt.Printf("## %s (updated %s, %d new)\n\n", ef.name, ef.updated, len(ef.items))
			for _, f := range ef.items {
				// Indent continuation lines for readability
				fmt.Printf("  %s: %s\n", f.date, f.summary)
			}
			fmt.Println()
		}

		return nil
	},
}

// entityInfo holds metadata about a library entity page.
type entityInfo struct {
	name        string
	aliases     []string // all searchable names (lowercase)
	entityType  string   // e.g. "service", "environment"
	lastUpdated string   // YYYY-MM-DD
	lastTime    time.Time
}

// finding is a single daily log entry mentioning an entity.
type finding struct {
	date    string
	summary string
}

// buildEntityIndex scans a library's Wiki/ directory and builds a map
// of entity name → entityInfo with aliases and last_updated.
func buildEntityIndex(libPath string) map[string]*entityInfo {
	entities := make(map[string]*entityInfo)
	wikiDir := filepath.Join(libPath, "Wiki")

	filepath.Walk(wikiDir, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if fi.IsDir() && strings.HasPrefix(fi.Name(), ".") {
			return filepath.SkipDir
		}
		if fi.IsDir() || !strings.HasSuffix(fi.Name(), ".md") {
			return nil
		}
		if fi.Name() == "index.md" {
			return nil
		}

		name := strings.TrimSuffix(fi.Name(), ".md")
		lastUpdated, entityType, aliases := readEntityMeta(path)

		t, _ := time.Parse("2006-01-02", lastUpdated)

		// Build search names: the entity name + all aliases
		searchNames := []string{strings.ToLower(name)}
		for _, a := range aliases {
			a = strings.TrimSpace(a)
			if a != "" {
				searchNames = append(searchNames, strings.ToLower(a))
			}
		}

		entities[name] = &entityInfo{
			name:        name,
			aliases:     searchNames,
			entityType:  entityType,
			lastUpdated: lastUpdated,
			lastTime:    t,
		}
		return nil
	})
	return entities
}

// readEntityMeta extracts last_updated, entity_type, and aliases from a page's frontmatter.
func readEntityMeta(path string) (lastUpdated, entityType string, aliases []string) {
	f, err := os.Open(path)
	if err != nil {
		return "", "", nil
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	if !scanner.Scan() || strings.TrimSpace(scanner.Text()) != "---" {
		return "", "", nil
	}

	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "---" {
			break
		}
		if strings.HasPrefix(line, "last_updated:") {
			lastUpdated = strings.TrimSpace(strings.TrimPrefix(line, "last_updated:"))
		}
		if strings.HasPrefix(line, "entity_type:") {
			entityType = strings.TrimSpace(strings.TrimPrefix(line, "entity_type:"))
		}
		if strings.HasPrefix(line, "aliases:") {
			val := strings.TrimSpace(strings.TrimPrefix(line, "aliases:"))
			val = strings.TrimPrefix(val, "[")
			val = strings.TrimSuffix(val, "]")
			val = strings.ReplaceAll(val, "\"", "")
			for _, a := range strings.Split(val, ",") {
				a = strings.TrimSpace(a)
				if a != "" {
					aliases = append(aliases, a)
				}
			}
		}
	}
	return lastUpdated, entityType, aliases
}

// scanDailyLogs reads all daily log files and matches entity mentions.
func scanDailyLogs(logDir string, entities map[string]*entityInfo) map[string][]finding {
	findings := make(map[string][]finding)

	// Walk all daily log .md files
	filepath.Walk(logDir, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if fi.IsDir() && strings.HasPrefix(fi.Name(), ".") {
			return filepath.SkipDir
		}
		if fi.IsDir() || !strings.HasSuffix(fi.Name(), ".md") {
			return nil
		}

		// Extract date from filename: YYYY-MM-DD.md
		date := strings.TrimSuffix(fi.Name(), ".md")
		logTime, err := time.Parse("2006-01-02", date)
		if err != nil {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		// Extract bullet points from "What Happened?" section
		bullets := extractBullets(string(data))

		for _, bullet := range bullets {
			lower := strings.ToLower(bullet)
			for name, ent := range entities {
				// Skip if this log predates the entity's last update
				if !ent.lastTime.IsZero() && !logTime.After(ent.lastTime) {
					continue
				}
				if matchesEntity(lower, ent.aliases) {
					summary := truncateBullet(bullet, 120)
					findings[name] = append(findings[name], finding{
						date:    date,
						summary: summary,
					})
				}
			}
		}
		return nil
	})

	// Sort findings by date within each entity
	for name := range findings {
		sort.Slice(findings[name], func(i, j int) bool {
			return findings[name][i].date < findings[name][j].date
		})
	}

	return findings
}

// extractBullets pulls bullet-point entries from the "What Happened?" section.
func extractBullets(content string) []string {
	var bullets []string
	inSection := false

	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "## What Happened") {
			inSection = true
			continue
		}
		if inSection && strings.HasPrefix(trimmed, "## ") {
			break
		}
		if inSection && (strings.HasPrefix(trimmed, "* ") || strings.HasPrefix(trimmed, "- ")) {
			bullets = append(bullets, trimmed)
		}
	}
	return bullets
}

// matchesEntity checks if a lowercased bullet contains any of the entity's
// search names as word-bounded matches.
func matchesEntity(lower string, aliases []string) bool {
	for _, alias := range aliases {
		// Look for the alias as a word boundary match
		idx := strings.Index(lower, alias)
		if idx < 0 {
			continue
		}
		// Check word boundaries
		before := idx == 0 || !isWordChar(lower[idx-1])
		after := idx+len(alias) >= len(lower) || !isWordChar(lower[idx+len(alias)])
		if before && after {
			return true
		}
	}
	return false
}

func isWordChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_'
}

// truncateBullet extracts the bold header if present, otherwise truncates.
func truncateBullet(bullet string, maxLen int) string {
	// Extract **bold header** if present
	bullet = strings.TrimPrefix(bullet, "* ")
	bullet = strings.TrimPrefix(bullet, "- ")

	if idx := strings.Index(bullet, "**"); idx >= 0 {
		if end := strings.Index(bullet[idx+2:], "**"); end >= 0 {
			header := bullet[idx+2 : idx+2+end]
			// Include the tag if present: `#tag`
			rest := bullet[idx+2+end+2:]
			if tagIdx := strings.Index(rest, "`#"); tagIdx >= 0 && tagIdx < 5 {
				if tagEnd := strings.Index(rest[tagIdx+2:], "`"); tagEnd >= 0 {
					tag := rest[tagIdx+1 : tagIdx+2+tagEnd]
					return header + " " + tag
				}
			}
			return header
		}
	}

	if len(bullet) > maxLen {
		return bullet[:maxLen] + "..."
	}
	return bullet
}

func init() {
	// no flags yet
}
