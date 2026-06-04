package lore

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gbuehler/lore/internal/config"
	gitpkg "github.com/gbuehler/lore/internal/git"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show vault and library status",
	Long:  `Displays subscription status, page counts, last update times, and staleness warnings.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		vaultPath, err := config.FindVault()
		if err != nil {
			return err
		}
		cfg, err := config.Load(vaultPath)
		if err != nil {
			return err
		}

		fmt.Printf("Vault: %s\n", cfg.Vault.Path)
		vaultPages := countMD(cfg.Vault.Path)
		fmt.Printf("  Pages: %d\n\n", vaultPages)

		if len(cfg.Subscriptions) == 0 {
			fmt.Println("No subscribed libraries.")
			fmt.Println("\nRun 'lore subscribe @<library> --repo <url>' to add one.")
			return nil
		}

		fmt.Println("Libraries:")
		for _, sub := range cfg.Subscriptions {
			contentPath := sub.ContentPath()
			pages := countMD(contentPath)
			age := "unknown"
			warn := ""
			isLocal := strings.HasPrefix(sub.Repo, "local:")
			isRepo := gitpkg.IsRepo(sub.Path)

			if isRepo {
				if t, err := gitpkg.LastCommitTime(sub.Path); err == nil {
					if parsed, err := time.Parse("2006-01-02 15:04:05 -0700", t); err == nil {
						age = formatAge(time.Since(parsed))
						if time.Since(parsed) > 24*time.Hour {
							warn = " ⚠"
						}
					}
				}
			} else if isLocal {
				if latest := latestModTime(contentPath); !latest.IsZero() {
					age = formatAge(time.Since(latest))
				}
				age += " (local)"
			}

			fmt.Printf("  %s: %d pages, updated %s%s\n", sub.Name, pages, age, warn)
			fmt.Printf("    Path: %s\n", sub.Path)
			if sub.ContentRoot() != "." {
				fmt.Printf("    Root: %s\n", sub.ContentRoot())
				fmt.Printf("    Content: %s\n", contentPath)
			}

			if isRepo {
				remote := gitpkg.RemoteURL(sub.Path)
				if remote != "" {
					fmt.Printf("    Repo: %s\n", remote)
				} else {
					fmt.Printf("    Repo: (git, no remote)\n")
				}

				if remote != "" {
					if behind, err := gitpkg.UpstreamStatus(sub.Path); err == nil {
						if behind == 0 {
							fmt.Printf("    Status: up to date\n")
						} else {
							fmt.Printf("    Status: %d commit(s) behind upstream\n", behind)
						}
					}
				}
			} else if isLocal {
				fmt.Printf("    Repo: local directory\n")
			}
		}

		return nil
	},
}

func latestModTime(dir string) time.Time {
	var latest time.Time
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() && strings.HasPrefix(info.Name(), ".") {
			return filepath.SkipDir
		}
		if !info.IsDir() && strings.HasSuffix(info.Name(), ".md") {
			if info.ModTime().After(latest) {
				latest = info.ModTime()
			}
		}
		return nil
	})
	return latest
}

func countMD(dir string) int {
	count := 0
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() && strings.HasPrefix(info.Name(), ".") {
			return filepath.SkipDir
		}
		if !info.IsDir() && strings.HasSuffix(info.Name(), ".md") {
			count++
		}
		return nil
	})
	return count
}

func formatAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
