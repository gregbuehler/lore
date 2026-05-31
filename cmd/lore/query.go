package lore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gregbuehler/lore/internal/config"
	"github.com/gregbuehler/lore/internal/daemon"
	"github.com/gregbuehler/lore/internal/store"
	"github.com/spf13/cobra"
)

var queryJSON bool
var queryLimit int
var queryType string
var queryGraph string
var queryBacklinks string
var queryDepth int

var queryCmd = &cobra.Command{
	Use:   "query <search terms>",
	Short: "Search the vault index",
	Long: `Performs a BM25-ranked full-text search across your vault and all
subscribed libraries. Results include title, path, and a text snippet.

If the daemon is running, queries go through the socket (fastest).
Otherwise, queries the SQLite DB directly (still fast, may be stale).

Graph traversal:
  lore query --graph Wiki/Services/gateway         # outgoing edges
  lore query --backlinks Wiki/Services/gateway     # incoming edges
  lore query --graph Wiki/Services/gateway --depth 2

Examples:
  lore query "gateway cert rotation"
  lore query --type service "gateway"
  lore query --json "deployment orchestration"`,
	Args: cobra.MinimumNArgs(0),
	RunE: func(cmd *cobra.Command, args []string) error {
		// Graph mode
		if queryGraph != "" || queryBacklinks != "" {
			return runGraphQuery()
		}

		if len(args) == 0 {
			return fmt.Errorf("provide search terms")
		}
		query := strings.Join(args, " ")

		// Try daemon, auto-starting if possible.
		client, err := connectDaemonForCurrentVault()
		if err == nil {
			defer client.Close()
			return runDaemonQuery(client, query)
		}

		// Fall back to direct DB query
		return runDirectQuery(query)
	},
}

func runDaemonQuery(client *daemon.Client, query string) error {
	req := &daemon.Request{
		Type:  "query",
		Query: query,
		Limit: queryLimit,
	}
	if queryType != "" {
		req.Filter = map[string]string{"entity_type": queryType}
	}

	resp, err := client.Send(req)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("query error: %s", resp.Error)
	}

	return printResults(resp.Results, resp.ElapsedMs)
}

func runDirectQuery(query string) error {
	vaultPath := resolveVaultPath()
	db, err := store.OpenForVault(store.DefaultPathForVault(vaultPath), vaultPath)
	if err != nil {
		// No DB yet — hint to run daemon
		return fmt.Errorf("no index found. Run 'lore daemon start' to build it")
	}
	defer db.Close()

	results, err := db.Search(query, queryLimit)
	if err != nil {
		return err
	}

	if queryJSON {
		return json.NewEncoder(os.Stdout).Encode(results)
	}

	if len(results) == 0 {
		fmt.Println("No matches.")
		return nil
	}

	for i, r := range results {
		fmt.Printf("%d. %s", i+1, r.Title)
		if r.EntityType != "" {
			fmt.Printf(" [%s]", r.EntityType)
		}
		fmt.Printf("  (%.2f)\n", r.Rank)
		fmt.Printf("   %s\n", r.RelPath)
		if r.Abstract != "" {
			fmt.Printf("   %s\n", r.Abstract)
		} else if r.Snippet != "" {
			fmt.Printf("   %s\n", r.Snippet)
		}
		fmt.Println()
	}
	return nil
}

func runGraphQuery() error {
	node := queryGraph
	reqType := "graph"
	if queryBacklinks != "" {
		node = queryBacklinks
		reqType = "backlinks"
	}

	// Try daemon, auto-starting if possible.
	client, err := connectDaemonForCurrentVault()
	if err == nil {
		defer client.Close()
		req := &daemon.Request{
			Type:     reqType,
			Node:     node,
			EdgeType: queryType,
			Depth:    queryDepth,
		}
		resp, err := client.Send(req)
		if err != nil {
			return err
		}
		if !resp.OK {
			return fmt.Errorf("graph error: %s", resp.Error)
		}
		return printResults(resp.Results, resp.ElapsedMs)
	}

	// Direct DB
	vaultPath := resolveVaultPath()
	db, err := store.OpenForVault(store.DefaultPathForVault(vaultPath), vaultPath)
	if err != nil {
		return fmt.Errorf("no index found. Run 'lore daemon start' to build it")
	}
	defer db.Close()

	var results []store.GraphResult
	if reqType == "backlinks" {
		results, err = db.Backlinks(node, queryType)
	} else {
		results, err = db.Neighbors(node, queryType, queryDepth)
	}
	if err != nil {
		return err
	}

	if queryJSON {
		return json.NewEncoder(os.Stdout).Encode(results)
	}

	if len(results) == 0 {
		fmt.Printf("No %s for %s\n", reqType, node)
		return nil
	}

	for _, r := range results {
		prefix := strings.Repeat("  ", r.Depth)
		fmt.Printf("%s%s → %s", prefix, r.EdgeType, r.Title)
		if r.EntityType != "" {
			fmt.Printf(" [%s]", r.EntityType)
		}
		fmt.Printf(" (%s)\n", r.RelPath)
	}
	return nil
}

func printResults(results []daemon.Result, elapsedMs float64) error {
	if queryJSON {
		return json.NewEncoder(os.Stdout).Encode(results)
	}

	if len(results) == 0 {
		fmt.Println("No results.")
		return nil
	}

	for i, r := range results {
		// Search result
		if r.Score > 0 {
			fmt.Printf("%d. %s", i+1, r.Title)
			if r.EntityType != "" {
				fmt.Printf(" [%s]", r.EntityType)
			}
			fmt.Printf("  (%.2f)\n", r.Score)
			fmt.Printf("   %s\n", r.RelPath)
			if r.Abstract != "" {
				fmt.Printf("   %s\n", r.Abstract)
			} else if r.Snippet != "" {
				fmt.Printf("   %s\n", r.Snippet)
			}
			fmt.Println()
		} else {
			// Graph result
			prefix := strings.Repeat("  ", r.Depth)
			fmt.Printf("%s%s → %s", prefix, r.EdgeType, r.Title)
			if r.EntityType != "" {
				fmt.Printf(" [%s]", r.EntityType)
			}
			fmt.Printf(" (%s)\n", r.RelPath)
		}
	}

	if elapsedMs > 0 {
		fmt.Printf("\n(%.2fms)\n", elapsedMs)
	}
	return nil
}

// resolveVaultPath finds the vault path for auto-starting the daemon.
// Checks: LORE_VAULT env, then walks up from cwd looking for .lore/config.yaml.
func resolveVaultPath() string {
	if v := os.Getenv("LORE_VAULT"); v != "" {
		abs, _ := filepath.Abs(v)
		return abs
	}
	if v, err := config.FindVault(); err == nil {
		abs, _ := filepath.Abs(v)
		return abs
	}
	return ""
}

func init() {
	queryCmd.Flags().BoolVar(&queryJSON, "json", false, "Output results as JSON")
	queryCmd.Flags().IntVarP(&queryLimit, "limit", "n", 10, "Max results")
	queryCmd.Flags().StringVar(&queryType, "type", "", "Filter by entity_type (or edge_type for graph)")
	queryCmd.Flags().StringVar(&queryGraph, "graph", "", "Show outgoing edges from a node")
	queryCmd.Flags().StringVar(&queryBacklinks, "backlinks", "", "Show incoming edges to a node")
	queryCmd.Flags().IntVar(&queryDepth, "depth", 1, "Graph traversal depth")
}
