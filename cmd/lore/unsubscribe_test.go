package lore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gregbuehler/lore/internal/config"
)

func TestRemoveSubscriptionFilesSkipsLocalSubscription(t *testing.T) {
	managedDir := t.TempDir()
	localDir := t.TempDir()
	mustWriteFile(t, filepath.Join(localDir, "note.md"))

	msg, err := removeSubscriptionFiles(config.SubscriptionConfig{
		Name: "local-lib",
		Repo: "local:" + localDir,
		Path: localDir,
	}, managedDir)
	if err != nil {
		t.Fatalf("removeSubscriptionFiles returned error: %v", err)
	}

	if _, err := os.Stat(localDir); err != nil {
		t.Fatalf("local subscription directory was removed: %v", err)
	}
	if !strings.Contains(msg, "local subscription") {
		t.Fatalf("message = %q, want local subscription skip message", msg)
	}
}

func TestRemoveSubscriptionFilesRejectsPathOutsideManagedLibrariesDir(t *testing.T) {
	managedDir := t.TempDir()
	outsideDir := t.TempDir()
	mustWriteFile(t, filepath.Join(outsideDir, "note.md"))

	msg, err := removeSubscriptionFiles(config.SubscriptionConfig{
		Name: "remote-lib",
		Repo: "git@example.com:team/remote-lib.git",
		Path: outsideDir,
	}, managedDir)
	if err != nil {
		t.Fatalf("removeSubscriptionFiles returned error: %v", err)
	}

	if _, err := os.Stat(outsideDir); err != nil {
		t.Fatalf("outside subscription directory was removed: %v", err)
	}
	if !strings.Contains(msg, "outside managed libraries directory") {
		t.Fatalf("message = %q, want outside managed directory skip message", msg)
	}
}

func TestRemoveSubscriptionFilesDeletesPathInsideManagedLibrariesDir(t *testing.T) {
	managedDir := t.TempDir()
	cloneDir := filepath.Join(managedDir, "remote-lib")
	mustWriteFile(t, filepath.Join(cloneDir, "note.md"))

	msg, err := removeSubscriptionFiles(config.SubscriptionConfig{
		Name: "remote-lib",
		Repo: "git@example.com:team/remote-lib.git",
		Path: cloneDir,
	}, managedDir)
	if err != nil {
		t.Fatalf("removeSubscriptionFiles returned error: %v", err)
	}

	if _, err := os.Stat(cloneDir); !os.IsNotExist(err) {
		t.Fatalf("clone directory still exists or stat failed unexpectedly: %v", err)
	}
	if !strings.Contains(msg, "Removed") {
		t.Fatalf("message = %q, want deletion message", msg)
	}
}

func TestRemoveSubscriptionFilesRejectsManagedLibrariesDirItself(t *testing.T) {
	managedDir := t.TempDir()
	mustWriteFile(t, filepath.Join(managedDir, "remote-lib", "note.md"))

	msg, err := removeSubscriptionFiles(config.SubscriptionConfig{
		Name: "remote-lib",
		Repo: "git@example.com:team/remote-lib.git",
		Path: managedDir,
	}, managedDir)
	if err != nil {
		t.Fatalf("removeSubscriptionFiles returned error: %v", err)
	}

	if _, err := os.Stat(managedDir); err != nil {
		t.Fatalf("managed libraries directory was removed: %v", err)
	}
	if !strings.Contains(msg, "outside managed libraries directory") {
		t.Fatalf("message = %q, want outside managed directory skip message", msg)
	}
}

func TestRemoveSubscriptionFilesRejectsResolvedPathOutsideManagedLibrariesDir(t *testing.T) {
	managedDir := t.TempDir()
	outsideDir := t.TempDir()
	mustWriteFile(t, filepath.Join(outsideDir, "note.md"))

	linkPath := filepath.Join(managedDir, "remote-lib")
	if err := os.Symlink(outsideDir, linkPath); err != nil {
		t.Fatalf("creating symlink: %v", err)
	}

	msg, err := removeSubscriptionFiles(config.SubscriptionConfig{
		Name: "remote-lib",
		Repo: "git@example.com:team/remote-lib.git",
		Path: linkPath,
	}, managedDir)
	if err != nil {
		t.Fatalf("removeSubscriptionFiles returned error: %v", err)
	}

	if _, err := os.Lstat(linkPath); err != nil {
		t.Fatalf("symlink inside managed directory was removed: %v", err)
	}
	if _, err := os.Stat(outsideDir); err != nil {
		t.Fatalf("outside target directory was removed: %v", err)
	}
	if !strings.Contains(msg, "outside managed libraries directory") {
		t.Fatalf("message = %q, want outside managed directory skip message", msg)
	}
}

func mustWriteFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("creating test dir: %v", err)
	}
	if err := os.WriteFile(path, []byte("test"), 0o644); err != nil {
		t.Fatalf("writing test file: %v", err)
	}
}
