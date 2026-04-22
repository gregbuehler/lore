package git

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Run executes a git command in the given directory and returns stdout.
// Stderr is captured quietly to avoid noise for non-git directories.
func Run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// Clone clones a repo to the given path.
func Clone(repo, dest string) error {
	cmd := exec.Command("git", "clone", repo, dest)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git clone %s: %w", repo, err)
	}
	return nil
}

// Pull runs git pull --rebase in the given directory.
func Pull(dir string) (string, error) {
	return Run(dir, "pull", "--rebase")
}

// IsRepo checks if a directory is a git repository.
func IsRepo(dir string) bool {
	_, err := Run(dir, "rev-parse", "--git-dir")
	return err == nil
}

// LastCommitTime returns the ISO timestamp of the last commit.
func LastCommitTime(dir string) (string, error) {
	return Run(dir, "log", "-1", "--format=%ci")
}

// RemoteURL returns the fetch URL of the "origin" remote, or "" if none.
func RemoteURL(dir string) string {
	url, err := Run(dir, "remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	return url
}

// UpstreamStatus runs git fetch (quiet) and returns how many commits the
// local branch is behind its upstream. Returns (behind, error).
// A non-nil error means we couldn't determine status (no remote, no tracking branch, etc.).
func UpstreamStatus(dir string) (behind int, err error) {
	// Fetch latest refs quietly
	if _, err := Run(dir, "fetch", "--quiet"); err != nil {
		return 0, err
	}

	out, err := Run(dir, "rev-list", "--count", "HEAD..@{upstream}")
	if err != nil {
		return 0, err
	}

	var n int
	if _, err := fmt.Sscanf(out, "%d", &n); err != nil {
		return 0, err
	}
	return n, nil
}
