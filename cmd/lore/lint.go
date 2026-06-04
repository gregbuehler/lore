package lore

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gregbuehler/lore/internal/config"
	"github.com/spf13/cobra"
)

var lintAll bool
var lintFix bool
var writeLintFile = os.WriteFile

// vaultLintCmd lints the personal vault (registered under "vault lint").
var vaultLintCmd = &cobra.Command{
	Use:   "lint",
	Short: "Check vault page health",
	Long: `Runs structural health checks on the vault.

Checks:
  - Missing frontmatter
  - Empty files

Use --fix to auto-add missing frontmatter. The type and domain are
inferred from the file's directory (e.g., Drafts/ → type: rfc,
Knowledge Base/ → type: context). Review the changes after fixing.

Examples:
  lore vault lint            # check vault pages
  lore vault lint --fix      # auto-fix missing frontmatter`,
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

		fmt.Printf("Linting vault: %s\n", cfg.Vault.Path)
		issues := lintVault(cfg.Vault.Path)

		fmt.Println()
		if issues == 0 {
			fmt.Println("No issues found.")
		} else {
			fmt.Printf("%d issue(s) found.\n", issues)
		}
		return nil
	},
}

// lintCmd lints libraries (registered under "library lint").
var lintCmd = &cobra.Command{
	Use:   "lint [library]",
	Short: "Check library page health",
	Long: `Runs library-aware health checks on a specific library or all libraries.

Checks:
  - Missing frontmatter
  - Empty files
  - Missing entity_type in frontmatter
  - Stale pages (last_updated older than library's default_ttl)
  - Stale index (Wiki/index.md page count doesn't match actual)
  - Local filesystem paths
  - Format consistency (required sections, heading names, change log format)

Use --fix to auto-fix formatting issues.
Use --all to lint all subscribed libraries.

Examples:
  lore library lint services         # one library
  lore library lint services --fix   # auto-fix format issues
  lore library lint --all            # all subscribed libraries`,
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

		issues := 0

		if len(args) == 1 {
			name := args[0]
			sub := findSubscription(cfg, name)
			if sub == nil {
				return fmt.Errorf("library %q not found in subscriptions", name)
			}
			libPath := sub.ContentPath()
			fmt.Printf("Linting library: %s (%s)\n", sub.Name, libPath)
			issues += lintLibrary(libPath, cfg.EffectiveHost())
		} else if lintAll {
			for _, sub := range cfg.Subscriptions {
				libPath := sub.ContentPath()
				fmt.Printf("Linting library: %s (%s)\n", sub.Name, libPath)
				issues += lintLibrary(libPath, cfg.EffectiveHost())
				fmt.Println()
			}
		} else {
			return fmt.Errorf("specify a library name or use --all")
		}

		fmt.Println()
		if issues == 0 {
			fmt.Println("No issues found.")
		} else {
			fmt.Printf("%d issue(s) found.\n", issues)
		}
		return nil
	},
}

// skipLintFile returns true for files that don't need frontmatter
// (logs, agent instructions, readmes, templates, library config docs).
func skipLintFile(name string) bool {
	lower := strings.ToLower(name)
	switch lower {
	case "log.md", "claude.md", "readme.md", "changelog.md", "license.md":
		return true
	}
	return false
}

// lintVault runs basic checks on the vault.
func lintVault(dir string) int {
	issues := 0
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() && strings.HasPrefix(info.Name(), ".") {
			return filepath.SkipDir
		}
		if info.IsDir() || !strings.HasSuffix(info.Name(), ".md") {
			return nil
		}
		if skipLintFile(info.Name()) {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		content := string(data)

		if strings.TrimSpace(content) == "" {
			fmt.Printf("  [empty-file] %s\n", rel)
			issues++
			return nil
		}
		if !strings.HasPrefix(content, "---") {
			if lintFix {
				fm := inferFrontmatter(rel)
				fixed := fm + content
				if err := writeLintFile(path, []byte(fixed), 0644); err != nil {
					fmt.Printf("  [missing-frontmatter] %s (fix failed: %v)\n", rel, err)
					issues++
				} else {
					fmt.Printf("  [fixed] %s\n", rel)
				}
			} else {
				fmt.Printf("  [missing-frontmatter] %s\n", rel)
				issues++
			}
		}
		return nil
	})
	return issues
}

