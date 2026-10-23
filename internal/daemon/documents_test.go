package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gregbuehler/lore/internal/store"
)

func documentTestDaemon(t *testing.T) (*Daemon, string, string) {
	t.Helper()
	vault, library := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(vault, "same.md"), []byte("# Source"), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	state := &State{
		Store:     db,
		VaultPath: vault,
		Paths:     []string{vault, library},
		sources: []sourceRoot{
			{Source: Source{SourceID: "vault", Name: "vault", Kind: "vault", Writable: true}, root: vault},
			{Source: Source{SourceID: "library:notes", Name: "notes", Kind: "library"}, root: library},
		},
	}
	return &Daemon{state: state}, vault, library
}

func TestDocumentResolveIsScopedToSelectedSource(t *testing.T) {
	d, vault, library := documentTestDaemon(t)
	if err := os.WriteFile(filepath.Join(vault, "target.md"), []byte("# Vault Target"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(library, "target.md"), []byte("# Library Target"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(library, "library-only.md"), []byte("# Library Only"), 0o644); err != nil {
		t.Fatal(err)
	}
	resolved := d.dispatchDocumentResolve(&Request{SourceID: "vault", FromPath: "same.md", Target: "target"})
	if !resolved.OK || resolved.Resolution == nil || !resolved.Resolution.Resolved || resolved.Resolution.SourceID != "vault" || resolved.Resolution.RelPath != "target.md" {
		t.Fatalf("source-local resolution = %#v", resolved)
	}
	unresolved := d.dispatchDocumentResolve(&Request{SourceID: "vault", FromPath: "same.md", Target: "library-only"})
	if !unresolved.OK || unresolved.Resolution == nil || unresolved.Resolution.Resolved {
		t.Fatalf("cross-source fallback = %#v", unresolved)
	}
	traversal := d.dispatchDocumentResolve(&Request{SourceID: "vault", FromPath: "same.md", Target: "../target"})
	if traversal.OK || traversal.Code != "invalid_path" {
		t.Fatalf("traversal target = %#v", traversal)
	}
}
func TestDocumentResolveAllowsSafeRelativeMarkdownLinks(t *testing.T) {
	d, vault, _ := documentTestDaemon(t)
	files := map[string]string{
		"folder/current.md": "# Current",
		"folder/note.md":    "# Same Folder",
		"Sibling/note.md":   "# Parent Relative",
	}
	for rel, content := range files {
		path := filepath.Join(vault, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for target, want := range map[string]string{
		"./note.md":          "folder/note.md",
		"../Sibling/note.md": "Sibling/note.md",
	} {
		resp := d.dispatchDocumentResolve(&Request{SourceID: "vault", FromPath: "folder/current.md", Target: target})
		if !resp.OK || resp.Resolution == nil || !resp.Resolution.Resolved || resp.Resolution.RelPath != want {
			t.Errorf("resolve %q = %#v, want %q", target, resp, want)
		}
	}
}
