package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/gregbuehler/lore/internal/config"
)

func TestHandleCreatedDirectoryWatchesNestedSubdirectories(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "Wiki", "Services")
	child := filepath.Join(parent, "Nested")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatalf("mkdir nested tree: %v", err)
	}

	fw, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatalf("new watcher: %v", err)
	}
	defer fw.Close()

	w := &Watcher{fw: fw, state: &State{Paths: []string{root}}}
	count := w.handleCreatedDirectory(parent)
	if count != 2 {
		t.Fatalf("watched directory count = %d, want 2", count)
	}
}

func TestFlushCoalescesMultipleReadyPathsIntoOneReindex(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first.md")
	second := filepath.Join(dir, "second.md")
	if err := os.WriteFile(first, []byte("# First\n"), 0o644); err != nil {
		t.Fatalf("write first: %v", err)
	}
	if err := os.WriteFile(second, []byte("# Second\n"), 0o644); err != nil {
		t.Fatalf("write second: %v", err)
	}

	calls := 0
	w := &Watcher{
		pending: map[string]time.Time{
			first:  time.Now().Add(-time.Second),
			second: time.Now().Add(-time.Second),
		},
		debounce:     time.Millisecond,
		rebuildIndex: func() error { calls++; return nil },
	}

	w.flush()

	if calls != 1 {
		t.Fatalf("reindex calls = %d, want 1", calls)
	}
}

func TestFlushCoalescesMixedReadyPathsIntoOneRebuild(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "existing.md")
	deleted := filepath.Join(dir, "deleted.md")
	if err := os.WriteFile(existing, []byte("# Existing\n"), 0o644); err != nil {
		t.Fatalf("write existing: %v", err)
	}

	rebuilds := 0
	w := &Watcher{
		pending: map[string]time.Time{
			existing: time.Now().Add(-time.Second),
			deleted:  time.Now().Add(-time.Second),
		},
		debounce:     time.Millisecond,
		rebuildIndex: func() error { rebuilds++; return nil },
	}

	w.flush()

	if rebuilds != 1 {
		t.Fatalf("rebuild calls = %d, want 1", rebuilds)
	}
}

