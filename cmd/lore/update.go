package lore

import (
	"fmt"
	"strings"

	"github.com/gbuehler/lore/internal/config"
	"github.com/gbuehler/lore/internal/git"
	"github.com/gbuehler/lore/internal/index"
	"github.com/spf13/cobra"
)

var updateCmd = &cobra.Command{
	Use:   "update [name]",
	Short: "Pull latest changes for subscribed libraries",
	Long: `Runs git pull --rebase on all subscribed libraries (or a specific one
by name) and rebuilds the meta-index.`,
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

		var filter string
		if len(args) > 0 {
			filter = args[0]
		}

		updated := 0
		for _, sub := range cfg.Subscriptions {
			if filter != "" && sub.Name != filter {
				continue
			}
			if strings.HasPrefix(sub.Repo, "local:") {
				fmt.Printf("Skipping %s (local subscription)\n", sub.Name)
				continue
			}
			fmt.Printf("Updating %s...\n", sub.Name)
			if _, err := git.Pull(sub.Path); err != nil {
				fmt.Printf("  Warning: %v\n", err)
				continue
			}
			fmt.Printf("  Updated.\n")
			updated++
		}

		if filter != "" && updated == 0 {
			return fmt.Errorf("library %s not found in subscriptions", filter)
		}

		// Rebuild meta-index
		if _, err := index.BuildMetaIndex(cfg); err != nil {
			fmt.Printf("Warning: failed to rebuild meta-index: %v\n", err)
		}

		fmt.Printf("\n%d libraries updated.\n", updated)
		return nil
	},
}
