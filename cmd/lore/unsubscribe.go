package lore

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gregbuehler/lore/internal/config"
	"github.com/gregbuehler/lore/internal/index"
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

		msg, err := removeSubscriptionFiles(sub, config.LibrariesDir())
		if err != nil {
			return err
		}
		if msg != "" {
			fmt.Println(msg)
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

func removeSubscriptionFiles(sub config.SubscriptionConfig, managedDir string) (string, error) {
	if strings.HasPrefix(sub.Repo, "local:") {
		return fmt.Sprintf("Keeping %s (%s): local subscription", sub.Name, sub.Path), nil
	}

	absManaged, err := filepath.Abs(managedDir)
	if err != nil {
		return "", fmt.Errorf("resolving managed libraries directory: %w", err)
	}
	absPath, err := filepath.Abs(sub.Path)
	if err != nil {
		return "", fmt.Errorf("resolving subscription path: %w", err)
	}
	checkPath := absPath
	if resolved, err := filepath.EvalSymlinks(absPath); err == nil {
		checkPath = resolved
	}

	rel, err := filepath.Rel(absManaged, checkPath)
	if err != nil {
		return "", fmt.Errorf("checking subscription path: %w", err)
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Sprintf("Keeping %s (%s): outside managed libraries directory", sub.Name, sub.Path), nil
	}

	if err := os.RemoveAll(absPath); err != nil {
		return "", fmt.Errorf("removing library directory: %w", err)
	}
	return fmt.Sprintf("Removed %s (%s)", sub.Name, absPath), nil
}
