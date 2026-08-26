package store

import (
	"errors"
	"path/filepath"
	"testing"
)

func newStoreWithDocs(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})

	docs := []*Document{
		{Path: "/v/Wiki/Gateway.md", RelPath: "Wiki/Gateway.md", Title: "Gateway", Body: "needle cert rotation"},
		{Path: "/v/Wiki/Authx.md", RelPath: "Wiki/Authx.md", Title: "Authx", Body: "token issuance"},
		{Path: "/v/Wiki/Fortress.md", RelPath: "Wiki/Fortress.md", Title: "Fortress", Body: "environment notes"},
	}
	for _, d := range docs {
		if err := s.UpsertDocument(d); err != nil {
			t.Fatalf("upsert %s: %v", d.RelPath, err)
		}
	}
	return s
}

func TestVerifyFTSPassesOnHealthyIndex(t *testing.T) {
	s := newStoreWithDocs(t)
	if err := s.VerifyFTS(); err != nil {
		t.Fatalf("VerifyFTS() on healthy index = %v, want nil", err)
	}
}

func TestVerifyFTSPassesOnEmptyIndex(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	if err := s.VerifyFTS(); err != nil {
		t.Fatalf("VerifyFTS() on empty index = %v, want nil", err)
	}
}

// An index that holds every content row but has lost its index entries passes
// FTS5's integrity-check and reports no error — it just silently answers nothing.
// This is the shape of the bug where a reindex "succeeds" but queries stay broken.
func TestVerifyFTSDetectsEmptiedIndexWithPopulatedContent(t *testing.T) {
	s := newStoreWithDocs(t)

	if _, err := s.db.Exec("INSERT INTO documents_fts(documents_fts) VALUES('delete-all')"); err != nil {
		t.Fatalf("delete-all: %v", err)
	}
	// Sanity: FTS5 itself sees nothing wrong.
	if _, err := s.db.Exec("INSERT INTO documents_fts(documents_fts) VALUES('integrity-check')"); err != nil {
		t.Fatalf("integrity-check unexpectedly failed: %v", err)
	}

	err := s.VerifyFTS()
	if !errors.Is(err, ErrFTSUnhealthy) {
		t.Fatalf("VerifyFTS() = %v, want ErrFTSUnhealthy", err)
	}
	if !isFTSCorruption(err) {
		t.Fatalf("isFTSCorruption(%v) = false, want true", err)
	}
}

func TestRepairFTSIfNeededRestoresRankedQueries(t *testing.T) {
	s := newStoreWithDocs(t)

	if _, err := s.db.Exec("INSERT INTO documents_fts(documents_fts) VALUES('delete-all')"); err != nil {
		t.Fatalf("delete-all: %v", err)
	}
	if results, err := s.Search("needle", 10); err != nil || len(results) != 0 {
		t.Fatalf("Search() before repair = (%d results, %v), want 0 results and no error", len(results), err)
	}

	repaired, err := s.RepairFTSIfNeeded()
	if err != nil {
		t.Fatalf("RepairFTSIfNeeded() error = %v", err)
	}
	if !repaired {
		t.Fatal("RepairFTSIfNeeded() repaired = false, want true")
	}

	results, err := s.Search("needle", 10)
	if err != nil {
		t.Fatalf("Search() after repair error = %v", err)
	}
	if len(results) != 1 || results[0].RelPath != "Wiki/Gateway.md" {
		t.Fatalf("Search() after repair = %#v, want Wiki/Gateway.md", results)
	}

	// Repair is idempotent: a healthy index is left alone.
	repaired, err = s.RepairFTSIfNeeded()
	if err != nil {
		t.Fatalf("second RepairFTSIfNeeded() error = %v", err)
	}
	if repaired {
		t.Fatal("second RepairFTSIfNeeded() repaired = true, want false on healthy index")
	}
}

// Open must not fail when the FTS index is damaged, and must leave it usable.
func TestOpenRepairsDamagedFTS(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "index.db")
	s := func() *Store {
		s, err := Open(dbPath)
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		if err := s.UpsertDocument(&Document{
			Path: "/v/Wiki/Gateway.md", RelPath: "Wiki/Gateway.md", Title: "Gateway", Body: "needle",
		}); err != nil {
			t.Fatalf("upsert: %v", err)
		}
		if _, err := s.db.Exec("INSERT INTO documents_fts(documents_fts) VALUES('delete-all')"); err != nil {
			t.Fatalf("delete-all: %v", err)
		}
		if err := s.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}
		reopened, err := Open(dbPath)
		if err != nil {
			t.Fatalf("reopen store: %v", err)
		}
		return reopened
	}()
	t.Cleanup(func() { _ = s.Close() })

	if err := s.VerifyFTS(); err != nil {
		t.Fatalf("VerifyFTS() after Open = %v, want nil (Open should have repaired)", err)
	}
	results, err := s.Search("needle", 10)
	if err != nil {
		t.Fatalf("Search() after Open error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("Search() after Open = %d results, want 1", len(results))
	}
}

func TestSearchSelfHealsOnCorruptFTSSegments(t *testing.T) {
	s := newStoreWithDocs(t)

	// Garble the FTS5 segment/structure records. This is what makes queries fail
	// with SQLITE_CORRUPT_VTAB (267) while the documents table stays intact.
	if _, err := s.db.Exec(`UPDATE documents_fts_data SET block = randomblob(64) WHERE id > 1`); err != nil {
		t.Fatalf("corrupt segments: %v", err)
	}
	if _, err := s.db.Exec("INSERT INTO documents_fts(documents_fts) VALUES('integrity-check')"); err == nil {
		t.Fatal("integrity-check passed, expected corruption to be detectable")
	}

	results, err := s.Search("needle", 10)
	if err != nil {
		t.Fatalf("Search() error = %v, want self-healed result", err)
	}
	if len(results) != 1 || results[0].RelPath != "Wiki/Gateway.md" {
		t.Fatalf("Search() = %#v, want Wiki/Gateway.md after self-heal", results)
	}
}

func TestIsFTSCorruption(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"unrelated", errors.New("no such column: foo"), false},
		{"unhealthy sentinel", ErrFTSUnhealthy, true},
		{"malformed text", errors.New("database disk image is malformed"), true},
		{"code in text", errors.New("SQL logic error: fts5 (267)"), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isFTSCorruption(tc.err); got != tc.want {
				t.Fatalf("isFTSCorruption(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
