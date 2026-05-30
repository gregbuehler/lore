package lore

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/gbuehler/lore/internal/config"
	"github.com/gbuehler/lore/internal/daemon"
	"github.com/spf13/cobra"
)

var publishVault string
var publishMessage string
var publishDryRun bool
var publishYes bool
var publishAll bool

var publishManagedPathCandidates = []string{
	"Wiki",
	"skills",
	"sources",
	"library.yaml",
	"excerpt.md",
	"log.md",
	"CLAUDE.md",
	"AGENTS.md",
	"README.md",
	".gitignore",
}

type publishChange struct {
	name            string
	path            string
	stageableStatus string
	skippedStatus   string
	managedPaths    []string
}

type publishResult struct {
	name      string
	published bool
	skipped   bool // read-only
	noChanges bool
	dryRun    bool
	output    string
	err       error
}

var publishCmd = &cobra.Command{
	Use:   "publish [library-name]",
	Short: "Commit and push changes from subscribed libraries back to their git repos",
	Long: `Stages, commits, and pushes any local changes in each subscribed library
back to its remote git repository.

Subscriptions marked as "read-only" are skipped. If no library name is given,
all subscriptions with pending changes are published.

Examples:
  lore publish
  lore publish my-library
  lore publish --dry-run
  lore publish --all
  lore publish --yes
  lore publish --message "docs: add runbook"
  lore publish --vault ~/notes -m "update content"`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		vaultPath := publishVault
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

		// Optional filter: publish only the named library.
		var filterName string
		if len(args) == 1 {
			filterName = args[0]
		}

		var results []publishResult
		var changes []publishChange
		matched := false

		for _, sub := range cfg.Subscriptions {
			// Apply name filter if provided.
			if filterName != "" && sub.Name != filterName {
				continue
			}
			matched = true

			// Skip read-only subscriptions.
			if strings.EqualFold(sub.Access, "read-only") {
				fmt.Printf("  skipping %s (read-only)\n", sub.Name)
				results = append(results, publishResult{name: sub.Name, skipped: true})
				continue
			}

			// Check for changes via git status --porcelain.
			statusCmd := exec.Command("git", "-C", sub.Path, "status", "--porcelain")
			statusOut, statusErr := statusCmd.Output()
			if statusErr != nil {
				results = append(results, publishResult{
					name:   sub.Name,
					output: strings.TrimSpace(string(statusOut)),
					err:    fmt.Errorf("git status: %w", statusErr),
				})
				continue
			}

			if strings.TrimSpace(string(statusOut)) == "" {
				fmt.Printf("  skipping %s (nothing to publish)\n", sub.Name)
				results = append(results, publishResult{name: sub.Name, noChanges: true})
				continue
			}

			change := publishChange{
				name: sub.Name,
				path: sub.Path,
			}
			if publishAll {
				change.stageableStatus = string(statusOut)
			} else {
				managedPaths, err := publishManagedPaths(sub.Path)
				if err != nil {
					results = append(results, publishResult{
						name: sub.Name,
						err:  fmt.Errorf("finding managed paths: %w", err),
					})
					continue
				}
				change.managedPaths = managedPaths
				change.stageableStatus, change.skippedStatus = splitPublishStatus(string(statusOut), managedPaths)
				if strings.TrimSpace(change.stageableStatus) == "" {
					fmt.Printf("  skipping %s (no lore-managed changes to publish)\n", sub.Name)
					results = append(results, publishResult{name: sub.Name, noChanges: true})
					continue
				}
			}
			changes = append(changes, change)
		}

		// If a specific library was requested but never matched, report it.
		if filterName != "" && !matched {
			return fmt.Errorf("no subscription named %q", filterName)
		}

		if len(changes) > 0 {
			fmt.Print(formatPublishStatuses(changes, publishAll))

			if publishDryRun {
				for _, change := range changes {
					results = append(results, publishResult{name: change.name, dryRun: true})
				}
			} else {
				if !publishYes {
					ok, err := confirmPublish(cmd.InOrStdin(), cmd.OutOrStdout())
					if err != nil {
						return fmt.Errorf("reading confirmation: %w", err)
					}
					if !ok {
						fmt.Fprintln(cmd.OutOrStdout(), "Publish canceled.")
						return nil
					}
				}

				for _, change := range changes {
					fmt.Printf("  publishing %s...\n", change.name)
					results = append(results, publishLibrary(change.name, change.path, publishAll, change.managedPaths))
				}
			}
		}

		// Print summary.
		fmt.Println()
		var published, skipped, failed, dryRun int
		for _, r := range results {
			switch {
			case r.err != nil:
				fmt.Printf("  FAIL  %s: %v\n", r.name, r.err)
				if r.output != "" {
					fmt.Printf("        %s\n", r.output)
				}
				failed++
			case r.skipped:
				fmt.Printf("  SKIP  %s (read-only)\n", r.name)
				skipped++
			case r.noChanges:
				fmt.Printf("  OK    %s (nothing to publish)\n", r.name)
			case r.dryRun:
				fmt.Printf("  DRY   %s (would publish)\n", r.name)
				dryRun++
			case r.published:
				fmt.Printf("  OK    %s (published)\n", r.name)
				published++
			}
		}
		fmt.Printf("\n%d published, %d would publish, %d skipped (read-only), %d failed.\n", published, dryRun, skipped, failed)

		if publishDryRun {
			return nil
		}

		// Trigger reindex if daemon is running.
		client, err := daemon.Connect()
		if err != nil {
			// Daemon not running — that's fine.
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
	publishCmd.Flags().StringVar(&publishVault, "vault", "", "Path to vault (auto-detected if omitted)")
	publishCmd.Flags().StringVarP(&publishMessage, "message", "m", "lore: update content", "Commit message")
	publishCmd.Flags().BoolVar(&publishDryRun, "dry-run", false, "Show what would be published without staging, committing, or pushing")
	publishCmd.Flags().BoolVarP(&publishYes, "yes", "y", false, "Publish without interactive confirmation")
	publishCmd.Flags().BoolVar(&publishAll, "all", false, "Stage all repository changes with git add -A")
}

func formatPublishStatuses(changes []publishChange, stageAll bool) string {
	var b strings.Builder
	b.WriteString("Pending changes to publish:\n")
	if stageAll {
		b.WriteString("Staging mode: all repository changes (--all / git add -A)\n")
	} else {
		b.WriteString("Staging mode: lore-managed library content paths only (use --all for full-repository staging)\n")
	}
	for _, change := range changes {
		fmt.Fprintf(&b, "  %s (%s)\n", change.name, change.path)
		writePublishStatusLines(&b, "staged", change.stageableStatus)
		if !stageAll {
			writePublishStatusLines(&b, "not staged by lore", change.skippedStatus)
		}
	}
	return b.String()
}

func writePublishStatusLines(b *strings.Builder, label, status string) {
	if strings.TrimSpace(status) == "" {
		return
	}
	fmt.Fprintf(b, "    %s:\n", label)
	for _, line := range strings.Split(strings.TrimRight(status, "\n"), "\n") {
		if line == "" {
			continue
		}
		fmt.Fprintf(b, "      %s\n", line)
	}
}

func splitPublishStatus(status string, managedPaths []string) (stageable string, skipped string) {
	var stageableLines []string
	var skippedLines []string
	for _, line := range strings.Split(strings.TrimRight(status, "\n"), "\n") {
		if line == "" {
			continue
		}
		if publishStatusLineMatches(line, managedPaths) {
			stageableLines = append(stageableLines, line)
		} else {
			skippedLines = append(skippedLines, line)
		}
	}
	return strings.Join(stageableLines, "\n"), strings.Join(skippedLines, "\n")
}

func publishStatusLineMatches(line string, managedPaths []string) bool {
	if len(line) < 4 {
		return false
	}
	path := strings.TrimSpace(line[3:])
	if strings.Contains(path, " -> ") {
		parts := strings.Split(path, " -> ")
		path = strings.TrimSpace(parts[len(parts)-1])
	}
	path = strings.Trim(path, `"`)
	for _, managed := range managedPaths {
		if path == managed || strings.HasPrefix(path, managed+"/") {
			return true
		}
	}
	return false
}

func confirmPublish(in io.Reader, out io.Writer) (bool, error) {
	fmt.Fprint(out, "Publish these changes? [y/N] ")

	answer, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && answer == "" {
		return false, err
	}

	answer = strings.ToLower(strings.TrimSpace(answer))
	return answer == "y" || answer == "yes", nil
}

func publishLibrary(name, path string, stageAll bool, stagePaths []string) publishResult {
	if !stageAll && len(stagePaths) == 0 {
		var err error
		stagePaths, err = publishManagedPaths(path)
		if err != nil {
			return publishResult{
				name: name,
				err:  fmt.Errorf("finding managed paths: %w", err),
			}
		}
	}

	addArgs := publishStageArgs(stageAll, stagePaths)
	if addArgs == nil {
		return publishResult{
			name: name,
			err:  fmt.Errorf("no lore-managed paths found to stage (use --all to stage the full repository)"),
		}
	}

	addCmd := exec.Command("git", append([]string{"-C", path}, addArgs...)...)
	if out, err := addCmd.CombinedOutput(); err != nil {
		return publishResult{
			name:   name,
			output: strings.TrimSpace(string(out)),
			err:    fmt.Errorf("git add: %w", err),
		}
	}

	// Commit.
	commitCmd := exec.Command("git", "-C", path, "commit", "-m", publishMessage)
	if out, err := commitCmd.CombinedOutput(); err != nil {
		return publishResult{
			name:   name,
			output: strings.TrimSpace(string(out)),
			err:    fmt.Errorf("git commit: %w", err),
		}
	}

	// Push.
	pushCmd := exec.Command("git", "-C", path, "push")
	out, err := pushCmd.CombinedOutput()
	output := strings.TrimSpace(string(out))

	if err != nil {
		return publishResult{
			name:   name,
			output: output,
			err:    fmt.Errorf("git push: %w", err),
		}
	}

	return publishResult{name: name, published: true, output: output}
}

func publishStageArgs(stageAll bool, managedPaths []string) []string {
	if stageAll {
		return []string{"add", "-A"}
	}
	if len(managedPaths) == 0 {
		return nil
	}
	args := []string{"add", "-A", "--"}
	return append(args, managedPaths...)
}

func publishManagedPaths(path string) ([]string, error) {
	var paths []string
	for _, candidate := range publishManagedPathCandidates {
		if _, err := os.Stat(filepath.Join(path, candidate)); err == nil {
			paths = append(paths, candidate)
		} else if err != nil && !os.IsNotExist(err) {
			return nil, err
		} else if publishPathTracked(path, candidate) {
			paths = append(paths, candidate)
		}
	}
	return paths, nil
}

func publishPathTracked(path, candidate string) bool {
	out, err := exec.Command("git", "-C", path, "ls-files", "--", candidate).Output()
	return err == nil && strings.TrimSpace(string(out)) != ""
}