// lintLibrary runs library-aware checks including TTL and entity_type.
func lintLibrary(libPath string, host ...string) int {
	gitHost := ""
	if len(host) > 0 {
		gitHost = host[0]
	}
	ttls := loadTTLs(libPath)
	issues := 0

	wikiDir := filepath.Join(libPath, "Wiki")
	actualPages := 0

	filepath.Walk(wikiDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() && strings.HasPrefix(info.Name(), ".") {
			return filepath.SkipDir
		}
		if info.IsDir() || !strings.HasSuffix(info.Name(), ".md") {
			return nil
		}

		rel, _ := filepath.Rel(libPath, path)

		// Don't count index.md as a content page
		if info.Name() == "index.md" {
			return nil
		}
		if skipLintFile(info.Name()) {
			return nil
		}
		actualPages++

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		content := string(data)

		if strings.TrimSpace(content) == "" {
			fmt.Printf("  [empty-file] %s\n", rel)
			issues++
			return nil
		}

		fm := extractLintFrontmatter(content)

		if !fm.hasFrontmatter {
			fmt.Printf("  [missing-frontmatter] %s\n", rel)
			issues++
			return nil
		}

		if fm.entityType == "" {
			fmt.Printf("  [missing-entity-type] %s\n", rel)
			issues++
		}

		if fm.lastUpdated != "" && fm.entityType != "" {
			if ttl, ok := ttls[fm.entityType]; ok {
				parsed, err := time.Parse("2006-01-02", fm.lastUpdated)
				if err == nil && time.Since(parsed) > ttl {
					age := int(time.Since(parsed).Hours() / 24)
					ttlDays := int(ttl.Hours() / 24)
					fmt.Printf("  [stale] %s (updated %dd ago, ttl %dd)\n", rel, age, ttlDays)
					issues++
				}
			}
		}

		// Check for local filesystem paths
		localPaths := countLocalPaths(content)
		if localPaths > 0 {
			if lintFix {
				fixed := fixLocalPaths(content, gitHost)
				if fixed != content {
					if err := writeLintFile(path, []byte(fixed), 0644); err != nil {
						fmt.Printf("  [local-path] %s (%d refs, fix failed: %v)\n", rel, localPaths, err)
						issues++
					} else {
						remaining := countLocalPaths(fixed)
						if remaining > 0 {
							fmt.Printf("  [local-path] %s (fixed %d, %d remain)\n", rel, localPaths-remaining, remaining)
							issues += remaining
						} else {
							fmt.Printf("  [fixed-local-path] %s (%d refs)\n", rel, localPaths)
						}
						// Re-read content after local-path fix for subsequent checks
						data, _ = os.ReadFile(path)
						content = string(data)
					}
				}
			} else {
				fmt.Printf("  [local-path] %s (%d refs)\n", rel, localPaths)
				issues += localPaths
			}
		}

		// Format checks
		issues += lintFormat(path, rel, content, fm.entityType)

		return nil
	})

	// Check if index is stale
	issues += lintIndexStaleness(libPath, actualPages)

	return issues
}

type lintFM struct {
	hasFrontmatter bool
	entityType     string
	lastUpdated    string
}

func extractLintFrontmatter(content string) lintFM {
	var fm lintFM
	if !strings.HasPrefix(content, "---\n") {
		return fm
	}
	end := strings.Index(content[4:], "\n---")
	if end < 0 {
		return fm
	}
	fm.hasFrontmatter = true
	block := content[4 : 4+end]

	for _, line := range strings.Split(block, "\n") {
		if strings.HasPrefix(line, "entity_type:") {
			fm.entityType = strings.TrimSpace(strings.TrimPrefix(line, "entity_type:"))
		}
		if strings.HasPrefix(line, "type:") && fm.entityType == "" {
			fm.entityType = strings.TrimSpace(strings.TrimPrefix(line, "type:"))
		}
		if strings.HasPrefix(line, "last_updated:") {
			fm.lastUpdated = strings.TrimSpace(strings.TrimPrefix(line, "last_updated:"))
		}
	}
	return fm
}

