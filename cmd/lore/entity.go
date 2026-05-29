package lore

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gbuehler/lore/internal/config"
	"github.com/gbuehler/lore/internal/daemon"
	"github.com/gbuehler/lore/internal/pathutil"
	"github.com/gbuehler/lore/internal/store"
	"github.com/spf13/cobra"
)

// --------------------------------------------------------------------------
// Top-level entity command
// --------------------------------------------------------------------------

var entityCmd = &cobra.Command{
	Use:   "entity",
	Short: "CRUD commands for Wiki entity pages",
	Long: `Commands for creating, reading, updating, and deleting Wiki entity pages.

Entity pages live under <vault>/Wiki/<Category>/ and carry structured
frontmatter (entity_type, title, last_updated, tags).

Examples:
  lore entity create Wiki/Services/foo --type service --title "Foo Service"
  lore entity update Wiki/Services/foo --set last_updated=2026-05-09
  lore entity update Wiki/Services/foo --append-changelog "Fixed cert rotation"
  lore entity get Wiki/Services/gateway
  lore entity get Wiki/Services/gateway --json
  lore entity delete Wiki/Services/foo --confirm
  lore entity list
  lore entity list --type service`,
}

// --------------------------------------------------------------------------
// Supported entity types
// --------------------------------------------------------------------------

var validEntityTypes = map[string]bool{
	"service":        true,
	"environment":    true,
	"person":         true,
	"tool":           true,
	"infrastructure": true,
	"organization":   true,
	"customer":       true,
	"vendor":         true,
	"concept":        true,
}

// --------------------------------------------------------------------------
// entity create
// --------------------------------------------------------------------------

var entityCreateType string
var entityCreateTitle string
var entityCreateVault string

var entityCreateCmd = &cobra.Command{
	Use:   "create <path>",
	Short: "Create a new Wiki entity page",
	Long: `Creates a new markdown file at <vault>/<path>.md with appropriate
frontmatter and section scaffolding for the given entity type.

The <path> argument is relative to the vault root, e.g. Wiki/Services/foo.

Errors if the file already exists — use 'lore entity update' instead.

Examples:
  lore entity create Wiki/Services/foo --type service
  lore entity create Wiki/People/jsmith --type person --title "Jane Smith"
  lore entity create Wiki/Environments/staging --type environment`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		entityPath := strings.TrimSuffix(args[0], ".md")

		if entityCreateType == "" {
			return fmt.Errorf("--type is required (service, environment, person, tool, infrastructure, organization, customer, vendor, concept)")
		}
		if !validEntityTypes[entityCreateType] {
			return fmt.Errorf("unknown entity type %q; valid types: service, environment, person, tool, infrastructure, organization, customer, vendor, concept", entityCreateType)
		}

		// Try daemon first
		client, err := daemon.Connect()
		if err != nil {
			vaultPath := resolveVaultPath()
			if vaultPath != "" {
				client, err = daemon.EnsureDaemon(vaultPath)
			}
		}
		if err == nil {
			defer client.Close()
			resp, err := client.Send(&daemon.Request{
				Type:        "entity_create",
				EntityPath:  entityPath,
				EntityType:  entityCreateType,
				EntityTitle: entityCreateTitle,
			})
			if err == nil {
				if !resp.OK {
					return fmt.Errorf("%s", resp.Error)
				}
				fmt.Println(resp.Content)
				return nil
			}
			// Send failed — fall through to direct file ops
		}

		// Fallback: direct file operations
		vaultPath, err := resolveEntityVault(entityCreateVault)
		if err != nil {
			return err
		}

		title := entityCreateTitle
		if title == "" {
			title = filepath.Base(entityPath)
		}

		destPath, entityPath, err := pathutil.ResolveMarkdownUnderRoot(vaultPath, entityPath)
		if err != nil {
			return err
		}

		// Refuse to overwrite
		if _, err := os.Stat(destPath); err == nil {
			return fmt.Errorf("file already exists: %s (use 'lore entity update' to modify it)", destPath)
		}

		// Ensure parent directory exists
		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			return fmt.Errorf("creating directory: %w", err)
		}

		today := time.Now().Format("2006-01-02")
		content := buildEntityContent(entityCreateType, title, today)

		if err := os.WriteFile(destPath, []byte(content), 0644); err != nil {
			return fmt.Errorf("writing entity file: %w", err)
		}

		fmt.Println(destPath)
		return nil
	},
}

