package lore

import (
	"fmt"
	"sort"
	"strings"

	"github.com/gregbuehler/lore/internal/pathutil"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var docsCmd = &cobra.Command{
	Use:   "docs",
	Short: "Generate project documentation",
}

var docsCommandsCmd = &cobra.Command{
	Use:   "commands [output-path]",
	Short: "Generate the command reference from the CLI tree",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		content := generateCommandReference(rootCmd)
		if len(args) == 0 {
			fmt.Print(content)
			return nil
		}
		if err := pathutil.AtomicWriteFile(args[0], []byte(content), 0o644); err != nil {
			return fmt.Errorf("writing command reference: %w", err)
		}
		return nil
	},
}

func generateCommandReference(root *cobra.Command) string {
	var commands []*cobra.Command
	seen := map[string]bool{}
	walkCommands(root, func(cmd *cobra.Command) {
		path := commandUseLine(cmd)
		if seen[path] {
			return
		}
		seen[path] = true
		commands = append(commands, cmd)
	})
	sort.Slice(commands, func(i, j int) bool {
		return commands[i].CommandPath() < commands[j].CommandPath()
	})

	var b strings.Builder
	b.WriteString("# lore Command Reference\n\n")
	b.WriteString("Generated from the Cobra command tree. Run `lore docs commands docs/commands.md` to refresh.\n\n")
	for _, cmd := range commands {
		fmt.Fprintf(&b, "### `%s`\n\n", commandUseLine(cmd))
		if cmd.Short != "" {
			b.WriteString(cmd.Short)
			b.WriteString("\n\n")
		}
		if cmd.HasAvailableLocalFlags() {
			b.WriteString("Flags:\n\n")
			cmd.LocalFlags().VisitAll(func(flag *pflag.Flag) {
				shorthand := ""
				if flag.Shorthand != "" {
					shorthand = fmt.Sprintf(", `-%s`", flag.Shorthand)
				}
				fmt.Fprintf(&b, "- `--%s`%s: %s\n", flag.Name, shorthand, flag.Usage)
			})
			b.WriteString("\n")
		}
	}
	return b.String()
}

func walkCommands(cmd *cobra.Command, visit func(*cobra.Command)) {
	if cmd.Hidden || cmd.Name() == "completion" {
		return
	}
	visit(cmd)
	for _, child := range cmd.Commands() {
		walkCommands(child, visit)
	}
}

func commandUseLine(cmd *cobra.Command) string {
	if cmd.Parent() == nil {
		return cmd.UseLine()
	}
	path := cmd.CommandPath()
	if strings.Contains(cmd.Use, " ") {
		parts := strings.SplitN(cmd.Use, " ", 2)
		return path + " " + parts[1]
	}
	return path
}

func init() {
	docsCmd.AddCommand(docsCommandsCmd)
}
