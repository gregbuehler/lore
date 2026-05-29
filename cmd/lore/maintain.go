package lore

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gbuehler/lore/internal/agent"
	"github.com/gbuehler/lore/internal/config"
	"github.com/gbuehler/lore/internal/index"
	"github.com/spf13/cobra"
)

var maintainEntity string
var maintainDryRun bool
var agentDangerouslySkipPermissions bool
var agentProviderOverride string

var maintainCmd = &cobra.Command{
	Use:   "maintain <library>",
	Short: "Synthesize new evidence into library pages",
	Long: `Scans your daily logs for new evidence about entities in the named
library, assembles context packages, and invokes the configured agent
to rewrite each library page to reflect the current understanding.

The agent receives:
  - The current library page
  - New daily log entries mentioning that entity
  - The library's tone rules
  - Instructions to synthesize (not append)

Use --entity to maintain a single entity. Use --dry-run to generate
context packages without invoking the agent.

Examples:
  lore maintain services
  lore maintain services --entity storage
  lore maintain services --dry-run`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		libraryName := args[0]

		vaultPath, err := config.FindVault()
		if err != nil {
			return err
		}
		cfg, err := config.LoadWithLocal(vaultPath)
		if err != nil {
			return err
		}
		agentCfg := agentConfigWithProvider(cfg.Agent, agentProviderOverride)

		sub := findSubscription(cfg, libraryName)
		if sub == nil {
			return fmt.Errorf("library %q not found in subscriptions", libraryName)
		}

		// Build entity index and scan for new evidence
		entities := buildEntityIndex(sub.Path)
		if len(entities) == 0 {
			fmt.Println("No entity pages found in library.")
			return nil
		}

		logDir := filepath.Join(cfg.Vault.Path, "Daily Log")
		if _, err := os.Stat(logDir); err != nil {
			return fmt.Errorf("no Daily Log directory found at %s", logDir)
		}

		// Get full bullet text (not truncated) for evidence
		fullFindings := scanDailyLogsFull(logDir, entities)

		if len(fullFindings) == 0 {
			fmt.Printf("No new evidence for %s since last library update.\n", libraryName)
			return nil
		}

		// Filter to single entity if specified
		if maintainEntity != "" {
			if items, ok := fullFindings[maintainEntity]; ok {
				filtered := map[string][]evidenceEntry{maintainEntity: items}
				fullFindings = filtered
			} else {
				return fmt.Errorf("no new evidence for entity %q", maintainEntity)
			}
		}

		// Load tone rules
		toneRules := loadToneRules(sub.Path)

		agentLabel := agent.Label(agentCfg)

		// Sort entities by evidence count
		type entityWork struct {
			name  string
			items []evidenceEntry
		}
		var work []entityWork
		for name, items := range fullFindings {
			work = append(work, entityWork{name: name, items: items})
		}
		sort.Slice(work, func(i, j int) bool {
			return len(work[i].items) > len(work[j].items)
		})

		fmt.Printf("Maintaining %s: %d entities with new evidence\n\n", libraryName, len(work))

		incomingDir := filepath.Join(sub.Path, "sources", "incoming")
		os.MkdirAll(incomingDir, 0755)

		maintained := 0
		for _, ew := range work {
			ent := entities[ew.name]
			pagePath := findEntityPage(sub.Path, ew.name)
			if pagePath == "" {
				fmt.Printf("  skip %s: page not found\n", ew.name)
				continue
			}

			currentPage, err := os.ReadFile(pagePath)
			if err != nil {
				fmt.Printf("  skip %s: %v\n", ew.name, err)
				continue
			}

			// Assemble context package
			pkg := buildContextPackage(ew.name, ent.entityType, string(currentPage), ew.items, toneRules, ent.lastUpdated)

			pkgPath := filepath.Join(incomingDir, fmt.Sprintf("maintain-%s-%s.md", ew.name, time.Now().Format("2006-01-02")))
			if err := os.WriteFile(pkgPath, []byte(pkg), 0644); err != nil {
				fmt.Printf("  skip %s: writing package: %v\n", ew.name, err)
				continue
			}

			if maintainDryRun {
				fmt.Printf("  [dry-run] %s (%d entries) → %s\n", ew.name, len(ew.items), filepath.Base(pkgPath))
				maintained++
				continue
			}

			fmt.Printf("  %s (%d entries) — invoking %s...\n", ew.name, len(ew.items), agentLabel)

			// Build the agent prompt
			prompt := fmt.Sprintf(
				"Read the context package at %s. It contains the current library page, new evidence from daily logs, and tone rules. "+
					"Rewrite the library page at %s to reflect the updated understanding. "+
					"Synthesize the new evidence into the existing page — update facts, add new known issues, update the change log with dated entries. "+
					"Do not append raw notes. Do not remove existing content unless it is contradicted by newer evidence. "+
					"Follow the tone rules. Update the last_updated field in frontmatter to today's date (%s). "+
					"Write the updated page directly to %s.",
				pkgPath, pagePath, time.Now().Format("2006-01-02"), pagePath,
			)

			if err := runAgent(agentCfg, sub.Path, prompt); err != nil {
				fmt.Printf("    error: %v\n", err)
				continue
			}

			fmt.Printf("    done\n")
			maintained++

			// Clean up the context package after successful processing
			os.Remove(pkgPath)
		}

		fmt.Printf("\nMaintained: %d/%d entities\n", maintained, len(work))

		if !maintainDryRun && maintained > 0 {
			// Rebuild index
			if err := index.BuildLibraryIndex(sub.Path); err != nil {
				fmt.Printf("Warning: failed to rebuild library index: %v\n", err)
			}
			if _, err := index.BuildMetaIndex(cfg); err != nil {
				fmt.Printf("Warning: failed to rebuild meta-index: %v\n", err)
			}
		}

		return nil
	},
}

