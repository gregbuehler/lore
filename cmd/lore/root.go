package lore

import (
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "lore",
	Short: "Institutional knowledge on your filesystem",
	Long: `lore manages personal knowledge vaults and shared team libraries.

Vaults are personal — your notes, your context, maintained by your agent.
Libraries are shared — team knowledge bases you subscribe to via git repos.
lore handles the plumbing: subscriptions, publishing, search, and maintenance.

Your editor and your agent are yours to choose. lore just makes sure the
markdown files are where they need to be.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		err := runTUI(cmd, args)
		if err != nil {
			// Can't launch TUI — fall back to printing help
			return cmd.Help()
		}
		return nil
	},
}

func Execute() error {
	return rootCmd.Execute()
}

func init() {
	rootCmd.AddCommand(vaultCmd)
	rootCmd.AddCommand(libraryCmd)
	rootCmd.AddCommand(subscribeCmd)
	rootCmd.AddCommand(unsubscribeCmd)
	rootCmd.AddCommand(updateCmd)
	rootCmd.AddCommand(syncCmd)
	rootCmd.AddCommand(publishCmd)
	rootCmd.AddCommand(searchCmd)
	rootCmd.AddCommand(discoverCmd)
	rootCmd.AddCommand(daemonCmd)
	rootCmd.AddCommand(queryCmd)
	rootCmd.AddCommand(contextQueryCmd)
	rootCmd.AddCommand(tuiCmd)
	rootCmd.AddCommand(threadCmd)
	rootCmd.AddCommand(noteCmd)
	rootCmd.AddCommand(entityCmd)
	rootCmd.AddCommand(fixLinksCmd)
}
