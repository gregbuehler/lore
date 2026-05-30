package lore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gbuehler/lore/internal/config"
	"github.com/gbuehler/lore/internal/daemon"
	entitypkg "github.com/gbuehler/lore/internal/entity"
	"github.com/gbuehler/lore/internal/parse"
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
		if !entitypkg.ValidTypes[entityCreateType] {
			return fmt.Errorf("unknown entity type %q; valid types: service, environment, person, tool, infrastructure, organization, customer, vendor, concept", entityCreateType)
		}

		// Try daemon first.
		client, err := connectDaemonForCurrentVault()
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
		content := entitypkg.BuildContent(entityCreateType, title, today)

		if err := os.WriteFile(destPath, []byte(content), 0644); err != nil {
			return fmt.Errorf("writing entity file: %w", err)
		}

		fmt.Println(destPath)
		return nil
	},
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
			// Try daemon first.
			client, err := connectDaemonForCurrentVault()
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
			content, err = parse.SetFrontmatterField(content, key, val)
			if err != nil {
				return fmt.Errorf("updating frontmatter key %q: %w", key, err)
			}
		}

		// Apply --append-changelog
		if entityUpdateAppendChangelog != "" {
			today := time.Now().Format("2006-01-02")
			entry := fmt.Sprintf("- **%s**: %s", today, entityUpdateAppendChangelog)
			content, err = entitypkg.AppendToSection(content, "Change Log", entry)
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
			content, err = entitypkg.AppendToSection(content, sectionName, text)
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

		// Try daemon first for content retrieval.
		client, err := connectDaemonForCurrentVault()
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
	fm := parse.ParseFrontmatterMap(content)
	fm["path"] = entityPath

	result := map[string]any{
		"frontmatter": fm,
	}

	// Try daemon for relationships.
	client, err := connectDaemonForCurrentVault()
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

// --------------------------------------------------------------------------
// entity delete
// --------------------------------------------------------------------------

var entityDeleteConfirm bool
var entityDeleteForce bool
var entityDeleteVault string

var entityDeleteCmd = &cobra.Command{
	Use:   "delete <path>",
	Short: "Delete a Wiki entity page",
	Long: `Deletes the markdown file for an entity page.

Requires --confirm for safety. Warns about files that reference (link to)
this entity before deleting. If the backlink index cannot be checked, deletion
is rejected unless --force is also provided.

Examples:
  lore entity delete Wiki/Services/foo --confirm
  lore entity delete Wiki/Services/foo --confirm --force`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		entityPath := strings.TrimSuffix(args[0], ".md")

		if !entityDeleteConfirm {
			return fmt.Errorf("pass --confirm to delete %s", entityPath)
		}

		// Try daemon first — it handles backlink warnings + index removal atomically.
		client, err := connectDaemonForCurrentVault()
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

		if err := checkEntityDeleteBacklinks(vaultPath, entityPath, entityDeleteForce); err != nil {
			return err
		}

		if err := os.Remove(filePath); err != nil {
			return fmt.Errorf("deleting entity file: %w", err)
		}

		fmt.Printf("deleted: %s\n", filePath)
		return nil
	},
}

func checkEntityDeleteBacklinks(vaultPath, entityPath string, force bool) error {
	dbPath := store.DefaultPathForVault(vaultPath)
	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			return entityDeleteBacklinkCheckError(force, fmt.Errorf("index not found at %s", dbPath))
		}
		return entityDeleteBacklinkCheckError(force, fmt.Errorf("checking index: %w", err))
	}

	db, err := store.OpenForVault(dbPath, vaultPath)
	if err != nil {
		return entityDeleteBacklinkCheckError(force, fmt.Errorf("opening index: %w", err))
	}
	defer db.Close()

	backlinks, err := db.Backlinks(entityPath, "")
	if err != nil {
		return entityDeleteBacklinkCheckError(force, fmt.Errorf("querying backlinks: %w", err))
	}
	if len(backlinks) > 0 {
		fmt.Fprintf(os.Stderr, "warning: %d page(s) link to %s:\n", len(backlinks), entityPath)
		for _, r := range backlinks {
			fmt.Fprintf(os.Stderr, "  - %s\n", r.RelPath)
		}
	}
	return nil
}

func entityDeleteBacklinkCheckError(force bool, err error) error {
	if force {
		fmt.Fprintf(os.Stderr, "warning: could not check backlinks before delete: %v\n", err)
		return nil
	}
	return fmt.Errorf("could not check backlinks before delete: %w (run 'lore reindex' or pass --force to delete anyway)", err)
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
		// Try daemon.
		client, err := connectDaemonForCurrentVault()
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

		fm := parse.ParseFrontmatterMap(string(data))
		entityType := frontmatterString(fm["entity_type"])
		title := frontmatterString(fm["title"])
		lastUpdated := frontmatterString(fm["last_updated"])

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

func frontmatterString(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

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
	entityDeleteCmd.Flags().BoolVar(&entityDeleteForce, "force", false, "Delete even when backlinks cannot be checked")
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
