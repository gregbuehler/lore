package lore

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gregbuehler/lore/internal/config"
	"github.com/gregbuehler/lore/internal/git"
	"github.com/gregbuehler/lore/internal/index"
	"github.com/spf13/cobra"
)

var subscribeName string
var subscribeLocal bool
var subscribeRoot string

var subscribeCmd = &cobra.Command{
	Use:   "subscribe <repo-url | path>",
	Short: "Subscribe to a shared library",
	Long: `Clones a library's git repo to the local libraries directory and adds it
to the vault configuration. The library's content becomes available as
local markdown files for your agent to read.

The argument can be a full git URL, an org/repo shorthand that expands
using the configured default_host, or a local path with --local. Use --root
to subscribe to a subdirectory within the checkout, such as docs.

An optional --name flag sets a local alias; if omitted, the alias is
derived from the repo name or directory name.

Examples:
  lore subscribe git@git.example.com:team/wiki-library.git
  lore subscribe git@github.com:gregbuehler/citizen.git --root docs
  lore subscribe team/wiki-library                     # shorthand (requires default_host)
  lore subscribe team/wiki-library --name my-library
  lore subscribe ~/Documents/lore/services --local     # local directory`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ref := args[0]

		vaultPath, err := config.FindVault()
		if err != nil {
			return err
		}
		cfg, err := config.Load(vaultPath)
		if err != nil {
			return err
		}

		var name, repo, dest string

		if subscribeLocal {
			// Local directory subscription -- link directly, no clone
			abs, err := filepath.Abs(ref)
			if err != nil {
				return fmt.Errorf("resolving path: %w", err)
			}
			info, err := os.Stat(abs)
			if err != nil || !info.IsDir() {
				return fmt.Errorf("%s does not exist or is not a directory", abs)
			}

			name = subscribeName
			if name == "" {
				name = filepath.Base(abs)
			}
			repo = "local:" + abs
			dest = abs
		} else {
			// Remote git subscription -- resolve and clone
			repo, err = cfg.ResolveRepo(ref)
			if err != nil {
				return err
			}

			name = subscribeName
			if name == "" {
				name = repoToName(repo)
			}

			dest = filepath.Join(config.LibrariesDir(), name)
		}

		// Check if already subscribed
		for _, sub := range cfg.Subscriptions {
			if sub.Repo == repo || sub.Name == name {
				return fmt.Errorf("already subscribed to %s (%s)", name, repo)
			}
		}

		if !subscribeLocal {
			fmt.Printf("Cloning %s as %s...\n", repo, name)
			if err := git.Clone(repo, dest); err != nil {
				return err
			}
		} else {
			fmt.Printf("Linking local library %s as %s...\n", dest, name)
		}

		root := strings.TrimSpace(subscribeRoot)
		if root == "" {
			root = "."
		}
		root = filepath.Clean(root)
		contentPath := dest
		if root != "." {
			if filepath.IsAbs(root) {
				return fmt.Errorf("--root must be relative to the subscription path")
			}
			contentPath = filepath.Join(dest, root)
		}
		info, err := os.Stat(contentPath)
		if err != nil || !info.IsDir() {
			return fmt.Errorf("subscription root %s does not exist or is not a directory", contentPath)
		}

		// Add to config
		cfg.Subscriptions = append(cfg.Subscriptions, config.SubscriptionConfig{
			Name:   name,
			Repo:   repo,
			Path:   dest,
			Root:   root,
			Access: "read-write",
		})
		if err := cfg.Save(vaultPath); err != nil {
			return err
		}

		// Rebuild meta-index
		if _, err := index.BuildMetaIndex(cfg); err != nil {
			fmt.Printf("Warning: failed to rebuild meta-index: %v\n", err)
		}

		fmt.Printf("Subscribed to %s\n", name)
		return nil
	},
}

// repoToName extracts a short name from a git URL.
// e.g. "git@git.example.com:team/wiki-library.git" → "wiki-library"
// e.g. "https://git.example.com/team/services.git" → "services"
func repoToName(repo string) string {
	// Handle SSH-style URLs: git@host:org/repo.git
	if idx := strings.LastIndex(repo, "/"); idx >= 0 {
		name := repo[idx+1:]
		name = strings.TrimSuffix(name, ".git")
		return name
	}
	// Handle colon-separated SSH: git@host:org/repo.git
	if idx := strings.LastIndex(repo, ":"); idx >= 0 {
		name := repo[idx+1:]
		if slashIdx := strings.LastIndex(name, "/"); slashIdx >= 0 {
			name = name[slashIdx+1:]
		}
		name = strings.TrimSuffix(name, ".git")
		return name
	}
	return strings.TrimSuffix(repo, ".git")
}

func init() {
	subscribeCmd.Flags().StringVar(&subscribeName, "name", "", "Local alias for the library (default: derived from repo/path)")
	subscribeCmd.Flags().BoolVar(&subscribeLocal, "local", false, "Subscribe to a local directory instead of cloning a git repo")
	subscribeCmd.Flags().StringVar(&subscribeRoot, "root", ".", "Root directory within the subscription to index")
}
