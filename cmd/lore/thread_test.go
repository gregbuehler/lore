package lore

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveThreadPathRejectsTraversal(t *testing.T) {
	threadsDir := filepath.Join(t.TempDir(), "Threads")

	_, err := resolveThreadPath(threadsDir, "../outside")
	if err == nil {
		t.Fatal("expected traversal topic to be rejected")
	}
	if !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("error = %q, want escape guidance", err)
	}
}

func TestResolveThreadPathAllowsNestedThread(t *testing.T) {
	threadsDir := filepath.Join(t.TempDir(), "Threads")

	got, err := resolveThreadPath(threadsDir, "Incidents/Auth")
	if err != nil {
		t.Fatalf("resolveThreadPath returned error: %v", err)
	}
	want := filepath.Join(threadsDir, "Incidents", "Auth.md")
	if got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
}
