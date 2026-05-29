package store

import (
	"database/sql"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

// Store is the SQLite-backed search index and graph for lore.
// It is a derived cache — the markdown files on disk are the source of truth.
// Delete the DB and rebuild from files at any time.
type Store struct {
	db   *sql.DB
	path string
}

// DefaultPath returns the default DB location.
func DefaultPath() string {
	if p := os.Getenv("LORE_DB"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "lore", "index.db")
}

// DefaultPathForVault returns the default DB location for a specific vault.
func DefaultPathForVault(vaultPath string) string {
	if p := os.Getenv("LORE_DB"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "lore", "vaults", vaultID(vaultPath), "index.db")
}

// Open opens (or creates) the SQLite store at the given path.
func Open(dbPath string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("creating db dir: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_synchronous=NORMAL")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	s := &Store{db: db, path: dbPath}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// OpenForVault opens a vault-scoped store and records the vault identity.
func OpenForVault(dbPath, vaultPath string) (*Store, error) {
	s, err := Open(dbPath)
	if err != nil {
		return nil, err
	}
	if err := s.EnsureVault(vaultPath); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error {
	return s.db.Close()
}

// Path returns the database file path.
func (s *Store) Path() string {
	return s.path
}

func (s *Store) migrate() error {
	migrations := []string{
		// Documents table: one row per markdown file
		`CREATE TABLE IF NOT EXISTS documents (
			path        TEXT PRIMARY KEY,
			rel_path    TEXT NOT NULL,
			title       TEXT NOT NULL,
			entity_type TEXT NOT NULL DEFAULT '',
			aliases     TEXT NOT NULL DEFAULT '',
			tags        TEXT NOT NULL DEFAULT '',
			last_updated TEXT NOT NULL DEFAULT '',
			status      TEXT NOT NULL DEFAULT '',
			body        TEXT NOT NULL DEFAULT '',
			root        TEXT NOT NULL DEFAULT '',
			abstract    TEXT NOT NULL DEFAULT '',
			indexed_at  TEXT NOT NULL DEFAULT (datetime('now'))
		)`,

		// FTS5 virtual table for full-text search with BM25 ranking
		`CREATE VIRTUAL TABLE IF NOT EXISTS documents_fts USING fts5(
			title,
			aliases,
			tags,
			body,
			content=documents,
			content_rowid=rowid,
			tokenize='unicode61 remove_diacritics 2'
		)`,

		// Triggers to keep FTS in sync with documents table
		`CREATE TRIGGER IF NOT EXISTS documents_ai AFTER INSERT ON documents BEGIN
			INSERT INTO documents_fts(rowid, title, aliases, tags, body)
			VALUES (new.rowid, new.title, new.aliases, new.tags, new.body);
		END`,

		`CREATE TRIGGER IF NOT EXISTS documents_ad AFTER DELETE ON documents BEGIN
			INSERT INTO documents_fts(documents_fts, rowid, title, aliases, tags, body)
			VALUES('delete', old.rowid, old.title, old.aliases, old.tags, old.body);
		END`,

		`CREATE TRIGGER IF NOT EXISTS documents_au AFTER UPDATE ON documents BEGIN
			INSERT INTO documents_fts(documents_fts, rowid, title, aliases, tags, body)
			VALUES('delete', old.rowid, old.title, old.aliases, old.tags, old.body);
			INSERT INTO documents_fts(rowid, title, aliases, tags, body)
			VALUES (new.rowid, new.title, new.aliases, new.tags, new.body);
		END`,

		// Edges table: typed directed relationships
		`CREATE TABLE IF NOT EXISTS edges (
			from_path TEXT NOT NULL,
			to_path   TEXT NOT NULL,
			edge_type TEXT NOT NULL,
			source    TEXT NOT NULL DEFAULT '',
			PRIMARY KEY (from_path, to_path, edge_type)
		)`,

		// Indexes for fast graph traversal
		`CREATE INDEX IF NOT EXISTS idx_edges_from ON edges(from_path)`,
		`CREATE INDEX IF NOT EXISTS idx_edges_to ON edges(to_path)`,
		`CREATE INDEX IF NOT EXISTS idx_edges_type ON edges(edge_type)`,
		`CREATE INDEX IF NOT EXISTS idx_documents_type ON documents(entity_type)`,
		`CREATE INDEX IF NOT EXISTS idx_documents_root ON documents(root)`,
		`CREATE TABLE IF NOT EXISTS metadata (
			key   TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
	}

	for _, m := range migrations {
		if _, err := s.db.Exec(m); err != nil {
			return fmt.Errorf("migration failed: %w\n  SQL: %s", err, m)
		}
	}
	return nil
}

// EnsureVault records and validates which vault owns this store.
func (s *Store) EnsureVault(vaultPath string) error {
	vaultPath = normalizePath(vaultPath)
	if vaultPath == "" {
		return fmt.Errorf("vault path is required")
	}
	var existing string
	err := s.db.QueryRow("SELECT value FROM metadata WHERE key = 'vault_path'").Scan(&existing)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("reading store vault metadata: %w", err)
	}
	if existing != "" && normalizePath(existing) != vaultPath {
		return fmt.Errorf("store belongs to vault %s, not %s", existing, vaultPath)
	}
	_, err = s.db.Exec("INSERT OR REPLACE INTO metadata (key, value) VALUES ('vault_path', ?)", vaultPath)
	return err
}

func vaultID(vaultPath string) string {
	vaultPath = normalizePath(vaultPath)
	h := fnv.New64a()
	_, _ = h.Write([]byte(vaultPath))
	return fmt.Sprintf("%x", h.Sum64())
}

func normalizePath(path string) string {
	if path == "" {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return abs
}

// Document is the data model for a stored document.
type Document struct {
	Path        string
	RelPath     string
	Title       string
	EntityType  string
	Aliases     []string
	Tags        []string
	LastUpdated string
	Status      string
	Body        string
	Root        string
	Abstract    string // computed 1-2 sentence summary
}

// Edge is the data model for a stored edge.
type Edge struct {
	From   string
	To     string
	Type   string
	Source string
}

// UpsertDocument inserts or replaces a document in the store.
func (s *Store) UpsertDocument(doc *Document) error {
	_, err := s.db.Exec(`
		INSERT OR REPLACE INTO documents (path, rel_path, title, entity_type, aliases, tags, last_updated, status, body, root, abstract, indexed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))`,
		doc.Path,
		doc.RelPath,
		doc.Title,
		doc.EntityType,
		strings.Join(doc.Aliases, ", "),
		strings.Join(doc.Tags, ", "),
		doc.LastUpdated,
		doc.Status,
		doc.Body,
		doc.Root,
		doc.Abstract,
	)
	return err
}

// RemoveDocument removes a document and its edges from the store.
func (s *Store) RemoveDocument(path string) error {
	relPath := ""
	err := s.db.QueryRow("SELECT rel_path FROM documents WHERE path = ?", path).Scan(&relPath)
	if err != nil && err != sql.ErrNoRows {
		return err
	}

	if _, err := s.db.Exec("DELETE FROM documents WHERE path = ?", path); err != nil {
		return err
	}
	if relPath != "" {
		key := strings.TrimSuffix(relPath, ".md")
		if _, err := s.db.Exec("DELETE FROM edges WHERE from_path = ?", key); err != nil {
			return err
		}
	}
	return nil
}

// SetEdges replaces all outgoing edges for a node.
func (s *Store) SetEdges(fromPath string, edges []Edge) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM edges WHERE from_path = ?", fromPath); err != nil {
		return err
	}

	stmt, err := tx.Prepare("INSERT OR IGNORE INTO edges (from_path, to_path, edge_type, source) VALUES (?, ?, ?, ?)")
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, e := range edges {
		if _, err := stmt.Exec(e.From, e.To, e.Type, e.Source); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// SearchResult is a result from a full-text search.
type SearchResult struct {
	Path       string
	RelPath    string
	Title      string
	EntityType string
	Rank       float64
	Snippet    string
	Abstract   string
}

// Search performs a BM25-ranked full-text search.
func (s *Store) Search(query string, limit int) ([]SearchResult, error) {
	if limit <= 0 {
		limit = 10
	}

	// Escape the query for FTS5: quote each term to prevent column:value
	// interpretation of hyphens and other special chars.
	ftsQuery := escapeFTS5(query)

	rows, err := s.db.Query(`
		SELECT d.path, d.rel_path, d.title, d.entity_type,
		       rank AS score,
		       snippet(documents_fts, 3, '»', '«', '…', 20) AS snippet,
		       d.abstract
		FROM documents_fts
		JOIN documents d ON d.rowid = documents_fts.rowid
		WHERE documents_fts MATCH ?
		ORDER BY rank
		LIMIT ?`,
		ftsQuery, limit)
	if err != nil {
		return nil, err
	}

	var results []SearchResult
	seen := make(map[string]bool)
	for rows.Next() {
		var r SearchResult
		if err := rows.Scan(&r.Path, &r.RelPath, &r.Title, &r.EntityType, &r.Rank, &r.Snippet, &r.Abstract); err != nil {
			if closeErr := closeRows(rows, "search rows"); closeErr != nil {
				return nil, fmt.Errorf("scan search result: %w; %v", err, closeErr)
			}
			return nil, fmt.Errorf("scan search result: %w", err)
		}
		// FTS5 rank is negative (lower = better), flip for display
		r.Rank = -r.Rank
		// Dedup: same rel_path indexed from multiple roots
		if seen[r.RelPath] {
			continue
		}
		seen[r.RelPath] = true
		results = append(results, r)
	}
	if err := closeRows(rows, "search rows"); err != nil {
		return nil, err
	}
	return results, nil
}

// ListEntities returns indexed Wiki documents, optionally filtered by entity type.
func (s *Store) ListEntities(entityType string, limit int) ([]SearchResult, error) {
	if limit <= 0 {
		limit = 2000
	}

	q := `
		SELECT path, rel_path, title, entity_type, abstract
		FROM documents
		WHERE rel_path LIKE 'Wiki/%'`
	args := []any{}
	if entityType != "" {
		q += " AND lower(entity_type) = lower(?)"
		args = append(args, entityType)
	}
	q += " ORDER BY rel_path LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		if err := rows.Scan(&r.Path, &r.RelPath, &r.Title, &r.EntityType, &r.Abstract); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

// Neighbors returns outgoing edges from a node.
func (s *Store) Neighbors(node string, edgeType string, depth int) ([]GraphResult, error) {
	if depth <= 0 {
		depth = 1
	}

	// BFS traversal
	visited := map[string]bool{node: true}
	var results []GraphResult
	frontier := []string{node}

	for d := 0; d < depth && len(frontier) > 0; d++ {
		placeholders := make([]string, len(frontier))
		args := make([]any, 0, len(frontier)+1)
		for i, f := range frontier {
			placeholders[i] = "?"
			args = append(args, f)
		}

		q := fmt.Sprintf(
			"SELECT e.to_path, e.edge_type, COALESCE(MAX(d.title), e.to_path), COALESCE(MAX(d.entity_type), '') "+
				"FROM edges e LEFT JOIN documents d ON d.rel_path = e.to_path || '.md' "+
				"WHERE e.from_path IN (%s)", strings.Join(placeholders, ","))
		if edgeType != "" {
			q += " AND e.edge_type = ?"
			args = append(args, edgeType)
		}
		q += " GROUP BY e.to_path, e.edge_type"

		rows, err := s.db.Query(q, args...)
		if err != nil {
			return results, err
		}

		var next []string
		for rows.Next() {
			var r GraphResult
			if err := rows.Scan(&r.RelPath, &r.EdgeType, &r.Title, &r.EntityType); err != nil {
				rows.Close()
				return results, err
			}
			r.Depth = d + 1
			if !visited[r.RelPath] {
				visited[r.RelPath] = true
				next = append(next, r.RelPath)
				results = append(results, r)
			}
		}
		if err := rows.Close(); err != nil {
			return results, err
		}
		if err := rows.Err(); err != nil {
			return results, err
		}
		frontier = next
	}

	return results, nil
}

// Backlinks returns nodes that link TO the given node.
func (s *Store) Backlinks(node string, edgeType string) ([]GraphResult, error) {
	q := `SELECT e.from_path, e.edge_type, COALESCE(MAX(d.title), e.from_path), COALESCE(MAX(d.entity_type), '')
	      FROM edges e LEFT JOIN documents d ON d.rel_path = e.from_path || '.md'
	      WHERE e.to_path = ?`
	args := []any{node}
	if edgeType != "" {
		q += " AND e.edge_type = ?"
		args = append(args, edgeType)
	}
	q += " GROUP BY e.from_path, e.edge_type"

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []GraphResult
	for rows.Next() {
		var r GraphResult
		if err := rows.Scan(&r.RelPath, &r.EdgeType, &r.Title, &r.EntityType); err != nil {
			return nil, err
		}
		r.Depth = 1
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

// escapeFTS5 makes a user query safe for FTS5 MATCH.
// Hyphens are replaced with spaces (FTS5 tokenizes on them anyway).
// Colons and other operators are quoted to prevent column:value syntax.
func escapeFTS5(query string) string {
	// Replace hyphens with spaces — FTS5's unicode61 tokenizer splits on them
	query = strings.ReplaceAll(query, "-", " ")

	terms := strings.Fields(query)
	if len(terms) == 0 {
		return query
	}
	var safe []string
	for _, t := range terms {
		// Quote terms with special FTS5 chars (colon, parens, etc.)
		if strings.ContainsAny(t, ":*\"(){}[]^~") {
			safe = append(safe, "\""+strings.ReplaceAll(t, "\"", "\"\"")+"\"")
		} else {
			safe = append(safe, t)
		}
	}
	return strings.Join(safe, " ")
}

// GraphResult is a result from a graph traversal query.
type GraphResult struct {
	RelPath    string
	Title      string
	EntityType string
	EdgeType   string
	Depth      int
}

// Stats returns index statistics.
func (s *Store) Stats() (docs, edges int, err error) {
	s.db.QueryRow("SELECT COUNT(*) FROM documents").Scan(&docs)
	s.db.QueryRow("SELECT COUNT(*) FROM edges").Scan(&edges)
	return docs, edges, nil
}

// Clear removes all data from the store (for full rebuild).
func (s *Store) Clear() error {
	if _, err := s.db.Exec("DELETE FROM edges"); err != nil {
		return err
	}
	if _, err := s.db.Exec("DELETE FROM documents"); err != nil {
		return err
	}
	return nil
}

// HealthIssue represents a problem found in the vault.
type HealthIssue struct {
	IssueType string // "broken_link", "orphan", "stale"
	Title     string
	RelPath   string
}

// HealthCheck finds issues in the vault index.
func (s *Store) HealthCheck() ([]HealthIssue, error) {
	var issues []HealthIssue

	// Broken links: edges pointing to nodes with no document
	rows, err := s.db.Query(`
		SELECT DISTINCT e.to_path, e.from_path
		FROM edges e
		LEFT JOIN documents d ON d.rel_path = e.to_path || '.md'
		WHERE d.path IS NULL
		LIMIT 50`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var target, source string
		if err := rows.Scan(&target, &source); err != nil {
			if closeErr := closeRows(rows, "broken link rows"); closeErr != nil {
				return nil, fmt.Errorf("scan broken links: %w; %v", err, closeErr)
			}
			return nil, fmt.Errorf("scan broken links: %w", err)
		}
		issues = append(issues, HealthIssue{
			IssueType: "broken_link",
			Title:     fmt.Sprintf("%s → %s (not found)", source, target),
			RelPath:   source,
		})
	}
	if err := closeRows(rows, "broken link rows"); err != nil {
		return nil, err
	}

	// Orphan pages: documents with no incoming or outgoing edges
	rows, err = s.db.Query(`
		SELECT d.rel_path, d.title
		FROM documents d
		WHERE NOT EXISTS (
			SELECT 1 FROM edges e WHERE e.from_path = REPLACE(d.rel_path, '.md', '')
		)
		AND NOT EXISTS (
			SELECT 1 FROM edges e WHERE e.to_path = REPLACE(d.rel_path, '.md', '')
		)
		AND d.rel_path NOT LIKE 'Daily Log/%'
		LIMIT 30`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var relPath, title string
		if err := rows.Scan(&relPath, &title); err != nil {
			if closeErr := closeRows(rows, "orphan rows"); closeErr != nil {
				return nil, fmt.Errorf("scan orphan pages: %w; %v", err, closeErr)
			}
			return nil, fmt.Errorf("scan orphan pages: %w", err)
		}
		issues = append(issues, HealthIssue{
			IssueType: "orphan",
			Title:     title,
			RelPath:   relPath,
		})
	}
	if err := closeRows(rows, "orphan rows"); err != nil {
		return nil, err
	}

	// Stale pages: entity pages not updated in 90+ days
	rows, err = s.db.Query(`
		SELECT rel_path, title, last_updated FROM documents
		WHERE entity_type != ''
		AND last_updated != ''
		AND last_updated < date('now', '-90 days')
		AND rel_path NOT LIKE 'Daily Log/%'
		LIMIT 20`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var relPath, title, lastUpdated string
		if err := rows.Scan(&relPath, &title, &lastUpdated); err != nil {
			if closeErr := closeRows(rows, "stale rows"); closeErr != nil {
				return nil, fmt.Errorf("scan stale pages: %w; %v", err, closeErr)
			}
			return nil, fmt.Errorf("scan stale pages: %w", err)
		}
		issues = append(issues, HealthIssue{
			IssueType: "stale",
			Title:     fmt.Sprintf("%s (last updated: %s)", title, lastUpdated),
			RelPath:   relPath,
		})
	}
	if err := closeRows(rows, "stale rows"); err != nil {
		return nil, err
	}

	return issues, nil
}

func closeRows(rows *sql.Rows, context string) error {
	closeErr := rows.Close()
	iterErr := rows.Err()
	if closeErr != nil && iterErr != nil {
		return fmt.Errorf("%s close/iteration: %w", context, errors.Join(closeErr, iterErr))
	}
	if closeErr != nil {
		return fmt.Errorf("%s close: %w", context, closeErr)
	}
	if iterErr != nil {
		return fmt.Errorf("%s iteration: %w", context, iterErr)
	}
	return nil
}
