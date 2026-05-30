package lore

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gbuehler/lore/internal/config"
	"github.com/gbuehler/lore/internal/pathutil"
	"github.com/spf13/cobra"
)

var threadCmd = &cobra.Command{
	Use:   "thread",
	Short: "Thread management commands",
	Long:  `Commands for creating and managing investigation threads.`,
}

var threadNewRelated []string
var threadNewStatus string
var threadNewVault string

var threadNewCmd = &cobra.Command{
	Use:   "new <topic>",
	Short: "Scaffold a new investigation thread",
	Long: `Creates a new investigation thread file under <vault>/Threads/.

The file is populated with standard frontmatter and section scaffolding.
The path to the created file is printed on success so it can be opened
directly by the caller.

Examples:
  lore thread new "Auth Token Expiry Bug"
  lore thread new "Service Mesh Latency" --related Wiki/Services/gateway,Wiki/Environments/production
  lore thread new "FedRAMP Gap Analysis" --status active --vault ~/Documents/lore/jane`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		topic := args[0]
		if strings.TrimSpace(topic) == "" {
			return fmt.Errorf("topic must not be empty")
		}

		// Resolve vault path
		vaultPath := threadNewVault
		if vaultPath == "" {
			var err error
			vaultPath, err = config.FindVault()
			if err != nil {
				return fmt.Errorf("specify --vault or run from within a vault: %w", err)
			}
		} else {
			abs, err := filepath.Abs(vaultPath)
			if err != nil {
				return fmt.Errorf("resolving vault path: %w", err)
			}
			vaultPath = abs
		}

		// Validate vault exists
		if _, err := os.Stat(vaultPath); err != nil {
			return fmt.Errorf("vault path does not exist: %s", vaultPath)
		}

		// Ensure Threads directory exists
		threadsDir := filepath.Join(vaultPath, "Threads")
		if err := os.MkdirAll(threadsDir, 0755); err != nil {
			return fmt.Errorf("creating Threads directory: %w", err)
		}

		destPath, err := resolveThreadPath(threadsDir, topic)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			return fmt.Errorf("creating thread directory: %w", err)
		}

		// Refuse to overwrite an existing file
		if _, err := os.Stat(destPath); err == nil {
			return fmt.Errorf("file already exists: %s", destPath)
		}

		// Build frontmatter
		content := buildThreadContent(topic, threadNewStatus, threadNewRelated)

		if err := pathutil.AtomicWriteFile(destPath, []byte(content), 0644); err != nil {
			return fmt.Errorf("writing thread file: %w", err)
		}

		fmt.Println(destPath)
		return nil
	},
}

func resolveThreadPath(threadsDir, topic string) (string, error) {
	absPath, _, err := pathutil.ResolveMarkdownUnderRoot(threadsDir, topic)
	if err != nil {
		return "", err
	}
	return absPath, nil
}

// buildThreadContent produces the full markdown content for a new thread file.
func buildThreadContent(topic, status string, related []string) string {
	var sb strings.Builder

	sb.WriteString("---\n")
	sb.WriteString(fmt.Sprintf("status: %s\n", status))

	if len(related) > 0 {
		sb.WriteString("related:\n")
		for _, r := range related {
			r = strings.TrimSpace(r)
			if r != "" {
				sb.WriteString(fmt.Sprintf("  - \"[[%s]]\"\n", r))
			}
		}
	}

	sb.WriteString("tags:\n")
	sb.WriteString("  - thread\n")
	sb.WriteString("---\n")
	sb.WriteString(fmt.Sprintf("# %s\n", topic))
	sb.WriteString("\n")
	sb.WriteString("> **Context:**\n")
	sb.WriteString("\n")
	sb.WriteString("## Background\n")
	sb.WriteString("\n")
	sb.WriteString("## Investigation\n")
	sb.WriteString("\n")
	sb.WriteString("## Findings\n")
	sb.WriteString("\n")
	sb.WriteString("## Next Steps\n")
	sb.WriteString("\n")
	sb.WriteString("- [ ] \n")

	return sb.String()
}

func init() {
	threadNewCmd.Flags().StringSliceVar(&threadNewRelated, "related", nil, "Comma-separated list of related entities to link (e.g. Wiki/Services/gateway,Wiki/Environments/production)")
	threadNewCmd.Flags().StringVar(&threadNewStatus, "status", "active", "Thread status (default: active)")
	threadNewCmd.Flags().StringVar(&threadNewVault, "vault", "", "Path to vault (auto-detected if omitted)")

	threadCmd.AddCommand(threadNewCmd)
}
