package lore

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gbuehler/lore/internal/config"
	"github.com/gbuehler/lore/internal/index"
	"github.com/spf13/cobra"
)

var watchEntity string
var watchDryRun bool

var watchCmd = &cobra.Command{
	Use:   "watch <library>",
	Short: "Update library pages from source repo changes",
	Long: `Scans configured source repositories for changes since each entity's
last_updated date, assembles context packages, and invokes the agent
to update library pages.

Sources are declared in library.yaml:

  sources:
    - repo: git.example.com/myorg/deployment
      local: ~/src/git.example.com/myorg/deployment
      watch:
        - path: "deployments/{entity}/**"
          maps_to: environment
    - repo: git.example.com/myorg/infra
      local: ~/src/git.example.com/myorg/infra
      watch:
        - path: "deployments/{prefix}-{entity}/**"
          maps_to: environment

The {entity} placeholder is resolved from the entity's Inventory Data
(Deploy Dir, CTF Dir fields) or falls back to the entity page name.

Use --entity to watch a single entity. Use --dry-run to generate
context packages without invoking the agent.

Examples:
  lore watch my-environments
  lore watch my-environments --entity staging
  lore watch my-environments --dry-run`,
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

		// Load sources from library.yaml
		sources := loadSources(sub.Path)
		if len(sources) == 0 {
			return fmt.Errorf("no sources configured in %s/library.yaml", sub.Path)
		}

		// Build entity index
		entities := buildEntityIndex(sub.Path)
		if len(entities) == 0 {
			fmt.Println("No entity pages found in library.")
			return nil
		}

		// Filter to single entity if specified
		if watchEntity != "" {
			if ent, ok := entities[watchEntity]; ok {
				entities = map[string]*entityInfo{watchEntity: ent}
			} else {
				return fmt.Errorf("entity %q not found in library", watchEntity)
			}
		}

		// Gather repo changes per entity
		apiToken := os.Getenv("GHE_TOKEN")
		if apiToken == "" {
			apiToken = os.Getenv("GITHUB_TOKEN")
		}

		allChanges := make(map[string][]repoChange)
		for _, src := range sources {
			localPath := resolveSourcePath(src.Local, src.Repo)
			useAPI := localPath == ""

			if useAPI && apiToken == "" {
				fmt.Printf("  skip source %s: no local checkout and no GHE_TOKEN/GITHUB_TOKEN set\n", src.Repo)
				continue
			}
			if useAPI {
				fmt.Printf("  source %s: using GitHub API\n", src.Repo)
			}

			for name, ent := range entities {
				// Read inventory data for directory mapping
				pagePath := findEntityPage(sub.Path, name)
				if pagePath == "" {
					continue
				}
				inv := readInventoryData(pagePath)

				for _, w := range src.Watch {
					dirName := resolveEntityDir(w.Path, name, inv)
					if dirName == "" {
						continue
					}

					since := ent.lastUpdated
					if since == "" {
						since = "2026-01-01"
					}

					var changes []repoChange
					if useAPI {
						changes = apiLogChanges(src.Repo, dirName, since, apiToken)
					} else {
						changes = gitLogChanges(localPath, dirName, since)
					}
					if len(changes) > 0 {
						allChanges[name] = append(allChanges[name], changes...)
					}
				}
			}
		}

		if len(allChanges) == 0 {
			fmt.Printf("No source repo changes found for %s since last updates.\n", libraryName)
			return nil
		}

		// Sort entities by change count
		type entityWork struct {
			name    string
			changes []repoChange
		}
		var work []entityWork
		for name, changes := range allChanges {
			work = append(work, entityWork{name: name, changes: changes})
		}
		sort.Slice(work, func(i, j int) bool {
			return len(work[i].changes) > len(work[j].changes)
		})

		toneRules := loadToneRules(sub.Path)
		agentCmd := cfg.Agent.Command
		if agentCmd == "" {
			agentCmd = "claude"
		}

		fmt.Printf("Watching %s: %d entities with source changes\n\n", libraryName, len(work))

		incomingDir := filepath.Join(sub.Path, "sources", "incoming")
		os.MkdirAll(incomingDir, 0755)

		updated := 0
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

			pkg := buildWatchContextPackage(ew.name, ent.entityType, string(currentPage), ew.changes, toneRules, ent.lastUpdated)

			pkgPath := filepath.Join(incomingDir, fmt.Sprintf("watch-%s-%s.md", ew.name, time.Now().Format("2006-01-02")))
			if err := os.WriteFile(pkgPath, []byte(pkg), 0644); err != nil {
				fmt.Printf("  skip %s: writing package: %v\n", ew.name, err)
				continue
			}

			if watchDryRun {
				fmt.Printf("  [dry-run] %s (%d commits) → %s\n", ew.name, len(ew.changes), filepath.Base(pkgPath))
				updated++
				continue
			}

			fmt.Printf("  %s (%d commits) — invoking %s...\n", ew.name, len(ew.changes), agentCmd)

			prompt := fmt.Sprintf(
				"Read the context package at %s. It contains the current library page, recent source repository changes, and tone rules. "+
					"Update the library page at %s to reflect changes from the infrastructure-as-code repositories. "+
					"Update inventory data if values changed. Add operational notes for significant changes. "+
					"Add dated entries to the Incident History or Operational Notes for noteworthy events. "+
					"Do not remove existing content unless it is contradicted by the repo changes. "+
					"Follow the tone rules. Update the last_updated field in frontmatter to today's date (%s). "+
					"Write the updated page directly to %s.",
				pkgPath, pagePath, time.Now().Format("2006-01-02"), pagePath,
			)

			if err := runAgent(agentCmd, sub.Path, prompt); err != nil {
				fmt.Printf("    error: %v\n", err)
				continue
			}

			fmt.Printf("    done\n")
			updated++
			os.Remove(pkgPath)
		}

		fmt.Printf("\nUpdated: %d/%d entities\n", updated, len(work))

		if !watchDryRun && updated > 0 {
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

// sourceConfig represents a source repo declaration from library.yaml.
type sourceConfig struct {
	Repo  string        // e.g. "git.example.com/myorg/deployment"
	Local string        // e.g. "~/src/git.example.com/myorg/deployment"
	Watch []watchConfig // what to watch in this repo
}

// watchConfig describes a path pattern to watch within a source repo.
type watchConfig struct {
	Path   string // e.g. "deployments/{entity}/**" or "deployments/{prefix}-{entity}/**"
	MapsTo string // entity type this maps to
}

// repoChange represents a single commit affecting an entity's source files.
type repoChange struct {
	Repo    string
	Hash    string
	Date    string
	Author  string
	Subject string
	Files   []string // changed file paths
}

// stripYAMLQuotes removes surrounding quotes from a YAML value.
func stripYAMLQuotes(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// loadSources parses the sources section from library.yaml.
func loadSources(libPath string) []sourceConfig {
	raw, err := os.ReadFile(filepath.Join(libPath, "library.yaml"))
	if err != nil {
		return nil
	}

	var sources []sourceConfig
	var current *sourceConfig
	var currentWatch *watchConfig
	inSources := false
	inWatch := false

	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)

		// Detect sources: block
		if trimmed == "sources:" {
			inSources = true
			inWatch = false
			continue
		}

		// Exit on next top-level key
		if inSources && len(line) > 0 && line[0] != ' ' && line[0] != '\t' && !strings.HasPrefix(trimmed, "#") {
			break
		}

		if !inSources {
			continue
		}

		// New source entry: "  - repo: ..."
		if strings.HasPrefix(trimmed, "- repo:") {
			if current != nil {
				if currentWatch != nil {
					current.Watch = append(current.Watch, *currentWatch)
					currentWatch = nil
				}
				sources = append(sources, *current)
			}
			current = &sourceConfig{
				Repo: stripYAMLQuotes(strings.TrimPrefix(trimmed, "- repo:")),
			}
			inWatch = false
			continue
		}

		if current == nil {
			continue
		}

		if strings.HasPrefix(trimmed, "local:") {
			current.Local = stripYAMLQuotes(strings.TrimPrefix(trimmed, "local:"))
			continue
		}

		if trimmed == "watch:" {
			inWatch = true
			continue
		}

		if inWatch && strings.HasPrefix(trimmed, "- path:") {
			if currentWatch != nil {
				current.Watch = append(current.Watch, *currentWatch)
			}
			currentWatch = &watchConfig{
				Path: stripYAMLQuotes(strings.TrimPrefix(trimmed, "- path:")),
			}
			continue
		}

		if inWatch && currentWatch != nil && strings.HasPrefix(trimmed, "maps_to:") {
			currentWatch.MapsTo = stripYAMLQuotes(strings.TrimPrefix(trimmed, "maps_to:"))
			continue
		}
	}

	// Flush last entries
	if current != nil {
		if currentWatch != nil {
			current.Watch = append(current.Watch, *currentWatch)
		}
		sources = append(sources, *current)
	}

	return sources
}