// evidenceEntry is a full daily log bullet with date.
type evidenceEntry struct {
	date string
	text string // full bullet text
}

// scanDailyLogsFull is like scanDailyLogs but returns full bullet text.
func scanDailyLogsFull(logDir string, entities map[string]*entityInfo) map[string][]evidenceEntry {
	findings := make(map[string][]evidenceEntry)

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

		date := strings.TrimSuffix(fi.Name(), ".md")
		logTime, err := time.Parse("2006-01-02", date)
		if err != nil {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		bullets := extractBullets(string(data))

		for _, bullet := range bullets {
			lower := strings.ToLower(bullet)
			for name, ent := range entities {
				if !ent.lastTime.IsZero() && !logTime.After(ent.lastTime) {
					continue
				}
				if matchesEntity(lower, ent.aliases) {
					findings[name] = append(findings[name], evidenceEntry{
						date: date,
						text: bullet,
					})
				}
			}
		}
		return nil
	})

	for name := range findings {
		sort.Slice(findings[name], func(i, j int) bool {
			return findings[name][i].date < findings[name][j].date
		})
	}

	return findings
}

// findEntityPage locates the .md file for an entity in the library's Wiki/.
func findEntityPage(libPath, entityName string) string {
	wikiDir := filepath.Join(libPath, "Wiki")
	var found string
	filepath.Walk(wikiDir, func(path string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return nil
		}
		if strings.TrimSuffix(fi.Name(), ".md") == entityName {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

// buildContextPackage assembles the markdown context for the agent.
func buildContextPackage(entityName, entityType, currentPage string, evidence []evidenceEntry, toneRules, lastUpdated string) string {
	var b strings.Builder

	b.WriteString("# Maintenance Context Package\n\n")
	b.WriteString(fmt.Sprintf("Entity: **%s**\n", entityName))
	b.WriteString(fmt.Sprintf("Library page last updated: %s\n", lastUpdated))
	b.WriteString(fmt.Sprintf("New evidence entries: %d\n\n", len(evidence)))

	b.WriteString("---\n\n")
	b.WriteString("## Current Library Page\n\n")
	b.WriteString("```markdown\n")
	b.WriteString(currentPage)
	if !strings.HasSuffix(currentPage, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("```\n\n")

	b.WriteString("---\n\n")
	b.WriteString("## New Evidence (from daily logs)\n\n")
	b.WriteString("Each entry below is a daily log observation. Synthesize these into the page — don't paste them verbatim.\n\n")
	for _, e := range evidence {
		b.WriteString(fmt.Sprintf("### %s\n\n", e.date))
		b.WriteString(e.text)
		b.WriteString("\n\n")
	}

	b.WriteString("---\n\n")
	b.WriteString("## Tone Rules\n\n")
	if toneRules != "" {
		b.WriteString(toneRules)
	} else {
		b.WriteString("No tone rules configured for this library.\n")
	}
	b.WriteString("\n\n")

	b.WriteString("---\n\n")
	b.WriteString("## Instructions\n\n")
	b.WriteString("Rewrite the library page to reflect the updated understanding:\n\n")
	b.WriteString("1. Update facts that are contradicted or refined by the new evidence.\n")
	b.WriteString("2. Add new known issues, quirks, or operational details discovered in the evidence.\n")
	b.WriteString("3. Append dated entries to the Change Log section for significant events.\n")
	b.WriteString("4. Do NOT remove existing content unless it is explicitly contradicted.\n")
	b.WriteString("5. Do NOT paste raw daily log text — synthesize it into the page's voice.\n")
	b.WriteString("6. Update the `last_updated` field in frontmatter to today's date.\n")
	b.WriteString("7. Follow the tone rules above.\n")
	b.WriteString("8. Do NOT introduce local filesystem paths (~/..., /Users/..., /home/...). Use web URLs instead.\n")
	b.WriteString("\n")
	b.WriteString("## Format Rules\n\n")
	b.WriteString("Follow these formatting conventions exactly:\n\n")
	b.WriteString("- **Frontmatter field order** (service): entity_type, aliases, last_updated, runbook, framework\n")
	b.WriteString("- **Frontmatter field order** (environment): entity_type, aliases, last_updated\n")
	b.WriteString("- **Change Log format**: bullet list, each entry `- YYYY-MM-DD — prose`. Do NOT use tables.\n")
	b.WriteString("- **Section heading**: use `## Known Issues` (not `## Known Issues and Quirks`)\n")

	// Add required sections for the entity type
	if sections, ok := requiredSections[entityType]; ok {
		b.WriteString(fmt.Sprintf("- **Required sections** for %s pages: %s\n", entityType, strings.Join(sections, ", ")))
	}

	return b.String()
}

// loadToneRules extracts the tone section from library.yaml as readable text.
func loadToneRules(libPath string) string {
	raw, err := os.ReadFile(filepath.Join(libPath, "library.yaml"))
	if err != nil {
		return ""
	}

	var b strings.Builder
	inTone := false
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)

		if trimmed == "tone:" {
			inTone = true
			continue
		}
		if inTone && len(line) > 0 && line[0] != ' ' && line[0] != '\t' {
			break
		}
		if inTone {
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	return b.String()
}

// runAgent invokes the configured agent command with a prompt.
func runAgent(agentCfg config.AgentConfig, workDir, prompt string) error {
	return agent.Run(agentCfg, workDir, prompt, agent.Options{
		DangerouslySkipPermissions: agentDangerouslySkipPermissions,
	}, os.Stdout, os.Stderr)
}

func agentConfigWithProvider(agentCfg config.AgentConfig, provider string) config.AgentConfig {
	if strings.TrimSpace(provider) != "" {
		agentCfg.Provider = strings.ToLower(strings.TrimSpace(provider))
		agentCfg.Command = ""
	}
	return agentCfg
}

func init() {
	maintainCmd.Flags().StringVar(&maintainEntity, "entity", "", "Maintain a single entity (e.g., --entity storage)")
	maintainCmd.Flags().BoolVar(&maintainDryRun, "dry-run", false, "Generate context packages without invoking the agent")
	maintainCmd.Flags().StringVar(&agentProviderOverride, "agent", "", "Agent provider for this run: claude, codex, custom, or none")
	maintainCmd.Flags().BoolVar(&agentDangerouslySkipPermissions, "dangerously-skip-permissions", false, "Pass --dangerously-skip-permissions to the configured agent")
}