// buildEntityContent produces the full markdown content for a new entity file.
func buildEntityContent(entityType, title, today string) string {
	var sb strings.Builder

	sb.WriteString("---\n")
	sb.WriteString(fmt.Sprintf("entity_type: %s\n", entityType))
	sb.WriteString(fmt.Sprintf("title: \"%s\"\n", title))
	sb.WriteString(fmt.Sprintf("last_updated: %s\n", today))
	sb.WriteString("tags:\n")
	sb.WriteString(fmt.Sprintf("  - %s\n", entityType))
	sb.WriteString("---\n")
	sb.WriteString(fmt.Sprintf("# %s\n", title))
	sb.WriteString("\n")

	switch entityType {
	case "person":
		sb.WriteString("## What They Do\n\n")
	default:
		sb.WriteString("## What It Does\n\n")
	}

	sb.WriteString("## Known Issues\n\n")
	sb.WriteString("## Change Log\n\n")

	return sb.String()
}

// --------------------------------------------------------------------------
// entity update
// --------------------------------------------------------------------------

var entityUpdateSets []string
var entityUpdateAppendChangelog string
var entityUpdateAppendSection []string // two-element: [section name, text]
var entityUpdateVault string

var entityUpdateCmd = &cobra.Command{
	Use:   "update <path>",
	Short: "Update frontmatter or append to sections of an entity page",
	Long: `Updates an existing entity page.

  --set key=value       Updates a frontmatter field (repeatable).
  --append-changelog    Appends a dated entry to the ## Change Log section.
  --append-section      Appends text to a named section (repeatable, format: "Section Name:text").

If the file does not exist, an error is returned.

Examples:
  lore entity update Wiki/Services/gateway --set last_updated=2026-05-09
  lore entity update Wiki/Services/gateway --set status=deprecated --set last_updated=2026-05-09
  lore entity update Wiki/Services/gateway --append-changelog "Fixed cert rotation"
  lore entity update Wiki/Services/gateway --append-section "Known Issues:TLS handshake fails on arm64"`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		entityPath := strings.TrimSuffix(args[0], ".md")

		// Build set_fields map from --set flags
		setFields := make(map[string]string)
		for _, kv := range entityUpdateSets {
			parts := strings.SplitN(kv, "=", 2)
			if len(parts) != 2 {
				return fmt.Errorf("invalid --set value %q: expected key=value", kv)
			}
			setFields[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}

		// --append-section is not yet supported via daemon; track whether we need fallback
		needsFallback := len(entityUpdateAppendSection) > 0

		if !needsFallback {
			// Try daemon first
			client, err := daemon.Connect()
			if err != nil {
				vaultPath := resolveVaultPath()
				if vaultPath != "" {
					client, err = daemon.EnsureDaemon(vaultPath)
				}
			}
			if err == nil {
				defer client.Close()
				resp, err := client.Send(&daemon.Request{
					Type:       "entity_update",
					EntityPath: entityPath,
					SetFields:  setFields,
					Changelog:  entityUpdateAppendChangelog,
				})
				if err == nil {
					if !resp.OK {
						return fmt.Errorf("%s", resp.Error)
					}
					fmt.Printf("updated: %s\n", resp.Content)
					return nil
				}
				// Send failed — fall through to direct file ops
			}
		}

		// Fallback: direct file operations
		vaultPath, err := resolveEntityVault(entityUpdateVault)
		if err != nil {
			return err
		}

		filePath, _, err := pathutil.ResolveMarkdownUnderRoot(vaultPath, entityPath)
		if err != nil {
			return err
		}
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			return fmt.Errorf("file does not exist: %s (use 'lore entity create' to create it)", filePath)
		}

		data, err := os.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("reading entity file: %w", err)
		}
		content := string(data)

		// Apply --set key=value updates to frontmatter
		for key, val := range setFields {
			content, err = setFrontmatterField(content, key, val)
			if err != nil {
				return fmt.Errorf("updating frontmatter key %q: %w", key, err)
			}
		}

		// Apply --append-changelog
		if entityUpdateAppendChangelog != "" {
			today := time.Now().Format("2006-01-02")
			entry := fmt.Sprintf("- **%s**: %s", today, entityUpdateAppendChangelog)
			content, err = appendToSection(content, "Change Log", entry)
			if err != nil {
				return fmt.Errorf("appending changelog: %w", err)
			}
		}

		// Apply --append-section "Section Name:text"
		for _, sv := range entityUpdateAppendSection {
			idx := strings.Index(sv, ":")
			if idx < 0 {
				return fmt.Errorf("invalid --append-section value %q: expected \"Section Name:text\"", sv)
			}
			sectionName := strings.TrimSpace(sv[:idx])
			text := strings.TrimSpace(sv[idx+1:])
			content, err = appendToSection(content, sectionName, text)
			if err != nil {
				return fmt.Errorf("appending to section %q: %w", sectionName, err)
			}
		}

		if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
			return fmt.Errorf("writing entity file: %w", err)
		}

		fmt.Printf("updated: %s\n", filePath)
		return nil
	},
}