// resolveSourcePath finds the local checkout of a source repo.
// Tries: explicit local path, ~/src/<repo>, $GITHUB_WORKSPACE (CI).
func resolveSourcePath(local, repo string) string {
	// Expand ~ in explicit local path
	if local != "" {
		expanded := expandHome(local)
		if fi, err := os.Stat(expanded); err == nil && fi.IsDir() {
			return expanded
		}
	}

	// Convention: ~/src/<repo>
	home, _ := os.UserHomeDir()
	if home != "" {
		conventional := filepath.Join(home, "src", repo)
		if fi, err := os.Stat(conventional); err == nil && fi.IsDir() {
			return conventional
		}
	}

	// CI: $GITHUB_WORKSPACE/<repo-name>
	if ws := os.Getenv("GITHUB_WORKSPACE"); ws != "" {
		parts := strings.Split(repo, "/")
		repoName := parts[len(parts)-1]
		ciPath := filepath.Join(ws, repoName)
		if fi, err := os.Stat(ciPath); err == nil && fi.IsDir() {
			return ciPath
		}
	}

	return ""
}

// expandHome replaces a leading ~ with the user's home directory.
func expandHome(path string) string {
	if !strings.HasPrefix(path, "~/") {
		return path
	}
	home, _ := os.UserHomeDir()
	if home == "" {
		return path
	}
	return filepath.Join(home, path[2:])
}

