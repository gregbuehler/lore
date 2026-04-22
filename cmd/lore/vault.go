package lore

import (
	"github.com/spf13/cobra"
)

var vaultCmd = &cobra.Command{
	Use:   "vault",
	Short: "Vault management commands",
	Long:  `Commands for initializing, configuring, and inspecting your personal vault.`,
}

func init() {
	vaultCmd.AddCommand(initCmd)
	vaultCmd.AddCommand(contextCmd)
	vaultCmd.AddCommand(statusCmd)
	vaultCmd.AddCommand(vaultLintCmd)
}