// setFrontmatterField sets or adds a key in the YAML frontmatter block.
// The frontmatter is delimited by leading and trailing `---` lines.
func setFrontmatterField(content, key, value string) (string, error) {
	if !strings.HasPrefix(content, "---\n") {
		return content, fmt.Errorf("file does not begin with YAML frontmatter (---)")
	}

	end := strings.Index(content[4:], "\n---")
	if end < 0 {
		return content, fmt.Errorf("frontmatter closing --- not found")
	}
	// Absolute end of the frontmatter block (the \n before ---)
	fmEnd := 4 + end // index of \n before closing ---
	fmBlock := content[4:fmEnd]
	afterFm := content[fmEnd:] // starts with \n---

	// Try to replace an existing key
	lines := strings.Split(fmBlock, "\n")
	replaced := false
	for i, line := range lines {
		// Match "key:" or "key: ..." at start of line (not indented — top-level only)
		if strings.HasPrefix(line, key+":") {
			lines[i] = fmt.Sprintf("%s: %s", key, value)
			replaced = true
			break
		}
	}

	if !replaced {
		// Append before closing ---
		lines = append(lines, fmt.Sprintf("%s: %s", key, value))
	}

	newFm := strings.Join(lines, "\n")
	return "---\n" + newFm + afterFm, nil
}

// appendToSection appends text under the named ## section.
// If the section is not found, it is created at the end of the file.
func appendToSection(content, sectionName, text string) (string, error) {
	target := "## " + sectionName
	lines := strings.Split(content, "\n")

	// Find the target section
	sectionIdx := -1
	for i, line := range lines {
		if strings.TrimRight(line, " \t") == target {
			sectionIdx = i
			break
		}
	}

	if sectionIdx < 0 {
		// Section not found — append it at the end
		if !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		content += fmt.Sprintf("\n%s\n\n%s\n", target, text)
		return content, nil
	}

	// Find the end of this section (next ## heading or EOF)
	insertAt := len(lines)
	for i := sectionIdx + 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "## ") || strings.HasPrefix(lines[i], "# ") {
			insertAt = i
			break
		}
	}

	// Insert before the next section (or at end), with a blank line separator
	// Walk back past trailing blank lines so we don't accumulate excess whitespace
	insertBefore := insertAt
	for insertBefore > sectionIdx+1 && strings.TrimSpace(lines[insertBefore-1]) == "" {
		insertBefore--
	}

	newLines := make([]string, 0, len(lines)+2)
	newLines = append(newLines, lines[:insertBefore]...)
	newLines = append(newLines, text)
	newLines = append(newLines, "")
	newLines = append(newLines, lines[insertBefore:]...)

	return strings.Join(newLines, "\n"), nil
}

// --------------------------------------------------------------------------
// entity get
// --------------------------------------------------------------------------

var entityGetJSON bool
var entityGetFull bool
var entityGetVault string