// resolveEntityDir extracts the directory path to watch for an entity
// from the watch pattern and the entity's inventory data.
func resolveEntityDir(pattern, entityName string, inv inventoryData) string {
	// Pattern like "deployments/{entity}/**" or "deployments/myprefix-{entity}/**"
	// Strip trailing /** or /*
	dir := pattern
	dir = strings.TrimSuffix(dir, "/**")
	dir = strings.TrimSuffix(dir, "/*")

	if strings.Contains(dir, "{entity}") {
		dirName := entityName

		// Check if there's a prefix before {entity} in the same path segment
		// e.g., "deployments/prefix-{entity}" → use CTF Dir or prepend prefix
		entityIdx := strings.Index(dir, "{entity}")
		segStart := strings.LastIndex(dir[:entityIdx], "/") + 1
		prefix := dir[segStart:entityIdx]

		if prefix != "" {
			// Prefixed pattern: use CTF Dir if available, else prefix + entity name
			if inv.CTFDir != "" {
				dirName = inv.CTFDir
			} else {
				dirName = prefix + entityName
			}
			dir = dir[:segStart] + dirName + dir[entityIdx+len("{entity}"):]
		} else {
			// Simple {entity} pattern: use Deploy Dir if available
			if inv.DeployDir != "" {
				dirName = inv.DeployDir
			}
			dir = strings.ReplaceAll(dir, "{entity}", dirName)
		}
	}

	return dir
}

// inventoryData holds mapped directory names from a page's Inventory Data table.
type inventoryData struct {
	CTFDir    string
	DeployDir string
}

