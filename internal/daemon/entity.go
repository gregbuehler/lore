package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	entitypkg "github.com/gbuehler/lore/internal/entity"
	"github.com/gbuehler/lore/internal/parse"
	"github.com/gbuehler/lore/internal/pathutil"
)

// dispatchEntityCreate creates a new entity page in the vault, then reindexes it.
func (d *Daemon) dispatchEntityCreate(req *Request) *Response {
	if req.EntityPath == "" {
		return &Response{OK: false, Error: "entity_path is required"}
	}
	if req.EntityType == "" {
		return &Response{OK: false, Error: "entity_type is required"}
	}
	if !entitypkg.ValidTypes[req.EntityType] {
		return &Response{OK: false, Error: fmt.Sprintf("unknown entity_type %q; valid types: service, environment, person, tool, infrastructure, organization, customer, vendor, concept", req.EntityType)}
	}
	if d.state.VaultPath == "" {
		return &Response{OK: false, Error: "daemon vault path not set"}
	}

	destPath, entityPath, err := pathutil.ResolveMarkdownUnderRoot(d.state.VaultPath, req.EntityPath)
	if err != nil {
		return &Response{OK: false, Error: err.Error()}
	}

	// Refuse to overwrite
	if _, err := os.Stat(destPath); err == nil {
		return &Response{OK: false, Error: fmt.Sprintf("file already exists: %s (use entity_update to modify it)", destPath)}
	}

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return &Response{OK: false, Error: fmt.Sprintf("creating directory: %v", err)}
	}

	title := req.EntityTitle
	if title == "" {
		title = filepath.Base(entityPath)
	}

	today := time.Now().Format("2006-01-02")
	content := entitypkg.BuildContent(req.EntityType, title, today)

	if err := os.WriteFile(destPath, []byte(content), 0644); err != nil {
		return &Response{OK: false, Error: fmt.Sprintf("writing entity file: %v", err)}
	}

	// Reindex immediately
	if err := d.state.IndexFile(destPath); err != nil {
		// Non-fatal — file was written, index will catch up via watcher
		fmt.Printf("lore daemon: warning: reindex after create failed: %v\n", err)
	}

	return &Response{
		OK:      true,
		Content: destPath,
	}
}

// dispatchEntityUpdate updates frontmatter fields and/or appends to the Change Log,
// then reindexes the file.
func (d *Daemon) dispatchEntityUpdate(req *Request) *Response {
	if req.EntityPath == "" {
		return &Response{OK: false, Error: "entity_path is required"}
	}
	if d.state.VaultPath == "" {
		return &Response{OK: false, Error: "daemon vault path not set"}
	}

	filePath, _, err := pathutil.ResolveMarkdownUnderRoot(d.state.VaultPath, req.EntityPath)
	if err != nil {
		return &Response{OK: false, Error: err.Error()}
	}

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return &Response{OK: false, Error: fmt.Sprintf("file does not exist: %s (use entity_create to create it)", filePath)}
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return &Response{OK: false, Error: fmt.Sprintf("reading entity file: %v", err)}
	}
	content := string(data)

	// Apply set_fields to frontmatter
	for key, val := range req.SetFields {
		content, err = parse.SetFrontmatterField(content, key, val)
		if err != nil {
			return &Response{OK: false, Error: fmt.Sprintf("updating frontmatter key %q: %v", key, err)}
		}
	}

	// Append changelog entry
	if req.Changelog != "" {
		today := time.Now().Format("2006-01-02")
		entry := fmt.Sprintf("- **%s**: %s", today, req.Changelog)
		content, err = entitypkg.AppendToSection(content, "Change Log", entry)
		if err != nil {
			return &Response{OK: false, Error: fmt.Sprintf("appending changelog: %v", err)}
		}
	}

	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		return &Response{OK: false, Error: fmt.Sprintf("writing entity file: %v", err)}
	}

	// Reindex immediately
	if err := d.state.IndexFile(filePath); err != nil {
		fmt.Printf("lore daemon: warning: reindex after update failed: %v\n", err)
	}

	return &Response{
		OK:      true,
		Content: filePath,
	}
}

// dispatchEntityGet reads an entity page from the vault and returns its content.
func (d *Daemon) dispatchEntityGet(req *Request) *Response {
	if req.EntityPath == "" {
		return &Response{OK: false, Error: "entity_path is required"}
	}

	// Search all indexed paths (vault + libraries)
	filePath := ""
	for _, root := range d.state.Paths {
		candidate, _, err := pathutil.ResolveMarkdownUnderRoot(root, req.EntityPath)
		if err != nil {
			return &Response{OK: false, Error: err.Error()}
		}
		if _, err := os.Stat(candidate); err == nil {
			filePath = candidate
			break
		}
	}
	if filePath == "" {
		return &Response{OK: false, Error: fmt.Sprintf("entity not found: %s", strings.TrimSuffix(req.EntityPath, ".md"))}
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return &Response{OK: false, Error: fmt.Sprintf("reading entity file: %v", err)}
	}

	return &Response{
		OK:      true,
		Content: string(data),
	}
}

// dispatchEntityDelete deletes an entity page from the vault and removes it from the index.
// Requires Confirm=true.
func (d *Daemon) dispatchEntityDelete(req *Request) *Response {
	if req.EntityPath == "" {
		return &Response{OK: false, Error: "entity_path is required"}
	}
	if !req.Confirm {
		return &Response{OK: false, Error: "confirm must be true to delete an entity"}
	}
	if d.state.VaultPath == "" {
		return &Response{OK: false, Error: "daemon vault path not set"}
	}

	filePath, entityPath, err := pathutil.ResolveMarkdownUnderRoot(d.state.VaultPath, req.EntityPath)
	if err != nil {
		return &Response{OK: false, Error: err.Error()}
	}

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return &Response{OK: false, Error: fmt.Sprintf("entity not found: %s", filePath)}
	}

	// Collect backlinks to report as a warning in the response
	d.state.mu.RLock()
	backlinks, _ := d.state.Store.Backlinks(entityPath, "")
	d.state.mu.RUnlock()
	var backlinkPaths []string
	for _, b := range backlinks {
		backlinkPaths = append(backlinkPaths, b.RelPath)
	}

	// Remove from index first
	d.state.RemoveFile(filePath)

	// Delete the file
	if err := os.Remove(filePath); err != nil {
		return &Response{OK: false, Error: fmt.Sprintf("deleting entity file: %v", err)}
	}

	msg := fmt.Sprintf("deleted: %s", filePath)
	if len(backlinkPaths) > 0 {
		msg += fmt.Sprintf("\nwarning: %d page(s) linked to this entity: %s",
			len(backlinkPaths), strings.Join(backlinkPaths, ", "))
	}

	return &Response{
		OK:      true,
		Content: msg,
	}
}

// dispatchEntityList queries the store for Wiki/* documents, optionally filtered by entity_type.
func (d *Daemon) dispatchEntityList(req *Request) *Response {
	d.state.mu.RLock()
	results, err := d.state.Store.ListEntities(req.EntityType, 2000)
	d.state.mu.RUnlock()
	if err != nil {
		return &Response{OK: false, Error: fmt.Sprintf("listing entities: %v", err)}
	}

	out := make([]Result, 0, len(results))
	for _, r := range results {
		out = append(out, Result{
			Path:       r.Path,
			RelPath:    strings.TrimSuffix(r.RelPath, ".md"),
			Title:      r.Title,
			EntityType: r.EntityType,
			Score:      r.Rank,
			Abstract:   r.Abstract,
		})
	}
	return &Response{OK: true, Results: out}
}
