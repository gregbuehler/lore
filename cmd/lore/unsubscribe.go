package lore

import (
	"fmt"
	"os"

	"github.com/gbuehler/lore/internal/config"
	"github.com/gbuehler/lore/internal/index"
	"github.com/spf13/cobra"
)

var unsubscribeCmd = &cobra.Command{
	Use:   "unsubscribe <name>",
	Short: "Unsubscribe from a shared library",
	Long: `Removes a library subscription by its local name, deletes the local clone,
and updates the meta-index.

Use 'lore status' to see your subscriptions and their names.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		vaultPath, err := config.FindVault()
		if err != nil {
			return err
		}
		cfg, err := config.Load(vaultPath)
		if err != nil {
			return err
		}

		// Find subscription by name or repo URL
		found := -1
		for i, sub := range cfg.Subscriptions {
			if sub.Name == name || sub.Repo == name {
				found = i
				break
			}
		}
		if found == -1 {
			return fmt.Errorf("not subscribed to %s", name)
		}

		sub := cfg.Subscriptions[found]

		// Remove local clone
		fmt.Printf("Removing %s (%s)...\n", sub.Name, sub.Path)
		if err := os.RemoveAll(sub.Path); err != nil {
			return fmt.Errorf("removing library directory: %w", err)
		}

		// Remove from config
		cfg.Subscriptions = append(cfg.Subscriptions[:found], cfg.Subscriptions[found+1:]...)
		if err := cfg.Save(vaultPath); err != nil {
			return err
		}

		// Rebuild meta-index
		if _, err := index.BuildMetaIndex(cfg); err != nil {
			fmt.Printf("Warning: failed to rebuild meta-index: %v\n", err)
		}

		fmt.Printf("Unsubscribed from %s\n", sub.Name)
		return nil
	},
}
