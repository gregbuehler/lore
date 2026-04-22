package lore

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/gbuehler/lore/internal/config"
	"github.com/gbuehler/lore/internal/daemon"
	"github.com/spf13/cobra"
)

var syncVault string

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Pull latest changes for all subscribed libraries and reindex",
	Long: `Runs 'git pull --ff-only' on every subscribed library, then sends a
reindex request to the daemon (if it is running).

Local subscriptions (added with --local) are skipped since they have no
remote to pull from.

Examples:
  lore sync
  lore sync --vault ~/notes`,
	RunE: func(cmd *cobra.Command, args []string) error {
		vaultPath := syncVault
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

		type result struct {
			name    string
			updated bool
			output  string
			err     error
		}

		var results []result

		for _, sub := range cfg.Subscriptions {
			if strings.HasPrefix(sub.Repo, "local:") {
				fmt.Printf("  skipping %s (local)\n", sub.Name)
				continue
			}

			fmt.Printf("  pulling %s...\n", sub.Name)
			c := exec.Command("git", "-C", sub.Path, "pull", "--ff-only")
			out, err := c.CombinedOutput()
			output := strings.TrimSpace(string(out))

			r := result{name: sub.Name, output: output, err: err}
			if err == nil {
				r.updated = output != "Already up to date."
			}
			results = append(results, r)
		}

		// Print summary
		fmt.Println()
		var updated, failed int
		for _, r := range results {
			switch {
			case r.err != nil:
				fmt.Printf("  FAIL  %s: %v\n", r.name, r.err)
				if r.output != "" {
					fmt.Printf("        %s\n", r.output)
				}
				failed++
			case r.updated:
				fmt.Printf("  OK    %s (updated)\n", r.name)
				updated++
			default:
				fmt.Printf("  OK    %s (already up to date)\n", r.name)
			}
		}
		fmt.Printf("\n%d updated, %d failed.\n", updated, failed)

		// Trigger reindex if daemon is running
		client, err := daemon.Connect()
		if err != nil {
			// Daemon not running — that's fine
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
	syncCmd.Flags().StringVar(&syncVault, "vault", "", "Path to vault (auto-detected if omitted)")
}
