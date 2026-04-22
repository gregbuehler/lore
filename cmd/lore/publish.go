package lore

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/gbuehler/lore/internal/config"
	"github.com/gbuehler/lore/internal/daemon"
	"github.com/spf13/cobra"
)

var publishVault string
var publishMessage string

var publishCmd = &cobra.Command{
	Use:   "publish [library-name]",
	Short: "Commit and push changes from subscribed libraries back to their git repos",
	Long: `Stages, commits, and pushes any local changes in each subscribed library
back to its remote git repository.

Subscriptions marked as "read-only" are skipped. If no library name is given,
all subscriptions with pending changes are published.

Examples:
  lore publish
  lore publish my-library
  lore publish --message "docs: add runbook"
  lore publish --vault ~/notes -m "update content"`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		vaultPath := publishVault
		if vaultPath == "" {
			var err error
			vaultPath, err = config.FindVault()
			if err != nil {
				return fmt.Errorf("specify --vault or run from within a vault: %w", err)
			}
		}

		cfg, err := config.Load(vaultPath)
		if err != nil {
			return err
		}

		if len(cfg.Subscriptions) == 0 {
			fmt.Println("No subscriptions found.")
			return nil
		}

		// Optional filter: publish only the named library.
		var filterName string
		if len(args) == 1 {
			filterName = args[0]
		}

		type result struct {
			name      string
			published bool
			skipped   bool // read-only
			noChanges bool
			output    string
			err       error
		}

		var results []result

		for _, sub := range cfg.Subscriptions {
			// Apply name filter if provided.
			if filterName != "" && sub.Name != filterName {
				continue
			}

			// Skip read-only subscriptions.
			if strings.EqualFold(sub.Access, "read-only") {
				fmt.Printf("  skipping %s (read-only)\n", sub.Name)
				results = append(results, result{name: sub.Name, skipped: true})
				continue
			}

			// Check for changes via git status --porcelain.
			statusCmd := exec.Command("git", "-C", sub.Path, "status", "--porcelain")
			statusOut, statusErr := statusCmd.Output()
			if statusErr != nil {
				results = append(results, result{
					name:   sub.Name,
					output: strings.TrimSpace(string(statusOut)),
					err:    fmt.Errorf("git status: %w", statusErr),
				})
				continue
			}

			if strings.TrimSpace(string(statusOut)) == "" {
				fmt.Printf("  skipping %s (nothing to publish)\n", sub.Name)
				results = append(results, result{name: sub.Name, noChanges: true})
				continue
			}

			fmt.Printf("  publishing %s...\n", sub.Name)

			// Stage all changes.
			addCmd := exec.Command("git", "-C", sub.Path, "add", "-A")
			if out, err := addCmd.CombinedOutput(); err != nil {
				results = append(results, result{
					name:   sub.Name,
					output: strings.TrimSpace(string(out)),
					err:    fmt.Errorf("git add: %w", err),
				})
				continue
			}

			// Commit.
			commitCmd := exec.Command("git", "-C", sub.Path, "commit", "-m", publishMessage)
			if out, err := commitCmd.CombinedOutput(); err != nil {
				results = append(results, result{
					name:   sub.Name,
					output: strings.TrimSpace(string(out)),
					err:    fmt.Errorf("git commit: %w", err),
				})
				continue
			}

			// Push.
			pushCmd := exec.Command("git", "-C", sub.Path, "push")
			out, err := pushCmd.CombinedOutput()
			output := strings.TrimSpace(string(out))

			if err != nil {
				results = append(results, result{
					name:   sub.Name,
					output: output,
					err:    fmt.Errorf("git push: %w", err),
				})
				continue
			}

			results = append(results, result{name: sub.Name, published: true, output: output})
		}

		// If a specific library was requested but never matched, report it.
		if filterName != "" && len(results) == 0 {
			return fmt.Errorf("no subscription named %q", filterName)
		}

		// Print summary.
		fmt.Println()
		var published, skipped, failed int
		for _, r := range results {
			switch {
			case r.err != nil:
				fmt.Printf("  FAIL  %s: %v\n", r.name, r.err)
				if r.output != "" {
					fmt.Printf("        %s\n", r.output)
				}
				failed++
			case r.skipped:
				fmt.Printf("  SKIP  %s (read-only)\n", r.name)
				skipped++
			case r.noChanges:
				fmt.Printf("  OK    %s (nothing to publish)\n", r.name)
			case r.published:
				fmt.Printf("  OK    %s (published)\n", r.name)
				published++
			}
		}
		fmt.Printf("\n%d published, %d skipped (read-only), %d failed.\n", published, skipped, failed)

		// Trigger reindex if daemon is running.
		client, err := daemon.Connect()
		if err != nil {
			// Daemon not running — that's fine.
			return nil
		}
		defer client.Close()

		resp, err := client.Send(&daemon.Request{Type: "reindex"})
		if err != nil {
			fmt.Printf("Warning: reindex request failed: %v\n", err)
			return nil
		}
		if !resp.OK {
			fmt.Printf("Warning: daemon reindex error: %s\n", resp.Error)
			return nil
		}
		fmt.Println("Daemon reindex triggered.")
		return nil
	},
}

func init() {
	publishCmd.Flags().StringVar(&publishVault, "vault", "", "Path to vault (auto-detected if omitted)")
	publishCmd.Flags().StringVarP(&publishMessage, "message", "m", "lore: update content", "Commit message")
}
