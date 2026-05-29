package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gbuehler/lore/internal/store"
)

func TestIndexFileClearsOutgoingEdgesWhenWikilinksRemoved(t *testing.T) {
	vault := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "index.db")

	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})

	state := &State{
		Store:     db,
		VaultPath: vault,
		Paths:     []string{vault},
	}

	sourcePath := filepath.Join(vault, "Source.md")
	if err := os.WriteFile(sourcePath, []byte("# Source\n\nSee [[Target]].\n"), 0o644); err != nil {
		t.Fatalf("write source with wikilink: %v", err)
	}
	if err := state.IndexFile(sourcePath); err != nil {
		t.Fatalf("index source with wikilink: %v", err)
	}

	neighbors, err := db.Neighbors("Source", "", 1)
	if err != nil {
		t.Fatalf("query neighbors after adding wikilink: %v", err)
	}
	if len(neighbors) != 1 || neighbors[0].RelPath != "Target" {
		t.Fatalf("neighbors after adding wikilink = %#v, want one edge to Target", neighbors)
	}

	if err := os.WriteFile(sourcePath, []byte("# Source\n\nNo outgoing links remain.\n"), 0o644); err != nil {
		t.Fatalf("write source without wikilink: %v", err)
	}
	if err := state.IndexFile(sourcePath); err != nil {
		t.Fatalf("index source without wikilink: %v", err)
	}

	neighbors, err = db.Neighbors("Source", "", 1)
	if err != nil {
		t.Fatalf("query neighbors after removing wikilink: %v", err)
	}
	if len(neighbors) != 0 {
		t.Fatalf("neighbors after removing last wikilink = %#v, want no outgoing edges", neighbors)
	}
}