var entityGetCmd = &cobra.Command{
	Use:   "get <path>",
	Short: "Print an entity page",
	Long: `Prints information about an entity page.

Default: prints frontmatter as YAML + the first section body.
--full:  prints the entire file content.
--json:  prints structured JSON (frontmatter fields + graph relationships from daemon).

Examples:
  lore entity get Wiki/Services/gateway
  lore entity get Wiki/Services/gateway --full
  lore entity get Wiki/Services/gateway --json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		entityPath := strings.TrimSuffix(args[0], ".md")

		// Try daemon first for content retrieval
		client, err := daemon.Connect()
		if err != nil {
			vaultPath := resolveVaultPath()
			if vaultPath != "" {
				client, err = daemon.EnsureDaemon(vaultPath)
			}
		}
		var content string
		if err == nil {
			defer client.Close()
			resp, sendErr := client.Send(&daemon.Request{
				Type:       "entity_get",
				EntityPath: entityPath,
			})
			if sendErr == nil && resp.OK {
				content = resp.Content
			} else if sendErr == nil && !resp.OK {
				return fmt.Errorf("entity not found: %s", entityPath)
			}
			// sendErr != nil — fall through to direct file read
		}

		if content == "" {
			// Fallback: direct file read
			vaultPath, err := resolveEntityVault(entityGetVault)
			if err != nil {
				return err
			}
			filePath, _, err := pathutil.ResolveMarkdownUnderRoot(vaultPath, entityPath)
			if err != nil {
				return err
			}
			if _, err := os.Stat(filePath); os.IsNotExist(err) {
				return fmt.Errorf("entity not found: %s", filePath)
			}
			data, err := os.ReadFile(filePath)
			if err != nil {
				return fmt.Errorf("reading entity file: %w", err)
			}
			content = string(data)
		}

		if entityGetFull {
			fmt.Print(content)
			return nil
		}

		if entityGetJSON {
			vaultPath, _ := resolveEntityVault(entityGetVault)
			return runEntityGetJSON(entityPath, content, vaultPath)
		}

		// Default: frontmatter + first section
		fmt.Print(truncateToFirstSection(content))
		return nil
	},
}

func runEntityGetJSON(entityPath, content, vaultPath string) error {
	fm := parseFrontmatter(content)
	fm["path"] = entityPath

	result := map[string]any{
		"frontmatter": fm,
	}

	// Try daemon for relationships
	client, err := daemon.Connect()
	if err != nil {
		v := resolveVaultPath()
		if v != "" {
			client, err = daemon.EnsureDaemon(v)
		}
	}
	if err == nil {
		defer client.Close()

		// Outgoing edges
		resp, err := client.Send(&daemon.Request{
			Type:  "graph",
			Node:  entityPath,
			Depth: 1,
		})
		if err == nil && resp.OK && len(resp.Results) > 0 {
			result["relationships"] = resp.Results
		}

		// Backlinks
		blResp, err := client.Send(&daemon.Request{
			Type: "backlinks",
			Node: entityPath,
		})
		if err == nil && blResp.OK && len(blResp.Results) > 0 {
			result["backlinks"] = blResp.Results
		}
	}

	return json.NewEncoder(os.Stdout).Encode(result)
}

// parseFrontmatter extracts top-level scalar key: value pairs from YAML frontmatter.
func parseFrontmatter(content string) map[string]any {
	fm := make(map[string]any)
	if !strings.HasPrefix(content, "---\n") {
		return fm
	}
	end := strings.Index(content[4:], "\n---")
	if end < 0 {
		return fm
	}
	block := content[4 : 4+end]
	scanner := bufio.NewScanner(strings.NewReader(block))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue // skip indented (list/map values)
		}
		idx := strings.Index(line, ":")
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		// Strip surrounding quotes
		if len(val) >= 2 && ((val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'')) {
			val = val[1 : len(val)-1]
		}
		fm[key] = val
	}
	return fm
}

// --------------------------------------------------------------------------
// entity delete
// --------------------------------------------------------------------------

var entityDeleteConfirm bool
var entityDeleteVault string

var entityDeleteCmd = &cobra.Command{
	Use:   "delete <path>",
	Short: "Delete a Wiki entity page",
	Long: `Deletes the markdown file for an entity page.

Requires --confirm for safety. If the daemon is running, warns about
files that reference (link to) this entity before deleting.

Examples:
  lore entity delete Wiki/Services/foo --confirm`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		entityPath := strings.TrimSuffix(args[0], ".md")

		if !entityDeleteConfirm {
			return fmt.Errorf("pass --confirm to delete %s", entityPath)
		}

		// Try daemon first — it handles backlink warnings + index removal atomically
		client, err := daemon.Connect()
		if err != nil {
			v := resolveVaultPath()
			if v != "" {
				client, err = daemon.EnsureDaemon(v)
			}
		}
		if err == nil {
			defer client.Close()
			resp, sendErr := client.Send(&daemon.Request{
				Type:       "entity_delete",
				EntityPath: entityPath,
				Confirm:    entityDeleteConfirm,
			})
			if sendErr == nil {
				if !resp.OK {
					return fmt.Errorf("%s", resp.Error)
				}
				fmt.Println(resp.Content)
				return nil
			}
			// Send failed — fall through to direct ops
		}

		// Fallback: direct file operations
		vaultPath, err := resolveEntityVault(entityDeleteVault)
		if err != nil {
			return err
		}

		filePath, _, err := pathutil.ResolveMarkdownUnderRoot(vaultPath, entityPath)
		if err != nil {
			return err
		}
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			return fmt.Errorf("entity not found: %s", filePath)
		}

		// Warn about incoming links via direct DB
		db, dbErr := store.Open(store.DefaultPath())
		if dbErr == nil {
			defer db.Close()
			backlinks, blErr := db.Backlinks(entityPath, "")
			if blErr == nil && len(backlinks) > 0 {
				fmt.Fprintf(os.Stderr, "warning: %d page(s) link to %s:\n", len(backlinks), entityPath)
				for _, r := range backlinks {
					fmt.Fprintf(os.Stderr, "  - %s\n", r.RelPath)
				}
			}
		}

		if err := os.Remove(filePath); err != nil {
			return fmt.Errorf("deleting entity file: %w", err)
		}

		fmt.Printf("deleted: %s\n", filePath)
		return nil
	},
}

// --------------------------------------------------------------------------
// entity list
// --------------------------------------------------------------------------

var entityListType string
var entityListVault string
var entityListJSON bool

var entityListCmd = &cobra.Command{
	Use:   "list",
	Short: "List Wiki entity pages",
	Long: `Lists entity pages from the vault.

Uses the daemon when available (faster, index-backed).
Falls back to a direct file walk of <vault>/Wiki/.

--type filters by entity_type frontmatter field.

Examples:
  lore entity list
  lore entity list --type service
  lore entity list --json`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Try daemon
		client, err := daemon.Connect()
		if err != nil {
			v := resolveVaultPath()
			if v != "" {
				client, err = daemon.EnsureDaemon(v)
			}
		}
		if err == nil {
			defer client.Close()
			return runEntityListDaemon(client)
		}

		// Fall back to file walk
		return runEntityListFileWalk()
	},
}

func runEntityListDaemon(client *daemon.Client) error {
	req := buildEntityListRequest(entityListType)

	resp, err := client.Send(req)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("list error: %s", resp.Error)
	}

	if entityListJSON {
		return json.NewEncoder(os.Stdout).Encode(resp.Results)
	}

	printEntityList(resp.Results)
	return nil
}

func buildEntityListRequest(entityType string) *daemon.Request {
	return &daemon.Request{
		Type:       "entity_list",
		EntityType: entityType,
	}
}

type entityListItem struct {
	Path        string `json:"path"`
	Title       string `json:"title"`
	EntityType  string `json:"entity_type"`
	LastUpdated string `json:"last_updated"`
}

func runEntityListFileWalk() error {
	vaultPath, err := resolveEntityVault(entityListVault)
	if err != nil {
		return err
	}

	wikiDir := filepath.Join(vaultPath, "Wiki")
	if _, err := os.Stat(wikiDir); os.IsNotExist(err) {
		fmt.Println("No Wiki directory found.")
		return nil
	}

	var items []entityListItem

	err = filepath.Walk(wikiDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".md") {
			return err
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		fm := parseFrontmatter(string(data))
		entityType, _ := fm["entity_type"].(string)
		title, _ := fm["title"].(string)
		lastUpdated, _ := fm["last_updated"].(string)

		if entityListType != "" && !strings.EqualFold(entityType, entityListType) {
			return nil
		}

		// Compute relative path from vault root
		relPath, _ := filepath.Rel(vaultPath, path)
		relPath = strings.TrimSuffix(relPath, ".md")

		if title == "" {
			title = filepath.Base(relPath)
		}

		items = append(items, entityListItem{
			Path:        relPath,
			Title:       title,
			EntityType:  entityType,
			LastUpdated: lastUpdated,
		})
		return nil
	})
	if err != nil {
		return fmt.Errorf("walking Wiki directory: %w", err)
	}

	if entityListJSON {
		return json.NewEncoder(os.Stdout).Encode(items)
	}

	if len(items) == 0 {
		if entityListType != "" {
			fmt.Printf("No entities of type %q found.\n", entityListType)
		} else {
			fmt.Println("No entities found.")
		}
		return nil
	}

	for _, item := range items {
		fmt.Printf("%-45s  %-14s  %-12s  %s\n", item.Path, item.EntityType, item.LastUpdated, item.Title)
	}
	return nil
}

func printEntityList(results []daemon.Result) {
	if len(results) == 0 {
		if entityListType != "" {
			fmt.Printf("No entities of type %q found.\n", entityListType)
		} else {
			fmt.Println("No entities found.")
		}
		return
	}
	for _, r := range results {
		relPath := strings.TrimSuffix(r.RelPath, ".md")
		fmt.Printf("%-45s  %-14s  %s\n", relPath, r.EntityType, r.Title)
	}
}

// --------------------------------------------------------------------------
// Helpers
// --------------------------------------------------------------------------

// resolveEntityVault returns an absolute vault path, preferring the flag
// value, then LORE_VAULT env, then config.FindVault().
func resolveEntityVault(flagVault string) (string, error) {
	if flagVault != "" {
		abs, err := filepath.Abs(flagVault)
		if err != nil {
			return "", fmt.Errorf("resolving vault path: %w", err)
		}
		if _, err := os.Stat(abs); err != nil {
			return "", fmt.Errorf("vault path does not exist: %s", abs)
		}
		return abs, nil
	}

	if v := os.Getenv("LORE_VAULT"); v != "" {
		abs, err := filepath.Abs(v)
		if err != nil {
			return "", fmt.Errorf("resolving LORE_VAULT: %w", err)
		}
		if _, err := os.Stat(abs); err != nil {
			return "", fmt.Errorf("LORE_VAULT path does not exist: %s", abs)
		}
		return abs, nil
	}

	vaultPath, err := config.FindVault()
	if err != nil {
		return "", fmt.Errorf("specify --vault or set LORE_VAULT: %w", err)
	}
	abs, err := filepath.Abs(vaultPath)
	if err != nil {
		return "", fmt.Errorf("resolving vault path: %w", err)
	}
	return abs, nil
}

// --------------------------------------------------------------------------
// init
// --------------------------------------------------------------------------

func init() {
	// entity create flags
	entityCreateCmd.Flags().StringVar(&entityCreateType, "type", "", "Entity type (required): service, environment, person, tool, infrastructure, organization, customer, vendor, concept")
	entityCreateCmd.Flags().StringVar(&entityCreateTitle, "title", "", "Entity title (defaults to basename of path)")
	entityCreateCmd.Flags().StringVar(&entityCreateVault, "vault", "", "Path to vault (auto-detected if omitted)")

	// entity update flags
	entityUpdateCmd.Flags().StringArrayVar(&entityUpdateSets, "set", nil, "Set a frontmatter field: key=value (repeatable)")
	entityUpdateCmd.Flags().StringVar(&entityUpdateAppendChangelog, "append-changelog", "", "Append a dated entry to the ## Change Log section")
	entityUpdateCmd.Flags().StringArrayVar(&entityUpdateAppendSection, "append-section", nil, "Append text to a named section: \"Section Name:text\" (repeatable)")
	entityUpdateCmd.Flags().StringVar(&entityUpdateVault, "vault", "", "Path to vault (auto-detected if omitted)")

	// entity get flags
	entityGetCmd.Flags().BoolVar(&entityGetJSON, "json", false, "Output structured JSON (frontmatter + relationships)")
	entityGetCmd.Flags().BoolVar(&entityGetFull, "full", false, "Print entire file content")
	entityGetCmd.Flags().StringVar(&entityGetVault, "vault", "", "Path to vault (auto-detected if omitted)")

	// entity delete flags
	entityDeleteCmd.Flags().BoolVar(&entityDeleteConfirm, "confirm", false, "Confirm deletion (required)")
	entityDeleteCmd.Flags().StringVar(&entityDeleteVault, "vault", "", "Path to vault (auto-detected if omitted)")

	// entity list flags
	entityListCmd.Flags().StringVar(&entityListType, "type", "", "Filter by entity_type")
	entityListCmd.Flags().StringVar(&entityListVault, "vault", "", "Path to vault (auto-detected if omitted)")
	entityListCmd.Flags().BoolVar(&entityListJSON, "json", false, "Output as JSON")

	// Register subcommands
	entityCmd.AddCommand(entityCreateCmd)
	entityCmd.AddCommand(entityUpdateCmd)
	entityCmd.AddCommand(entityGetCmd)
	entityCmd.AddCommand(entityDeleteCmd)
	entityCmd.AddCommand(entityListCmd)
}
