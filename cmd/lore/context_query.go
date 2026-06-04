package lore

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gbuehler/lore/internal/config"
	"github.com/gbuehler/lore/internal/daemon"
	"github.com/gbuehler/lore/internal/pathutil"
	"github.com/gbuehler/lore/internal/store"
	"github.com/spf13/cobra"
)

var contextQueryDepth int
var contextQueryBrief bool
var contextQueryVault string

var contextQueryCmd = &cobra.Command{
	Use:   "context <node>",
	Short: "Assemble context for an entity",
	Long: `Produces a composed context package for an entity: the node's page content,
its typed relationships, recent mentions in daily logs, and related thread
summaries. Designed to be consumed by an LLM without blowing the context window.

The output is structured markdown that Claude (or any agent) can ingest directly.

Examples:
  lore context Wiki/Services/gateway
  lore context Wiki/Environments/production --depth 2
  lore context Wiki/People/jsmith --brief`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		node := args[0]
		// Strip .md if provided
		node = strings.TrimSuffix(node, ".md")

		// Try daemon first (like query.go does).
		client, err := connectDaemonForCurrentVault()
		if err == nil {
			defer client.Close()
			resp, err := client.Send(&daemon.Request{
				Type:  "context",
				Node:  node,
				Depth: contextQueryDepth,
				Brief: contextQueryBrief,
			})
			if err != nil {
				return err
			}
			if !resp.OK {
				return fmt.Errorf("context error: %s", resp.Error)
			}
			fmt.Print(resp.Content)
			return nil
		}

		// Fall back to direct DB access
		var vaultPath string
		if contextQueryVault != "" {
			vaultPath, _ = filepath.Abs(contextQueryVault)
		} else {
			vaultPath, err = config.FindVault()
			if err != nil {
				return err
			}
			vaultPath, _ = filepath.Abs(vaultPath)
		}
		absVault := vaultPath

		cfg, err := config.Load(vaultPath)
		if err != nil {
			return err
		}

		db, err := store.OpenForVault(store.DefaultPathForVault(vaultPath), vaultPath)
		if err != nil {
			return fmt.Errorf("no index found. Run 'lore daemon start' to build it")
		}
		defer db.Close()

		var b strings.Builder

		// 1. Read the node's page content
		pagePath, err := findPagePath(absVault, cfg, node)
		if err != nil {
			return err
		}
		if pagePath == "" {
			return fmt.Errorf("page not found: %s", node)
		}

		content, err := os.ReadFile(pagePath)
		if err != nil {
			return fmt.Errorf("reading page: %w", err)
		}

		b.WriteString(fmt.Sprintf("# Context: %s\n\n", node))
		b.WriteString("## Page\n\n")
		if contextQueryBrief {
			// Brief mode: just frontmatter + first section
			b.WriteString(truncateToFirstSection(string(content)))
		} else {
			b.WriteString(string(content))
		}
		b.WriteString("\n\n---\n\n")

		// 2. Outgoing edges (typed relationships)
		neighbors, err := db.Neighbors(node, "", contextQueryDepth)
		if err == nil && len(neighbors) > 0 {
			b.WriteString("## Relationships (outgoing)\n\n")
			byType := groupByEdgeType(neighbors)
			for edgeType, items := range byType {
				b.WriteString(fmt.Sprintf("**%s:**\n", edgeType))
				for _, item := range items {
					label := item.Title
					if item.EntityType != "" {
						label += " [" + item.EntityType + "]"
					}
					if item.Depth > 1 {
						label += fmt.Sprintf(" (depth %d)", item.Depth)
					}
					b.WriteString(fmt.Sprintf("- %s\n", label))
				}
				b.WriteString("\n")
			}
			b.WriteString("---\n\n")
		}

		// 3. Incoming edges (who references this node)
		backlinks, err := db.Backlinks(node, "")
		if err == nil && len(backlinks) > 0 {
			b.WriteString("## Referenced by\n\n")
			// Deduplicate and group
			byType := groupByEdgeType(backlinks)
			for edgeType, items := range byType {
				b.WriteString(fmt.Sprintf("**%s:**\n", edgeType))
				shown := 0
				for _, item := range items {
					if shown >= 10 {
						b.WriteString(fmt.Sprintf("- ... and %d more\n", len(items)-10))
						break
					}
					label := item.Title
					if item.EntityType != "" {
						label += " [" + item.EntityType + "]"
					}
					b.WriteString(fmt.Sprintf("- %s\n", label))
					shown++
				}
				b.WriteString("\n")
			}
			b.WriteString("---\n\n")
		}

		// 4. Recent mentions via search (last 5 daily log hits)
		searchResults, err := db.Search(filepath.Base(node), 5)
		if err == nil && len(searchResults) > 0 {
			b.WriteString("## Recent mentions\n\n")
			for _, r := range searchResults {
				// Skip the page itself
				if strings.TrimSuffix(r.RelPath, ".md") == node {
					continue
				}
				b.WriteString(fmt.Sprintf("- **%s** (%s)\n", r.Title, r.RelPath))
				if r.Snippet != "" {
					b.WriteString(fmt.Sprintf("  %s\n", r.Snippet))
				}
			}
			b.WriteString("\n")
		}

		fmt.Print(b.String())
		return nil
	},
}

// findPagePath resolves a node RelPath to an absolute file path,
// searching vault and all subscribed libraries.
func findPagePath(vaultPath string, cfg *config.Config, node string) (string, error) {
	// Try vault first
	candidate, _, err := pathutil.ResolveMarkdownUnderRoot(vaultPath, node)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(candidate); err == nil {
		return candidate, nil
	}

	// Try subscribed libraries
	for _, sub := range cfg.Subscriptions {
		candidate, _, err = pathutil.ResolveMarkdownUnderRoot(sub.ContentPath(), node)
		if err != nil {
			return "", err
		}
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}

	return "", nil
}

// truncateToFirstSection returns content up to and including the first ## heading's body.
func truncateToFirstSection(content string) string {
	lines := strings.Split(content, "\n")
	inFrontmatter := false
	pastFrontmatter := false
	firstSectionFound := false
	var result []string

	for _, line := range lines {
		if line == "---" && !pastFrontmatter {
			if !inFrontmatter {
				inFrontmatter = true
			} else {
				pastFrontmatter = true
			}
			result = append(result, line)
			continue
		}

		if pastFrontmatter && strings.HasPrefix(line, "## ") {
			if firstSectionFound {
				// Hit second section, stop
				result = append(result, "\n[... truncated, use --depth or full context for more ...]")
				break
			}
			firstSectionFound = true
		}
		result = append(result, line)
	}
	return strings.Join(result, "\n")
}

func groupByEdgeType(results []store.GraphResult) map[string][]store.GraphResult {
	groups := make(map[string][]store.GraphResult)
	for _, r := range results {
		groups[r.EdgeType] = append(groups[r.EdgeType], r)
	}
	return groups
}

func init() {
	contextQueryCmd.Flags().IntVar(&contextQueryDepth, "depth", 1, "Graph traversal depth for relationships")
	contextQueryCmd.Flags().BoolVar(&contextQueryBrief, "brief", false, "Truncate page content to first section")
	contextQueryCmd.Flags().StringVar(&contextQueryVault, "vault", "", "Path to vault (auto-detected if not set)")
}
