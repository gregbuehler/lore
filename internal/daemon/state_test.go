package daemon

import (
	"database/sql"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/gregbuehler/lore/internal/store"
	_ "modernc.org/sqlite"
)

// damageFTS garbles the FTS5 segment records through a second connection,
// reproducing the SQLITE_CORRUPT_VTAB (267) state that leaves the documents
// table intact while ranked queries fail.
func damageFTS(t *testing.T, dbPath string) error {
	t.Helper()
	raw, err := sql.Open("sqlite", dbPath+"?_busy_timeout=5000")
	if err != nil {
		return err
	}
	defer raw.Close()
	_, err = raw.Exec(`UPDATE documents_fts_data SET block = randomblob(64) WHERE id > 1`)
	return err
}

func TestRebuildIndexForPathClearsOutgoingEdgesWhenWikilinksRemoved(t *testing.T) {
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
	if err := state.RebuildIndexForPath(sourcePath); err != nil {
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
	if err := state.RebuildIndexForPath(sourcePath); err != nil {
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

func TestRebuildIndexForPathRefreshesResolverForNewTargets(t *testing.T) {
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
		t.Fatalf("write source: %v", err)
	}
	if err := state.RebuildIndexForPath(sourcePath); err != nil {
		t.Fatalf("index source: %v", err)
	}

	targetPath := filepath.Join(vault, "Wiki", "Services", "Target.md")
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		t.Fatalf("mkdir target dir: %v", err)
	}
	if err := os.WriteFile(targetPath, []byte("# Target\n"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := state.RebuildIndexForPath(targetPath); err != nil {
		t.Fatalf("index target: %v", err)
	}

	backlinks, err := db.Backlinks("Wiki/Services/Target", "")
	if err != nil {
		t.Fatalf("query backlinks: %v", err)
	}
	if len(backlinks) != 1 || backlinks[0].RelPath != "Source" {
		t.Fatalf("backlinks = %#v, want Source linking to resolved target", backlinks)
	}
}

func TestStateExposesSharedStoreLock(t *testing.T) {
	state := &State{}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		state.mu.RLock()
		state.mu.RUnlock()
	}()
	go func() {
		defer wg.Done()
		state.mu.Lock()
		state.mu.Unlock()
	}()
	wg.Wait()
}

func TestRootForRequiresPathBoundary(t *testing.T) {
	root := filepath.Join(t.TempDir(), "vault")
	state := &State{Paths: []string{root}}

	if got := state.rootFor(filepath.Join(root, "Wiki", "Page.md")); got != root {
		t.Fatalf("rootFor path inside root = %q, want %q", got, root)
	}

	if got := state.rootFor(root + "-other/Wiki/Page.md"); got != "" {
		t.Fatalf("rootFor sibling prefix path = %q, want empty", got)
	}
}

// A reindex must repair a damaged FTS index, not just repopulate documents.
// Content rows alone are not enough: FTS5 can hold every row and still fail the
// ranked traversal that 'lore query' uses, which is why buildIndexLocked ends
// with an FTS-level rebuild.
func TestBuildIndexRepairsDamagedFTSIndex(t *testing.T) {
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

	state := &State{Store: db, VaultPath: vault, Paths: []string{vault}}

	notePath := filepath.Join(vault, "Gateway.md")
	if err := os.WriteFile(notePath, []byte("# Gateway\n\nneedle cert rotation\n"), 0o644); err != nil {
		t.Fatalf("write note: %v", err)
	}
	if err := state.BuildIndex(); err != nil {
		t.Fatalf("initial BuildIndex: %v", err)
	}
	if results, err := db.Search("needle", 10); err != nil || len(results) != 1 {
		t.Fatalf("Search() after build = (%d results, %v), want 1 result", len(results), err)
	}

	if err := damageFTS(t, dbPath); err != nil {
		t.Fatalf("damage FTS: %v", err)
	}

	if err := state.BuildIndex(); err != nil {
		t.Fatalf("BuildIndex after damage: %v", err)
	}
	if err := db.VerifyFTS(); err != nil {
		t.Fatalf("VerifyFTS() after reindex = %v, want nil", err)
	}
	results, err := db.Search("needle", 10)
	if err != nil {
		t.Fatalf("Search() after reindex error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("Search() after reindex = %d results, want 1", len(results))
	}
}
