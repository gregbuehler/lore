package store

import (
	"errors"
	"fmt"
	"log"
	"strings"

	sqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// FTS5 health and repair.
//
// FTS5 damage is not uniform, and the cheap checks lie. An index can pass
// 'integrity-check', answer plain MATCH, bm25() and snippet() correctly, and
// hold a complete set of rows, while the ranked traversal that `ORDER BY rank`
// performs still fails with SQLITE_CORRUPT_VTAB (267). Repopulating the content
// table — i.e. a full reindex, even one that deletes and reinserts every
// document — does not necessarily clear the damaged segment that the ranked
// traversal reads. The only reliable repair is an FTS5-level rebuild:
//
//	INSERT INTO documents_fts(documents_fts) VALUES('rebuild');
//
// It costs ~0.15s for ~1k documents, so we can afford to run it on any doubt.
//
// Guards, in order of cheapness:
//   - VerifyFTS runs a ranked probe, not just integrity-check (Open, doctor).
//   - Search self-heals once when a query hits corruption mid-session.
//   - Full index builds finish with RebuildFTS so `lore reindex` actually repairs.

// ErrFTSUnhealthy reports that the FTS index cannot serve ranked queries.
var ErrFTSUnhealthy = errors.New("FTS index unhealthy")

// isFTSCorruption reports whether err is SQLite corruption, including the
// virtual-table flavour (SQLITE_CORRUPT_VTAB, 267) that FTS5 raises when a
// segment the query needs is damaged.
func isFTSCorruption(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrFTSUnhealthy) {
		return true
	}
	var serr *sqlite.Error
	if errors.As(err, &serr) {
		switch serr.Code() {
		case sqlite3.SQLITE_CORRUPT, sqlite3.SQLITE_CORRUPT_VTAB, sqlite3.SQLITE_NOTADB:
			return true
		}
	}
	// modernc.org/sqlite surfaces some vtab errors as plain strings.
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "database disk image is malformed") ||
		strings.Contains(msg, "sqlite_corrupt") ||
		strings.Contains(msg, "(267)")
}

// VerifyFTS checks the FTS index the way real queries use it.
//
// It runs FTS5's own integrity-check *and* a ranked probe, because the two fail
// independently: integrity-check passing tells you nothing about whether
// `ORDER BY rank` works. Probes use tokens taken from indexed titles, so on a
// healthy index a probe must match at least its own document — probes that match
// nothing mean the index lost content the documents table still has.
func (s *Store) VerifyFTS() error {
	if _, err := s.db.Exec("INSERT INTO documents_fts(documents_fts) VALUES('integrity-check')"); err != nil {
		return fmt.Errorf("%w: integrity-check failed: %v", ErrFTSUnhealthy, err)
	}

	tokens, err := s.probeTokens()
	if err != nil {
		return err
	}
	for _, tok := range tokens {
		match := "\"" + strings.ReplaceAll(tok, "\"", "\"\"") + "\""

		var unranked int
		err := s.db.QueryRow(
			`SELECT count(*) FROM (SELECT rowid FROM documents_fts WHERE documents_fts MATCH ? LIMIT 1)`,
			match).Scan(&unranked)
		if err != nil {
			return fmt.Errorf("%w: unranked probe %q failed: %v", ErrFTSUnhealthy, tok, err)
		}
		if unranked == 0 {
			// Tokenizer disagreement is possible for odd titles; try the next token
			// before concluding that the index lost rows.
			continue
		}

		var ranked int
		err = s.db.QueryRow(
			`SELECT count(*) FROM (SELECT rowid FROM documents_fts WHERE documents_fts MATCH ? ORDER BY rank LIMIT 1)`,
			match).Scan(&ranked)
		if err != nil {
			return fmt.Errorf("%w: ranked probe %q failed: %v", ErrFTSUnhealthy, tok, err)
		}
		if ranked == 0 {
			return fmt.Errorf("%w: probe %q matches %d rows unranked but 0 with ORDER BY rank",
				ErrFTSUnhealthy, tok, unranked)
		}
		return nil
	}
	if len(tokens) > 0 {
		return fmt.Errorf("%w: none of %d probe tokens from indexed titles match any row",
			ErrFTSUnhealthy, len(tokens))
	}
	return nil
}

// probeTokens returns tokens drawn from indexed content, so that a MATCH for
// them is expected to hit at least one row on a healthy index.
func (s *Store) probeTokens() ([]string, error) {
	rows, err := s.db.Query(`SELECT title FROM documents WHERE title <> '' ORDER BY rowid LIMIT 8`)
	if err != nil {
		return nil, fmt.Errorf("%w: reading probe titles: %v", ErrFTSUnhealthy, err)
	}
	var tokens []string
	seen := map[string]bool{}
	for rows.Next() {
		var title string
		if err := rows.Scan(&title); err != nil {
			_ = closeRows(rows, "probe titles")
			return nil, fmt.Errorf("%w: scanning probe title: %v", ErrFTSUnhealthy, err)
		}
		for _, f := range strings.FieldsFunc(title, func(r rune) bool {
			return !(r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z')
		}) {
			if len(f) < 3 || seen[strings.ToLower(f)] {
				continue
			}
			seen[strings.ToLower(f)] = true
			tokens = append(tokens, f)
			break
		}
	}
	if err := closeRows(rows, "probe titles"); err != nil {
		return nil, err
	}
	return tokens, nil
}

// RebuildFTS runs the FTS5-level rebuild that re-derives every segment from the
// content table. This is the only repair that clears damaged segments; deleting
// and reinserting content rows does not.
func (s *Store) RebuildFTS() error {
	if _, err := s.db.Exec("INSERT INTO documents_fts(documents_fts) VALUES('rebuild')"); err != nil {
		return fmt.Errorf("FTS rebuild failed: %w", err)
	}
	return nil
}

// RepairFTSIfNeeded verifies the index and rebuilds it when the verification
// fails. It reports whether a rebuild was performed.
func (s *Store) RepairFTSIfNeeded() (repaired bool, err error) {
	verifyErr := s.VerifyFTS()
	if verifyErr == nil {
		return false, nil
	}
	if err := s.RebuildFTS(); err != nil {
		return false, fmt.Errorf("%v; rebuild also failed: %w", verifyErr, err)
	}
	if err := s.VerifyFTS(); err != nil {
		return true, fmt.Errorf("FTS still unhealthy after rebuild (original: %v): %w", verifyErr, err)
	}
	return true, nil
}

// selfHealFTS is the mid-session recovery path: a query already failed with
// corruption, so rebuild once and let the caller retry.
func (s *Store) selfHealFTS(cause error) error {
	log.Printf("lore: FTS corruption detected (%v); rebuilding FTS index", cause)
	if err := s.RebuildFTS(); err != nil {
		return fmt.Errorf("%v; %w", cause, err)
	}
	log.Printf("lore: FTS index rebuilt")
	return nil
}