// readInventoryData extracts CTF Dir and Deploy Dir from a page's Inventory Data table.
func readInventoryData(pagePath string) inventoryData {
	data, err := os.ReadFile(pagePath)
	if err != nil {
		return inventoryData{}
	}

	var inv inventoryData
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "|") {
			continue
		}
		cells := strings.Split(trimmed, "|")
		if len(cells) < 3 {
			continue
		}
		key := strings.TrimSpace(cells[1])
		val := strings.TrimSpace(cells[2])
		// Treat placeholder values as empty
		if val == "—" || val == "-" || val == "N/A" || val == "n/a" {
			val = ""
		}
		switch key {
		case "CTF Dir":
			inv.CTFDir = val
		case "Deploy Dir":
			inv.DeployDir = val
		}
	}
	return inv
}

// gitLogChanges runs git log on a source repo to find changes under a given
// directory since a given date.
func gitLogChanges(repoPath, dir, since string) []repoChange {
	// git log --since=<date> --format="%H|%ad|%an|%s" --date=short -- <dir>
	out, err := exec.Command("git", "-C", repoPath,
		"log",
		"--since="+since,
		"--format=%H|%ad|%an|%s",
		"--date=short",
		"--name-only",
		"--", dir,
	).Output()
	if err != nil {
		return nil
	}

	var changes []repoChange
	var current *repoChange

	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Commit header line: hash|date|author|subject
		parts := strings.SplitN(line, "|", 4)
		if len(parts) == 4 && len(parts[0]) == 40 {
			if current != nil {
				changes = append(changes, *current)
			}
			current = &repoChange{
				Repo:    repoPath,
				Hash:    parts[0],
				Date:    parts[1],
				Author:  parts[2],
				Subject: parts[3],
			}
		} else if current != nil {
			// File path line
			current.Files = append(current.Files, line)
		}
	}
	if current != nil {
		changes = append(changes, *current)
	}

	return changes
}

// parseRepoID splits a repo identifier like "git.example.com/org/repo"
// into (host, owner, repo).
func parseRepoID(repoID string) (host, owner, repo string) {
	parts := strings.SplitN(repoID, "/", 3)
	if len(parts) != 3 {
		return "", "", ""
	}
	return parts[0], parts[1], parts[2]
}

