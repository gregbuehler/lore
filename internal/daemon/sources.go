package daemon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gregbuehler/lore/internal/config"
)

// Libraries created through HTTP are local Lore subscriptions, not another
// content store. Keeping their roots under .lore avoids indexing them twice as
// vault content and keeps a self-contained deployment on one canonical volume.
func (d *Daemon) dispatchSourceCreate(req *Request) *Response {
	name := strings.TrimSpace(req.Name)
	if name == "" || name != req.Name || len(name) > 120 || strings.HasPrefix(name, ".") || strings.ContainsAny(name, "/\\\x00\r\n:") {
		return &Response{OK: false, Code: "invalid_source", Error: "library name must be a non-hidden path-free name"}
	}
	d.state.mu.Lock()
	defer d.state.mu.Unlock()
	cfg, err := config.Load(d.state.VaultPath)
	if err != nil {
		return &Response{OK: false, Error: "unable to load Lore configuration"}
	}
	for _, sub := range cfg.Subscriptions {
		if strings.EqualFold(sub.Name, name) {
			return &Response{OK: false, Code: "conflict", Error: "library already exists"}
		}
	}
	for _, source := range d.state.sources {
		if strings.EqualFold(source.SourceID, "library:"+name) {
			return &Response{OK: false, Code: "conflict", Error: "library already exists"}
		}
	}
	metadataPath, err := d.state.identityPath()
	if err != nil {
		return identityError(err)
	}
	parent := filepath.Join(filepath.Dir(metadataPath), "libraries")
	if err := os.Mkdir(parent, 0o755); err != nil && !os.IsExist(err) {
		return &Response{OK: false, Error: "unable to create library directory"}
	}
	info, err := os.Lstat(parent)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return pathError(fmt.Errorf("unsafe library directory"))
	}
	root := filepath.Join(parent, name)
	if err := os.Mkdir(root, 0o755); err != nil {
		if os.IsExist(err) {
			return &Response{OK: false, Code: "conflict", Error: "library path already exists"}
		}
		return &Response{OK: false, Error: "unable to create library"}
	}
	cfg.Subscriptions = append(cfg.Subscriptions, config.SubscriptionConfig{Name: name, Repo: "local:" + root, Path: root, Access: "read-write"})
	if err := cfg.Save(d.state.VaultPath); err != nil {
		_ = os.Remove(root)
		return &Response{OK: false, Error: "unable to persist library subscription"}
	}
	source := sourceRoot{Source: Source{SourceID: "library:" + name, Name: name, Kind: "library", Writable: true}, root: root}
	d.state.sources = append(d.state.sources, source)
	d.state.Paths = append(d.state.Paths, root)
	if d.state.configuredSources == nil {
		d.state.configuredSources = make(map[string]bool)
	}
	d.state.configuredSources[source.SourceID] = true
	if d.watcher != nil {
		_, _ = d.watcher.addRecursive(root)
	}
	return &Response{OK: true, Source: &source.Source}
}

// Reload subscription metadata on discovery so a CLI subscribe/unsubscribe is
// visible to HTTP clients without a second registry or a daemon restart.
func (d *Daemon) refreshSources() error {
	d.state.mu.Lock()
	defer d.state.mu.Unlock()
	cfg, err := config.Load(d.state.VaultPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	} // Legacy explicit-path callers.
	if err != nil {
		return err
	}
	next := make([]sourceRoot, 0, len(cfg.Subscriptions)+1)
	configured := make(map[string]bool, len(cfg.Subscriptions))
	for _, source := range d.state.sources {
		if source.SourceID == "vault" {
			next = append(next, source)
		}
	}
	for _, sub := range cfg.Subscriptions {
		name := strings.TrimSpace(sub.Name)
		source := sourceRoot{Source: Source{SourceID: "library:" + name, Name: name, Kind: "library", Writable: sub.Access == "read-write"}, root: sub.ContentPath()}
		next = append(next, source)
		configured[source.SourceID] = true
	}
	for _, source := range d.state.sources {
		if source.SourceID != "vault" && !d.state.configuredSources[source.SourceID] && !configured[source.SourceID] {
			next = append(next, source)
		}
	}
	changed := len(next) != len(d.state.sources)
	if !changed {
		for i := range next {
			if next[i] != d.state.sources[i] {
				changed = true
				break
			}
		}
	}
	if !changed {
		return nil
	}
	oldSources, oldPaths, oldConfigured := d.state.sources, d.state.Paths, d.state.configuredSources
	d.state.sources = next
	if err := d.validateSources(); err != nil {
		d.state.sources = oldSources
		return err
	}
	d.state.configuredSources = configured
	d.state.Paths = []string{d.state.VaultPath}
	for _, source := range next {
		found := false
		for _, root := range d.state.Paths {
			if sameRoot(root, source.root) {
				found = true
				break
			}
		}
		if !found {
			d.state.Paths = append(d.state.Paths, source.root)
		}
	}
	if err := d.state.buildIndexLocked(); err != nil {
		d.state.sources, d.state.Paths, d.state.configuredSources = oldSources, oldPaths, oldConfigured
		return err
	}
	if d.watcher != nil {
		for _, root := range d.state.Paths {
			_, _ = d.watcher.addRecursive(root)
		}
	}
	return nil
}