func TestWatcherFiltersRelativeToMostSpecificSourceRoot(t *testing.T) {
	vault := filepath.Join(t.TempDir(), ".hidden-parent", "vault")
	library := filepath.Join(vault, ".lore", "libraries", "notes")
	if err := os.MkdirAll(filepath.Join(library, ".metadata", "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "outside.md"), []byte("# Outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, link := range []struct{ name, target string }{
		{"linked.md", filepath.Join(outside, "outside.md")},
		{"linked-dir", outside},
	} {
		if err := os.Symlink(link.target, filepath.Join(library, link.name)); err != nil {
			t.Fatal(err)
		}
	}
	w := &Watcher{state: &State{Paths: []string{vault, library}}, pending: make(map[string]time.Time)}
	for _, tc := range []struct {
		path string
		want bool
	}{
		{filepath.Join(vault, "visible.md"), true},
		{filepath.Join(library, "external.md"), true},
		{filepath.Join(library, "nested", "removed.md"), true},
		{filepath.Join(vault, ".lore", "unregistered.md"), false},
		{filepath.Join(library, ".metadata", "hidden.md"), false},
		{filepath.Join(library, ".hidden.md"), false},
		{filepath.Join(library, "linked.md"), false},
		{filepath.Join(library, "linked-dir", "outside.md"), false},
		{filepath.Join(outside, "outside.md"), false},
		{filepath.Join(vault+"-sibling", "outside.md"), false},
	} {
		w.handleEvent(fsnotify.Event{Name: tc.path, Op: fsnotify.Write})
		if _, got := w.pending[tc.path]; got != tc.want {
			t.Errorf("pending[%q] = %v, want %v", tc.path, got, tc.want)
		}
	}
	// Directory events must pass the same filtering before adding watches.
	w.handleEvent(fsnotify.Event{Name: filepath.Join(library, ".metadata", "nested"), Op: fsnotify.Create})
	w.handleEvent(fsnotify.Event{Name: filepath.Join(library, "linked-dir"), Op: fsnotify.Create})
	w.handleEvent(fsnotify.Event{Name: outside, Op: fsnotify.Create})
}

func TestWatcherDirectoryCreateQueuesExistingContents(t *testing.T) {
	root := t.TempDir()
	w, err := NewWatcher(&State{Paths: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Stop()
	nested := filepath.Join(root, "created", "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "already-there.md"), []byte("# Existing contents"), 0o644); err != nil {
		t.Fatal(err)
	}
	created := filepath.Dir(nested)
	w.handleEvent(fsnotify.Event{Name: created, Op: fsnotify.Create})
	if _, ok := w.pending[created]; !ok {
		t.Fatal("directory creation did not schedule indexing its existing contents")
	}
}

func TestWatcherSourceRootLookupDuringReload(t *testing.T) {
	root := t.TempDir()
	library := filepath.Join(root, ".lore", "libraries", "notes")
	state := &State{Paths: []string{root}}
	w := &Watcher{state: state, pending: make(map[string]time.Time)}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 100 {
			state.mu.Lock()
			state.Paths = []string{root, library}
			state.mu.Unlock()
			state.mu.Lock()
			state.Paths = []string{root}
			state.mu.Unlock()
		}
	}()
	for range 100 {
		w.handleEvent(fsnotify.Event{Name: filepath.Join(library, "external.md"), Op: fsnotify.Write})
	}
	wg.Wait()
}

func TestWatcherIndexesExternalChangesInManagedLibraries(t *testing.T) {
	for _, registration := range []string{"initial", "created", "reloaded"} {
		t.Run(registration, func(t *testing.T) {
			d, vault, _ := documentTestDaemon(t)
			cfg := &config.Config{Vault: config.VaultConfig{Path: vault}}
			if err := cfg.Save(vault); err != nil {
				t.Fatal(err)
			}
			root := filepath.Join(vault, ".lore", "libraries", "review-fixes")
			register := func() {
				t.Helper()
				if err := os.MkdirAll(root, 0o755); err != nil {
					t.Fatal(err)
				}
				cfg.Subscriptions = []config.SubscriptionConfig{{Name: "review-fixes", Repo: "local:" + root, Path: root, Access: "read-write"}}
				if err := cfg.Save(vault); err != nil {
					t.Fatal(err)
				}
				if response := d.dispatch(&Request{Type: "sources"}); !response.OK {
					t.Fatal(response.Error)
				}
			}
			if registration == "initial" {
				register()
			}
			if err := d.state.BuildIndex(); err != nil {
				t.Fatal(err)
			}
			w, err := NewWatcher(d.state)
			if err != nil {
				t.Fatal(err)
			}
			d.watcher = w
			stopped := make(chan struct{})
			go func() {
				defer close(stopped)
				w.Start()
			}()
			defer func() {
				w.Stop()
				<-stopped
			}()
			handler := d.handleHTTP("watcher-token")
			if registration == "created" {
				req := httptest.NewRequest(http.MethodPost, "/api/v1/sources", strings.NewReader(`{"name":"review-fixes"}`))
				req.Header.Set("Authorization", "Bearer watcher-token")
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, req)
				if response.Code != http.StatusOK {
					t.Fatalf("create source = %d %s", response.Code, response.Body.String())
				}
			} else if registration == "reloaded" {
				register()
			}
			// These writes bypass Lore entirely; only filesystem events can
			// bring the documents into the disposable search index.
			awaitDocument := func(rel, query string) DocumentSummary {
				t.Helper()
				deadline := time.Now().Add(5 * time.Second)
				for {
					req := httptest.NewRequest(http.MethodGet, "/api/v1/documents?source=library:review-fixes&q="+query, nil)
					req.Header.Set("Authorization", "Bearer watcher-token")
					recorder := httptest.NewRecorder()
					handler.ServeHTTP(recorder, req)
					if recorder.Code != http.StatusOK {
						t.Fatalf("list documents = %d %s", recorder.Code, recorder.Body.String())
					}
					var response httpDocumentListResponse
					if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
						t.Fatal(err)
					}
					for _, doc := range response.Documents {
						if doc.RelPath == rel {
							return doc
						}
					}
					if time.Now().After(deadline) {
						t.Fatalf("watcher never indexed %s for query %q: %s", rel, query, recorder.Body.String())
					}
					time.Sleep(20 * time.Millisecond)
				}
			}
			path := filepath.Join(root, "external.md")
			if err := os.WriteFile(path, []byte("# External\nexternalwatchermarker\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			original := awaitDocument("external.md", "externalwatchermarker")
			if original.DocumentID == "" {
				t.Fatal("externally created document has no identity")
			}
			if err := os.WriteFile(path, []byte("# External edited\nupdatedwatchermarker\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			edited := awaitDocument("external.md", "updatedwatchermarker")
			if original.DocumentID != edited.DocumentID || original.Revision == edited.Revision {
				t.Fatalf("external edit identity/revision: before=%#v after=%#v", original, edited)
			}
			if listed := awaitDocument("external.md", ""); listed.DocumentID != original.DocumentID {
				t.Fatal("listing changed external document identity")
			}
			nested := filepath.Join(root, "new", "nested")
			if err := os.MkdirAll(nested, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(nested, "external.md"), []byte("# Nested\nnestedwatchermarker\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			awaitDocument("new/nested/external.md", "nestedwatchermarker")
		})
	}
}