// loadTTLs reads default_ttl from library.yaml and parses durations.
func loadTTLs(libPath string) map[string]time.Duration {
	ttls := make(map[string]time.Duration)

	raw, err := os.ReadFile(filepath.Join(libPath, "library.yaml"))
	if err != nil {
		return ttls
	}

	inTTL := false
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)

		// Detect default_ttl: block
		if strings.HasPrefix(trimmed, "default_ttl:") {
			inTTL = true
			continue
		}

		// Exit on next top-level key
		if inTTL && len(line) > 0 && line[0] != ' ' && line[0] != '\t' {
			break
		}

		if inTTL && strings.Contains(trimmed, ":") {
			parts := strings.SplitN(trimmed, ":", 2)
			key := strings.TrimSpace(parts[0])
			val := strings.TrimSpace(parts[1])
			if d := parseTTL(val); d > 0 {
				ttls[key] = d
			}
		}
	}
	return ttls
}

// parseTTL converts "90d", "30d", "14d" etc. to time.Duration.
func parseTTL(s string) time.Duration {
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, "d") {
		if n, err := strconv.Atoi(strings.TrimSuffix(s, "d")); err == nil {
			return time.Duration(n) * 24 * time.Hour
		}
	}
	return 0
}

// lintIndexStaleness checks whether Wiki/index.md page count matches reality.
func lintIndexStaleness(libPath string, actualPages int) int {
	indexPath := filepath.Join(libPath, "Wiki", "index.md")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		fmt.Printf("  [missing-index] Wiki/index.md not found\n")
		return 1
	}

	content := string(data)
	// Look for "N pages total" in the auto-generated header
	idx := strings.Index(content, " pages total.")
	if idx < 0 {
		// Not an auto-generated index, skip this check
		return 0
	}

	// Walk backwards to find the number
	start := idx - 1
	for start >= 0 && content[start] >= '0' && content[start] <= '9' {
		start--
	}
	start++

	if start >= idx {
		return 0
	}

	indexCount, err := strconv.Atoi(content[start:idx])
	if err != nil {
		return 0
	}

	if indexCount != actualPages {
		fmt.Printf("  [stale-index] Wiki/index.md says %d pages, found %d (run 'lore index')\n", indexCount, actualPages)
		return 1
	}
	return 0
}

// Required sections by entity type. Pages must have these H2 headings.
var requiredSections = map[string][]string{
	"service":     {"## What It Does", "## Known Issues", "## Change Log"},
	"environment": {"## Inventory Data", "## Operational Notes", "## Incident History"},
}

// Deprecated section names that should be renamed.
var sectionRenames = map[string]string{
	"## Known Issues and Quirks": "## Known Issues",
}

// lintFormat checks page structure: required sections, heading consistency,
// change log format, and section naming.
func lintFormat(path, rel, content, entityType string) int {
	issues := 0
	needsWrite := false

	// Check for deprecated section names (fixable)
	for old, canonical := range sectionRenames {
		if strings.Contains(content, "\n"+old) || strings.HasPrefix(content, old) {
			if lintFix {
				content = strings.ReplaceAll(content, old, canonical)
				needsWrite = true
				fmt.Printf("  [fixed-section-name] %s: %q → %q\n", rel, old, canonical)
			} else {
				fmt.Printf("  [section-name] %s: use %q not %q\n", rel, canonical, old)
				issues++
			}
		}
	}

	// Check required sections
	if sections, ok := requiredSections[entityType]; ok {
		for _, req := range sections {
			if !strings.Contains(content, "\n"+req) && !strings.HasPrefix(content, req) {
				fmt.Printf("  [missing-section] %s: %s\n", rel, req)
				issues++
			}
		}
	}

	// Check change log format (service pages): should be bullet list, not table
	if entityType == "service" {
		clIdx := strings.Index(content, "\n## Change Log")
		if clIdx >= 0 {
			clContent := content[clIdx+len("\n## Change Log"):]
			if nextH2 := strings.Index(clContent, "\n## "); nextH2 >= 0 {
				clContent = clContent[:nextH2]
			}
			if strings.Contains(clContent, "| Date") || strings.Contains(clContent, "|---") {
				if lintFix {
					content = fixChangeLogFormat(content)
					needsWrite = true
					fmt.Printf("  [fixed-changelog-format] %s: table → bullet list\n", rel)
				} else {
					fmt.Printf("  [changelog-format] %s: use bullet list (- YYYY-MM-DD — prose), not table\n", rel)
					issues++
				}
			}
		}
	}

	// Check frontmatter field order
	issues += lintFrontmatterOrder(rel, content, entityType)

	if needsWrite {
		if err := writeLintFile(path, []byte(content), 0644); err != nil {
			fmt.Printf("  [fix-write] %s (fix failed: %v)\n", rel, err)
			issues++
		}
	}
	return issues
}

