package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestSearchReturnsScanError(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close db: %v", err)
		}
	})

	execSQL(t, db, `CREATE TABLE documents (
		path TEXT,
		rel_path TEXT,
		title TEXT,
		entity_type TEXT,
		aliases TEXT,
		tags TEXT,
		body TEXT,
		abstract TEXT
	)`)
	execSQL(t, db, `CREATE VIRTUAL TABLE documents_fts USING fts5(
		title,
		aliases,
		tags,
		body,
		content=documents,
		content_rowid=rowid
	)`)
	execSQL(t, db, `INSERT INTO documents (rowid, path, rel_path, title, entity_type, aliases, tags, body, abstract)
		VALUES (1, '/tmp/doc.md', 'doc.md', 'Doc', '', '', '', 'needle', NULL)`)
	execSQL(t, db, `INSERT INTO documents_fts (rowid, title, aliases, tags, body)
		VALUES (1, 'Doc', '', '', 'needle')`)

	s := &Store{db: db}
	if _, err := s.Search("needle", 10); err == nil {
		t.Fatal("Search() error = nil, want scan error")
	}
}

func TestSearchAndHealthCheckNormalPath(t *testing.T) {
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
		{
			Path:    "/vault/Wiki/Source.md",
			RelPath: "Wiki/Source.md",
			Title:   "Source",
			Body:    "needle source",
		},
		{
			Path:        "/vault/Wiki/Stale.md",
			RelPath:     "Wiki/Stale.md",
			Title:       "Stale",
			EntityType:  "project",
			LastUpdated: "2000-01-01",
			Body:        "stale project",
		},
	}
	for _, doc := range docs {
		if err := s.UpsertDocument(doc); err != nil {
			t.Fatalf("upsert document %s: %v", doc.RelPath, err)
		}
	}
	if err := s.SetEdges("Wiki/Source", []Edge{
		{From: "Wiki/Source", To: "Wiki/Missing", Type: "mentions"},
	}); err != nil {
		t.Fatalf("set source edges: %v", err)
	}
	if err := s.SetEdges("Wiki/Stale", []Edge{
		{From: "Wiki/Stale", To: "Wiki/Source", Type: "mentions"},
	}); err != nil {
		t.Fatalf("set stale edges: %v", err)
	}

	results, err := s.Search("needle", 10)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(results) != 1 || results[0].RelPath != "Wiki/Source.md" {
		t.Fatalf("Search() results = %#v, want Wiki/Source.md", results)
	}

	issues, err := s.HealthCheck()
	if err != nil {
		t.Fatalf("HealthCheck() error = %v", err)
	}
	if !hasHealthIssue(issues, "broken_link", "Wiki/Source") {
		t.Fatalf("HealthCheck() issues = %#v, want broken link from Wiki/Source", issues)
	}
	if !hasHealthIssue(issues, "stale", "Wiki/Stale.md") {
		t.Fatalf("HealthCheck() issues = %#v, want stale Wiki/Stale.md", issues)
	}
}

func execSQL(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

func hasHealthIssue(issues []HealthIssue, issueType, relPath string) bool {
	for _, issue := range issues {
		if issue.IssueType == issueType && issue.RelPath == relPath {
			return true
		}
	}
	return false
}
