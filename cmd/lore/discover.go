package lore

import (
	"fmt"

	"github.com/spf13/cobra"
)

var discoverCmd = &cobra.Command{
	Use:   "discover",
	Short: "Discover available libraries from registries",
	Long: `Queries configured registries and lists available libraries.
Not yet implemented — requires a registry to query.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("Library discovery is not yet implemented.")
		fmt.Println("Use 'lore subscribe @<name> --repo <url>' to subscribe directly.")
		return nil
	},
}
