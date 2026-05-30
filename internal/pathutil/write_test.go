package pathutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAtomicWriteFileWritesContentAndMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "generated.md")

	if err := AtomicWriteFile(path, []byte("# Generated\n"), 0o640); err != nil {
		t.Fatalf("AtomicWriteFile returned error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading output: %v", err)
	}
	if string(data) != "# Generated\n" {
		t.Fatalf("content = %q", data)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stating output: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o640 {
		t.Fatalf("mode = %o, want 640", got)
	}
}

func TestAtomicWriteFileReplacesExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "generated.md")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatalf("writing old file: %v", err)
	}

	if err := AtomicWriteFile(path, []byte("new"), 0o644); err != nil {
		t.Fatalf("AtomicWriteFile returned error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading output: %v", err)
	}
	if string(data) != "new" {
		t.Fatalf("content = %q", data)
	}
}

func TestAtomicWriteFileSyncsParentDirectoryAfterRename(t *testing.T) {
	path := filepath.Join(t.TempDir(), "generated.md")
	calls := 0
	oldSyncParentDir := syncParentDir
	syncParentDir = func(dir string) error {
		calls++
		if dir != filepath.Dir(path) {
			t.Fatalf("sync parent dir = %q, want %q", dir, filepath.Dir(path))
		}
		return nil
	}
	t.Cleanup(func() {
		syncParentDir = oldSyncParentDir
	})

	if err := AtomicWriteFile(path, []byte("content"), 0o644); err != nil {
		t.Fatalf("AtomicWriteFile returned error: %v", err)
	}

	if calls != 1 {
		t.Fatalf("sync parent dir calls = %d, want 1", calls)
	}
}
