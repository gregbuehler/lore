package lore

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/gbuehler/lore/internal/config"
	"github.com/spf13/cobra"
)

var searchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Search across vault and subscribed libraries",
	Long: `Searches markdown content across the vault and all subscribed libraries
using ripgrep. Falls back to grep if rg is not installed.`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		query := strings.Join(args, " ")

		vaultPath, err := config.FindVault()
		if err != nil {
			return err
		}
		cfg, err := config.Load(vaultPath)
		if err != nil {
			return err
		}

		// Collect search paths
		paths := []string{cfg.Vault.Path}
		for _, sub := range cfg.Subscriptions {
			paths = append(paths, sub.Path)
		}

		// Prefer ripgrep, fall back to grep
		rgPath, err := exec.LookPath("rg")
		if err != nil {
			return searchWithGrep(query, paths)
		}
		return searchWithRg(rgPath, query, paths)
	},
}

func searchWithRg(rgPath, query string, paths []string) error {
	args := []string{
		"--type", "md",
		"--color", "always",
		"--heading",
		"--line-number",
		"--smart-case",
		query,
	}
	args = append(args, paths...)

	cmd := exec.Command(rgPath, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
		fmt.Println("No matches found.")
		return nil
	}
	return err
}

func searchWithGrep(query string, paths []string) error {
	args := []string{"-r", "-n", "--include=*.md", "-i", query}
	args = append(args, paths...)

	cmd := exec.Command("grep", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
		fmt.Println("No matches found.")
		return nil
	}
	return err
}
