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
// debounces rapid events, and triggers incremental re-indexing.
type Watcher struct {
	fw       *fsnotify.Watcher
	state    *State
	pending  map[string]time.Time
	mu       sync.Mutex
	debounce time.Duration
	stop     chan struct{}
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

	// Add all directories under watched paths
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
	path := event.Name

	// If a new directory was created, watch it
	if event.Has(fsnotify.Create) {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			if !strings.HasPrefix(info.Name(), ".") {
				w.fw.Add(path)
			}
			return
		}
	}

	// Only care about markdown files
	if !strings.HasSuffix(path, ".md") {
		return
	}

	// Skip hidden paths
	for _, part := range strings.Split(path, string(filepath.Separator)) {
		if strings.HasPrefix(part, ".") {
			return
		}
	}

	// Debounce: record the latest event time for this path
	w.mu.Lock()
	w.pending[path] = time.Now()
	w.mu.Unlock()
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

	for _, path := range ready {
		// Check if file still exists
		if _, err := os.Stat(path); os.IsNotExist(err) {
			w.state.RemoveFile(path)
		} else {
			if err := w.state.IndexFile(path); err != nil {
				fmt.Fprintf(os.Stderr, "lore watcher: reindex %s: %v\n", path, err)
			}
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
		if strings.HasPrefix(info.Name(), ".") {
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
