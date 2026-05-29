package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareSocketPathRejectsNonSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.sock")
	if err := os.WriteFile(path, []byte("not a socket"), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	err := prepareSocketPath(path)
	if err == nil {
		t.Fatal("expected non-socket rejection")
	}
	if !strings.Contains(err.Error(), "refusing to remove non-socket") {
		t.Fatalf("error = %v", err)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("non-socket was removed: %v", statErr)
	}
}
