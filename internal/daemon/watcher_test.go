package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

func TestHandleCreatedDirectoryWatchesNestedSubdirectories(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "Wiki", "Services")
	child := filepath.Join(parent, "Nested")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatalf("mkdir nested tree: %v", err)
	}

	fw, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatalf("new watcher: %v", err)
	}
	defer fw.Close()

	w := &Watcher{fw: fw}
	count := w.handleCreatedDirectory(parent)
	if count != 2 {
		t.Fatalf("watched directory count = %d, want 2", count)
	}
}

func TestFlushCoalescesMultipleReadyPathsIntoOneReindex(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first.md")
	second := filepath.Join(dir, "second.md")
	if err := os.WriteFile(first, []byte("# First\n"), 0o644); err != nil {
		t.Fatalf("write first: %v", err)
	}
	if err := os.WriteFile(second, []byte("# Second\n"), 0o644); err != nil {
		t.Fatalf("write second: %v", err)
	}

	calls := 0
	w := &Watcher{
		pending: map[string]time.Time{
			first:  time.Now().Add(-time.Second),
			second: time.Now().Add(-time.Second),
		},
		debounce:     time.Millisecond,
		rebuildIndex: func() error { calls++; return nil },
	}

	w.flush()

	if calls != 1 {
		t.Fatalf("reindex calls = %d, want 1", calls)
	}
}

func TestFlushCoalescesMixedReadyPathsIntoOneRebuild(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "existing.md")
	deleted := filepath.Join(dir, "deleted.md")
	if err := os.WriteFile(existing, []byte("# Existing\n"), 0o644); err != nil {
		t.Fatalf("write existing: %v", err)
	}

	rebuilds := 0
	w := &Watcher{
		pending: map[string]time.Time{
			existing: time.Now().Add(-time.Second),
			deleted:  time.Now().Add(-time.Second),
		},
		debounce:     time.Millisecond,
		rebuildIndex: func() error { rebuilds++; return nil },
	}

	w.flush()

	if rebuilds != 1 {
		t.Fatalf("rebuild calls = %d, want 1", rebuilds)
	}
}
