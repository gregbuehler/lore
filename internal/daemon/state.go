package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/gbuehler/lore/internal/parse"
	"github.com/gbuehler/lore/internal/resolve"
	"github.com/gbuehler/lore/internal/store"
)

// State holds the daemon's index state. The SQLite store persists between
// restarts; the file watcher keeps it current with the markdown source of truth.
type State struct {
	Store     *store.Store
	VaultPath string
	Paths     []string // all indexed root paths (vault + libraries)
	resolver  *shortNameResolver
	mu        sync.RWMutex
}

// NewState opens the SQLite store and prepares for indexing.
func NewState(vaultPath string, libraryPaths []string) (*State, error) {
	db, err := store.OpenForVault(store.DefaultPathForVault(vaultPath), vaultPath)
	if err != nil {
		return nil, err
	}

	paths := append([]string{vaultPath}, libraryPaths...)
	return &State{
		Store:     db,
		VaultPath: vaultPath,
		Paths:     paths,
	}, nil
}

// BuildIndex walks all configured paths and populates the store.
// This is a full rebuild — clears existing data first.
func (s *State) BuildIndex() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buildIndexLocked()
}

func (s *State) buildIndexLocked() error {
	if err := s.Store.Clear(); err != nil {
		return err
	}

	// Build short-name resolver from all known files before indexing edges.
	s.resolver = newShortNameResolver()
	for _, root := range s.Paths {
		s.resolver.scan(root)
	}

	for _, root := range s.Paths {
		if err := s.indexPath(root); err != nil {
			return err
		}
	}
	return nil
}

// RebuildIndexForPath rebuilds the full index when path is under a configured
// indexed root. The index is rebuilt as a whole because wikilinks can resolve
// differently after any file create, update, rename, or removal.
func (s *State) RebuildIndexForPath(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.rootFor(path) == "" {
		return nil
	}
	return s.buildIndexLocked()
}

// skipIndexFile returns true for files that are infrastructure, not content.
func skipIndexFile(name string) bool {
	return resolve.SkipIndexFile(name)
}

func (s *State) indexPath(root string) error {
	return filepath.Walk(root, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if fi.IsDir() {
			if strings.HasPrefix(fi.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(fi.Name(), ".md") {
			return nil
		}
		if skipIndexFile(fi.Name()) {
			return nil
		}
		return s.indexSingleFile(path, root)
	})
}

func (s *State) indexSingleFile(path, root string) error {
	doc, err := parse.ParseDocument(path, root)
	if err != nil {
		return err
	}

	// Upsert document
	storeDoc := &store.Document{
		Path:        doc.Path,
		RelPath:     doc.RelPath,
		Title:       doc.Title,
		EntityType:  doc.EntityType,
		Aliases:     doc.Aliases,
		Tags:        doc.Tags,
		LastUpdated: doc.LastUpdated,
		Status:      doc.Status,
		Body:        doc.Body,
		Root:        root,
		Abstract:    parse.BuildAbstract(doc),
	}
	if err := s.Store.UpsertDocument(storeDoc); err != nil {
		return err
	}

	// Build and store edges, resolving short-name and relative targets
	relKey := strings.TrimSuffix(doc.RelPath, ".md")
	var edges []store.Edge
	for _, wl := range doc.Wikilinks {
		target := wl.Target
		if s.resolver != nil {
			if resolved := s.resolver.resolve(target, relKey); resolved != "" {
				target = resolved
			}
		}
		edges = append(edges, store.Edge{
			From:   relKey,
			To:     target,
			Type:   wl.EdgeType,
			Source: wl.Source,
		})
	}
	if err := s.Store.SetEdges(relKey, edges); err != nil {
		return err
	}

	return nil
}

func (s *State) rootFor(path string) string {
	for _, root := range s.Paths {
		rel, err := filepath.Rel(root, path)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
			return root
		}
	}
	return ""
}