// fixChangeLogFormat converts a table-formatted Change Log to bullet list.
func fixChangeLogFormat(content string) string {
	marker := "\n## Change Log"
	idx := strings.Index(content, marker)
	if idx < 0 {
		return content
	}

	before := content[:idx+len(marker)]
	rest := content[idx+len(marker):]

	nextH2 := strings.Index(rest, "\n## ")
	var clSection, after string
	if nextH2 >= 0 {
		clSection = rest[:nextH2]
		after = rest[nextH2:]
	} else {
		clSection = rest
		after = ""
	}

	var bullets []string
	for _, line := range strings.Split(clSection, "\n") {
		trimmed := strings.TrimSpace(line)
		// Skip table headers and separators
		if trimmed == "" || strings.HasPrefix(trimmed, "| Date") || strings.HasPrefix(trimmed, "|---") {
			continue
		}
		// Parse table row: | date | entry |
		if strings.HasPrefix(trimmed, "|") {
			cells := strings.Split(trimmed, "|")
			if len(cells) >= 3 {
				date := strings.TrimSpace(cells[1])
				entry := strings.TrimSpace(cells[2])
				if date != "" && entry != "" {
					bullets = append(bullets, fmt.Sprintf("- %s — %s", date, entry))
				}
			}
		}
	}

	if len(bullets) == 0 {
		return content
	}

	return before + "\n\n" + strings.Join(bullets, "\n") + "\n" + after
}

// expectedFrontmatterOrder defines the canonical field order per entity type.
var expectedFrontmatterOrder = map[string][]string{
	"service":     {"entity_type", "aliases", "last_updated", "runbook", "framework"},
	"environment": {"entity_type", "aliases", "last_updated"},
	"rfc":         {"status", "type", "domain"},
}

// lintFrontmatterOrder checks that frontmatter fields appear in canonical order.
func lintFrontmatterOrder(rel, content, entityType string) int {
	expected, ok := expectedFrontmatterOrder[entityType]
	if !ok {
		return 0
	}

	if !strings.HasPrefix(content, "---\n") {
		return 0
	}
	end := strings.Index(content[4:], "\n---")
	if end < 0 {
		return 0
	}
	block := content[4 : 4+end]

	// Extract field names in order
	var fields []string
	for _, line := range strings.Split(block, "\n") {
		if len(line) > 0 && line[0] != ' ' && line[0] != '\t' && strings.Contains(line, ":") {
			field := strings.SplitN(line, ":", 2)[0]
			fields = append(fields, field)
		}
	}

	// Check order of fields that appear in both lists
	lastIdx := -1
	for _, f := range fields {
		for i, e := range expected {
			if f == e {
				if i < lastIdx {
					fmt.Printf("  [frontmatter-order] %s: field %q out of order (expected: %s)\n",
						rel, f, strings.Join(expected, ", "))
					return 1
				}
				lastIdx = i
				break
			}
		}
	}
	return 0
}

// localPathPrefixes are patterns that indicate local filesystem references
// that won't be accessible to other library consumers.
var localPathPrefixes = []string{
	"~/",
	"/Users/",
	"/home/",
	"/tmp/",
}

// countLocalPaths counts lines containing local filesystem paths.
func countLocalPaths(content string) int {
	count := 0
	for _, line := range strings.Split(content, "\n") {
		for _, prefix := range localPathPrefixes {
			if strings.Contains(line, prefix) {
				count++
				break
			}
		}
	}
	return count
}

