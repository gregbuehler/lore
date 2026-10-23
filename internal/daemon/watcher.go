package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Watcher monitors vault and library paths for file changes,
// debounces rapid events, and triggers full index rebuilds.
type Watcher struct {
	fw       *fsnotify.Watcher
	state    *State
	pending  map[string]time.Time
	mu       sync.Mutex
	debounce time.Duration
	stop     chan struct{}

	rebuildIndex func() error
}

// NewWatcher creates a file watcher for the given state.
func NewWatcher(state *State) (*Watcher, error) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("creating watcher: %w", err)
	}

	w := &Watcher{
		fw:       fw,
		state:    state,
		pending:  make(map[string]time.Time),
		debounce: 500 * time.Millisecond,
		stop:     make(chan struct{}),
	}
	w.rebuildIndex = state.BuildIndex

	// Add all directories under watched paths
	state.mu.RLock()
	defer state.mu.RUnlock()
	dirs := 0
	for _, root := range state.Paths {
		n, err := w.addRecursive(root)
		if err != nil {
			fw.Close()
			return nil, fmt.Errorf("watching %s: %w", root, err)
		}
		dirs += n
	}

	fmt.Printf("lore daemon: watching %d directories\n", dirs)
	return w, nil
}

// Start begins the event loop. Call in a goroutine.
func (w *Watcher) Start() {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-w.stop:
			return

		case event, ok := <-w.fw.Events:
			if !ok {
				return
			}
			w.handleEvent(event)

		case err, ok := <-w.fw.Errors:
			if !ok {
				return
			}
			fmt.Fprintf(os.Stderr, "lore watcher error: %v\n", err)

		case <-ticker.C:
			w.flush()
		}
	}
}

// Stop terminates the watcher.
func (w *Watcher) Stop() {
	close(w.stop)
	w.fw.Close()
}

func (w *Watcher) handleEvent(event fsnotify.Event) {
	path := filepath.Clean(event.Name)
	if !w.acceptsPath(path) {
		return
	}

	// A directory can already contain files before its watch is installed.
	// Queue a rebuild even if their individual create events were missed.
	if event.Has(fsnotify.Create) {
		if info, err := os.Lstat(path); err == nil && info.IsDir() {
			if w.handleCreatedDirectory(path) > 0 {
				w.queue(path)
			}
			return
		}
	}

	// Only care about markdown files
	if !strings.HasSuffix(path, ".md") {
		return
	}

	w.queue(path)
}

func (w *Watcher) queue(path string) {
	// Debounce: record the latest event time for this path.
	w.mu.Lock()
	w.pending[path] = time.Now()
	w.mu.Unlock()
}

// Hidden ancestors of a registered root are allowed, but hidden entries and
// symlinks within that root are not. Root selection is protected against source
// creation and subscription reloads, and favors a nested library over the vault.
func (w *Watcher) acceptsPath(path string) bool {
	w.state.mu.RLock()
	root := w.state.rootFor(path)
	w.state.mu.RUnlock()
	if root == "" {
		return false
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	current := root
	parts := strings.Split(rel, string(filepath.Separator))
	for i := -1; i < len(parts); i++ {
		if i >= 0 {
			if parts[i] == "." {
				continue
			}
			if strings.HasPrefix(parts[i], ".") {
				return false
			}
			current = filepath.Join(current, parts[i])
		}
		info, err := os.Lstat(current)
		if err != nil {
			if os.IsNotExist(err) {
				continue // Deleted or renamed Markdown must also trigger a rebuild.
			}
			return false
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return false
		}
	}
	return true
}

func (w *Watcher) handleCreatedDirectory(path string) int {
	if !w.acceptsPath(path) {
		return 0
	}
	count, err := w.addRecursive(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lore watcher: watch new directory %s: %v\n", path, err)
		return 0
	}
	return count
}

// flush processes any paths whose last event is older than the debounce window.
func (w *Watcher) flush() {
	w.mu.Lock()
	now := time.Now()
	var ready []string
	for path, lastEvent := range w.pending {
		if now.Sub(lastEvent) >= w.debounce {
			ready = append(ready, path)
		}
	}
	for _, path := range ready {
		delete(w.pending, path)
	}
	w.mu.Unlock()

	if len(ready) > 0 {
		if err := w.rebuildIndex(); err != nil {
			fmt.Fprintf(os.Stderr, "lore watcher: rebuild index: %v\n", err)
		}
	}
}

// addRecursive adds a directory and all its non-hidden subdirectories to the watcher.
func (w *Watcher) addRecursive(root string) (int, error) {
	count := 0
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			return nil
		}
		if path != root && strings.HasPrefix(info.Name(), ".") {
			return filepath.SkipDir
		}
		if err := w.fw.Add(path); err != nil {
			return nil // skip unwatchable dirs
		}
		count++
		return nil
	})
	return count, err
}
