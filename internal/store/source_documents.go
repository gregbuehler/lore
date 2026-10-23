package store

import "fmt"

// SourceDocuments returns indexed Markdown documents from exactly one root.
// A non-empty query is searched against title and full body by the shared FTS index.
func (s *Store) SourceDocuments(root, query string, limit, offset int) ([]SearchResult, error) {
	if limit <= 0 {
		limit = 200
	}
	var rows interface {
		Next() bool
		Scan(...any) error
		Close() error
		Err() error
	}
	var err error
	if query == "" {
		rows, err = s.db.Query(`SELECT path, rel_path, title, entity_type, 0.0, '', abstract
			FROM documents WHERE root = ? ORDER BY rel_path LIMIT ? OFFSET ?`, root, limit, offset)
	} else {
		rows, err = s.db.Query(`SELECT d.path, d.rel_path, d.title, d.entity_type,
			rank, snippet(documents_fts, 3, '»', '«', '…', 20), d.abstract
			FROM documents_fts JOIN documents d ON d.rowid = documents_fts.rowid
			WHERE documents_fts MATCH ? AND d.root = ?
			ORDER BY rank, d.rel_path LIMIT ? OFFSET ?`, escapeFTS5(query), root, limit, offset)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []SearchResult
	for rows.Next() {
		var result SearchResult
		if err := rows.Scan(&result.Path, &result.RelPath, &result.Title, &result.EntityType, &result.Rank, &result.Snippet, &result.Abstract); err != nil {
			return nil, fmt.Errorf("scan source document: %w", err)
		}
		result.Rank = -result.Rank
		results = append(results, result)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}