// fixLocalPaths converts known local path patterns to accessible URLs.
// Pattern: ~/src/<host>/<org>/<repo>/... → https://<host>/<org>/<repo>/blob/main/...
// The host is read from the vault config or auto-detected.
func fixLocalPaths(content, host string) string {
	if host == "" {
		return content
	}
	var lines []string
	for _, line := range strings.Split(content, "\n") {
		line = fixHostPath(line, host)
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// fixHostPath converts ~/src/<host>/org/repo/path references to web URLs.
func fixHostPath(line, host string) string {
	prefix := "~/src/" + host + "/"
	for {
		idx := strings.Index(line, prefix)
		if idx < 0 {
			break
		}

		// Find the end of the path (whitespace, backtick, quote, paren, or EOL)
		pathStart := idx
		pathEnd := len(line)
		rest := line[idx+len(prefix):]
		for i, c := range rest {
			if c == ' ' || c == '\t' || c == '`' || c == '"' || c == '\'' || c == ')' || c == '>' || c == '|' {
				pathEnd = idx + len(prefix) + i
				break
			}
		}

		localPath := line[pathStart:pathEnd]
		// Parse: ~/src/<host>/<org>/<repo>/<rest>
		parts := strings.SplitN(localPath[len(prefix):], "/", 3)
		if len(parts) >= 3 {
			org := parts[0]
			repo := parts[1]
			filePath := parts[2]
			webURL := fmt.Sprintf("https://%s/%s/%s/blob/main/%s", host, org, repo, filePath)
			line = line[:pathStart] + webURL + line[pathEnd:]
		} else {
			// Can't parse — skip this occurrence to avoid infinite loop
			break
		}
	}
	return line
}

// inferFrontmatter generates a frontmatter block based on the file's
// directory path within the vault. Uses the vault's convention:
// status / type / domain.
func inferFrontmatter(rel string) string {
	dir := strings.ToLower(filepath.Dir(rel))
	parts := strings.Split(dir, string(filepath.Separator))
	topDir := ""
	if len(parts) > 0 {
		topDir = parts[0]
	}

	status := "draft"
	typ := "notes"
	var domains []string

	switch topDir {
	case "drafts":
		// Check filename for hints
		lowerRel := strings.ToLower(rel)
		switch {
		case strings.Contains(lowerRel, "rfc"):
			typ = "rfc"
		case strings.Contains(lowerRel, "roadmap"):
			typ = "roadmap"
		case strings.Contains(lowerRel, "strategy"):
			typ = "strategy"
		case strings.Contains(lowerRel, "spec"):
			typ = "spec"
		default:
			typ = "notes"
		}
		// Try to infer domain from subdirectory
		if len(parts) > 1 {
			domains = append(domains, strings.ReplaceAll(parts[1], " ", "-"))
		}
	case "knowledge base":
		status = "reference"
		typ = "context"
	case "current things":
		status = "active"
		typ = "context"
	case "templates":
		status = "reference"
		typ = "template"
	case "tracking":
		status = "active"
		typ = "tracking"
	case "wiki":
		// Wiki pages use entity_type, not type — but this path
		// shouldn't normally hit since wiki pages have frontmatter.
		status = "active"
		if len(parts) > 1 {
			typ = strings.TrimSuffix(parts[1], "s") // "Services" → "service"
		}
	}

	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString(fmt.Sprintf("status: %s\n", status))
	b.WriteString(fmt.Sprintf("type: %s\n", typ))
	if len(domains) > 0 {
		b.WriteString("domain:\n")
		for _, d := range domains {
			b.WriteString(fmt.Sprintf("  - %s\n", d))
		}
	}
	b.WriteString("---\n")
	return b.String()
}

func init() {
	vaultLintCmd.Flags().BoolVar(&lintFix, "fix", false, "Auto-fix missing frontmatter")
	lintCmd.Flags().BoolVar(&lintAll, "all", false, "Lint all subscribed libraries")
	lintCmd.Flags().BoolVar(&lintFix, "fix", false, "Auto-fix formatting issues")
}
