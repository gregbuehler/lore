package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/gregbuehler/lore/internal/config"
	"github.com/gregbuehler/lore/internal/parse"
	"github.com/gregbuehler/lore/internal/store"
)

// State holds the daemon's index state. The SQLite store persists between
// restarts; the file watcher keeps it current with the markdown source of truth.
type State struct {
	Store             *store.Store
	VaultPath         string
	Paths             []string // all indexed root paths (vault + libraries)
	sources           []sourceRoot
	initialized       bool
	resolver          *shortNameResolver
	mu                sync.RWMutex
	apiMu             sync.Mutex
	identityMu        sync.Mutex
	identities        map[string]documentIdentity
	configuredSources map[string]bool
}

type sourceRoot struct {
	Source
	root string
}

// NewState opens the SQLite store and prepares for indexing.
func NewState(vaultPath string, libraryPaths []string) (*State, error) {
	db, err := store.OpenForVault(store.DefaultPathForVault(vaultPath), vaultPath)
	if err != nil {
		return nil, err
	}

	paths := append([]string{vaultPath}, libraryPaths...)
	state := &State{Store: db, VaultPath: vaultPath, Paths: paths}
	state.sources = append(state.sources, sourceRoot{
		Source: Source{SourceID: "vault", Name: "Vault", Kind: "vault", Writable: true},
		root:   vaultPath,
	})

	// Source metadata is intentionally additive to the legacy Start signature.
	// A missing/invalid config must not prevent the legacy daemon from starting;
	// new source APIs report invalid metadata rather than exposing ambiguous IDs.
	cfg, cfgErr := config.Load(vaultPath)
	if cfgErr == nil {
		state.configuredSources = make(map[string]bool)
		// Subscription names, not root basenames, are the stable public IDs.
		// Keep distinct subscriptions even when they intentionally share a root.
		for _, sub := range cfg.Subscriptions {
			name := strings.TrimSpace(sub.Name)
			state.configuredSources["library:"+name] = true
			state.sources = append(state.sources, sourceRoot{
				Source: Source{SourceID: "library:" + name, Name: name, Kind: "library", Writable: sub.Access == "read-write"},
				root:   sub.ContentPath(),
			})
			found := false
			for _, path := range state.Paths {
				if sameRoot(path, sub.ContentPath()) {
					found = true
					break
				}
			}
			if !found {
				state.Paths = append(state.Paths, sub.ContentPath())
			}
		}
		// Unconfigured paths passed by legacy callers retain their old readonly
		// basename-derived identities.
		for _, path := range libraryPaths {
			configured := false
			for _, sub := range cfg.Subscriptions {
				if sameRoot(path, sub.ContentPath()) {
					configured = true
					break
				}
			}
			if configured {
				continue
			}
			name := filepath.Base(path)
			state.sources = append(state.sources, sourceRoot{
				Source: Source{SourceID: "library:" + name, Name: name, Kind: "library"},
				root:   path,
			})
		}
	} else {
		for _, path := range libraryPaths {
			name := filepath.Base(path)
			state.sources = append(state.sources, sourceRoot{
				Source: Source{SourceID: "library:" + name, Name: name, Kind: "library"},
				root:   path,
			})
		}
	}
	return state, nil
}

func sameRoot(a, b string) bool {
	aa, errA := filepath.Abs(a)
	bb, errB := filepath.Abs(b)
	return errA == nil && errB == nil && filepath.Clean(aa) == filepath.Clean(bb)
}

// BuildIndex walks all configured paths and populates the store.
// This is a full rebuild — clears existing data first.
func (s *State) BuildIndex() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.buildIndexLocked(); err != nil {
		return err
	}
	s.initialized = true
	return nil
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

	// Content rows alone are not enough: FTS5 can hold every row and still fail
	// the ranked traversal that `ORDER BY rank` uses. Finish a full build with an
	// FTS-level rebuild so `lore reindex` genuinely repairs a damaged index.
	if err := s.Store.RebuildFTS(); err != nil {
		return err
	}
	if err := s.Store.VerifyFTS(); err != nil {
		return err
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
		if fi.Mode()&os.ModeSymlink != 0 || !fi.Mode().IsRegular() || !strings.HasSuffix(fi.Name(), ".md") || strings.HasPrefix(fi.Name(), ".") {
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

// rootFor returns the most-specific registered root. Callers hold s.mu.
func (s *State) rootFor(path string) string {
	var best string
	for _, root := range s.Paths {
		rel, err := filepath.Rel(root, path)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
			if best == "" || len(filepath.Clean(root)) > len(filepath.Clean(best)) {
				best = root
			}
		}
	}
	return best
}
