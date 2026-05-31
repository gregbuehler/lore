package lore

import (
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/gregbuehler/lore/internal/config"
	"github.com/gregbuehler/lore/internal/tui"
)

var tuiCmd = &cobra.Command{
	Use:   "ui",
	Short: "Launch the interactive TUI",
	Long: `Opens the interactive terminal interface for navigating your vault.

Tabs: Search, Graph, Status, Libraries, Health
The daemon will be auto-started if a vault is found.`,
	RunE: runTUI,
}

func runTUI(cmd *cobra.Command, args []string) error {
	client, err := connectDaemonForCurrentVault()
	if err != nil {
		return fmt.Errorf("cannot connect to daemon: %w\nRun 'lore daemon start' first", err)
	}

	vaultPath := resolveTUIVaultPath()
	m := tui.New(client, vaultPath)
	p := tea.NewProgram(m, tea.WithAltScreen())

	if _, err := p.Run(); err != nil {
		client.Close()
		return err
	}

	client.Close()
	return nil
}

// resolveTUIVaultPath finds the vault path for auto-starting the daemon.
// Checks: LORE_VAULT env, then walks up from cwd looking for .lore/config.yaml.
func resolveTUIVaultPath() string {
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