// apiLogChanges queries the GitHub Commits API for changes under a directory.
// Works with both github.com and GitHub Enterprise instances.
func apiLogChanges(repoID, dir, since, token string) []repoChange {
	host, owner, repo := parseRepoID(repoID)
	if host == "" {
		return nil
	}

	// Build API base URL: GHE uses /api/v3, github.com uses api.github.com
	var apiBase string
	if host == "github.com" {
		apiBase = "https://api.github.com"
	} else {
		apiBase = fmt.Sprintf("https://%s/api/v3", host)
	}

	// GET /repos/{owner}/{repo}/commits?path={dir}&since={date}
	url := fmt.Sprintf("%s/repos/%s/%s/commits?path=%s&since=%sT00:00:00Z&per_page=100",
		apiBase, owner, repo, dir, since)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Printf("    API error for %s/%s path %s: %v\n", owner, repo, dir, err)
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		fmt.Printf("    API %d for %s/%s path %s: %s\n", resp.StatusCode, owner, repo, dir, string(body))
		return nil
	}

	var commits []struct {
		SHA    string `json:"sha"`
		Commit struct {
			Author struct {
				Name string `json:"name"`
				Date string `json:"date"`
			} `json:"author"`
			Message string `json:"message"`
		} `json:"commit"`
		Files []struct {
			Filename string `json:"filename"`
		} `json:"files,omitempty"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&commits); err != nil {
		fmt.Printf("    API decode error for %s/%s: %v\n", owner, repo, err)
		return nil
	}

	var changes []repoChange
	for _, c := range commits {
		// Extract date (YYYY-MM-DD) from ISO 8601
		date := c.Commit.Author.Date
		if len(date) >= 10 {
			date = date[:10]
		}

		// Subject is first line of commit message
		subject := c.Commit.Message
		if idx := strings.IndexByte(subject, '\n'); idx >= 0 {
			subject = subject[:idx]
		}

		rc := repoChange{
			Repo:    repoID,
			Hash:    c.SHA,
			Date:    date,
			Author:  c.Commit.Author.Name,
			Subject: subject,
		}

		// The list endpoint doesn't include files — fetch per-commit if needed
		// For watch purposes, we don't strictly need file lists (commits are
		// already scoped to the directory by the path parameter), but we
		// include them when available from the list response.
		for _, f := range c.Files {
			rc.Files = append(rc.Files, f.Filename)
		}

		changes = append(changes, rc)
	}

	return changes
}

// buildWatchContextPackage assembles the context for agent-driven updates
// from source repo changes.
func buildWatchContextPackage(entityName, entityType, currentPage string, changes []repoChange, toneRules, lastUpdated string) string {
	var b strings.Builder

	b.WriteString("# Watch Context Package\n\n")
	b.WriteString(fmt.Sprintf("Entity: **%s**\n", entityName))
	b.WriteString(fmt.Sprintf("Entity type: **%s**\n", entityType))
	b.WriteString(fmt.Sprintf("Library page last updated: %s\n", lastUpdated))
	b.WriteString(fmt.Sprintf("Source repo commits: %d\n\n", len(changes)))

	b.WriteString("---\n\n")
	b.WriteString("## Current Library Page\n\n")
	b.WriteString("```markdown\n")
	b.WriteString(currentPage)
	if !strings.HasSuffix(currentPage, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("```\n\n")

	b.WriteString("---\n\n")
	b.WriteString("## Source Repository Changes\n\n")
	b.WriteString("These are commits to infrastructure-as-code repositories that affect this entity.\n\n")

	for _, c := range changes {
		// Use repo basename for readability (works for both paths and IDs)
		repoName := c.Repo
		if idx := strings.LastIndex(repoName, "/"); idx >= 0 {
			repoName = repoName[idx+1:]
		}
		b.WriteString(fmt.Sprintf("### %s — %s (%s)\n", c.Date, c.Subject, repoName))
		b.WriteString(fmt.Sprintf("Author: %s | Commit: %s\n", c.Author, c.Hash[:12]))
		if len(c.Files) > 0 {
			b.WriteString("Files:\n")
			for _, f := range c.Files {
				b.WriteString(fmt.Sprintf("  - %s\n", f))
			}
		}
		b.WriteString("\n")
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
	b.WriteString("Update the library page to reflect changes from the source repositories:\n\n")
	b.WriteString("1. Update Inventory Data if field values changed (owners, flags, configs).\n")
	b.WriteString("2. Add or update Operational Notes for significant infrastructure changes.\n")
	b.WriteString("3. Add dated entries to Incident History for outage-related or notable events.\n")
	b.WriteString("4. Do NOT remove existing content unless it is explicitly contradicted.\n")
	b.WriteString("5. Synthesize — do NOT paste raw commit messages or file listings.\n")
	b.WriteString("6. Update the `last_updated` field in frontmatter to today's date.\n")
	b.WriteString("7. Follow the tone rules above.\n")
	b.WriteString("8. Do NOT introduce local filesystem paths (~/..., /Users/..., /home/...). Use web URLs instead.\n")

	// Format rules
	b.WriteString("\n## Format Rules\n\n")
	b.WriteString("Follow these formatting conventions exactly:\n\n")
	b.WriteString("- **Frontmatter field order** (environment): entity_type, aliases, last_updated\n")
	b.WriteString("- **Section heading**: use `## Known Issues` (not `## Known Issues and Quirks`)\n")
	if sections, ok := requiredSections[entityType]; ok {
		b.WriteString(fmt.Sprintf("- **Required sections** for %s pages: %s\n", entityType, strings.Join(sections, ", ")))
	}

	return b.String()
}

func init() {
	watchCmd.Flags().StringVar(&watchEntity, "entity", "", "Watch a single entity (e.g., --entity argus)")
	watchCmd.Flags().BoolVar(&watchDryRun, "dry-run", false, "Generate context packages without invoking the agent")
}
