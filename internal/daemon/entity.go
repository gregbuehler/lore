package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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
	if !validEntityTypes[req.EntityType] {
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
	content := buildEntityContent(req.EntityType, title, today)

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
		content, err = setFrontmatterField(content, key, val)
		if err != nil {
			return &Response{OK: false, Error: fmt.Sprintf("updating frontmatter key %q: %v", key, err)}
		}
	}

	// Append changelog entry
	if req.Changelog != "" {
		today := time.Now().Format("2006-01-02")
		entry := fmt.Sprintf("- **%s**: %s", today, req.Changelog)
		content, err = appendToSection(content, "Change Log", entry)
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

// --------------------------------------------------------------------------
// Shared helpers (mirrors cmd/lore/entity.go logic — kept in sync manually)
// --------------------------------------------------------------------------

// validEntityTypes lists the accepted entity_type values.
var validEntityTypes = map[string]bool{
	"service":        true,
	"environment":    true,
	"person":         true,
	"tool":           true,
	"infrastructure": true,
	"organization":   true,
	"customer":       true,
	"vendor":         true,
	"concept":        true,
}

// buildEntityContent produces the full markdown content for a new entity file.
func buildEntityContent(entityType, title, today string) string {
	var sb strings.Builder

	sb.WriteString("---\n")
	sb.WriteString(fmt.Sprintf("entity_type: %s\n", entityType))
	sb.WriteString(fmt.Sprintf("title: \"%s\"\n", title))
	sb.WriteString(fmt.Sprintf("last_updated: %s\n", today))
	sb.WriteString("tags:\n")
	sb.WriteString(fmt.Sprintf("  - %s\n", entityType))
	sb.WriteString("---\n")
	sb.WriteString(fmt.Sprintf("# %s\n", title))
	sb.WriteString("\n")

	switch entityType {
	case "person":
		sb.WriteString("## What They Do\n\n")
	default:
		sb.WriteString("## What It Does\n\n")
	}

	sb.WriteString("## Known Issues\n\n")
	sb.WriteString("## Change Log\n\n")

	return sb.String()
}

// setFrontmatterField sets or adds a key in the YAML frontmatter block.
func setFrontmatterField(content, key, value string) (string, error) {
	if !strings.HasPrefix(content, "---\n") {
		return content, fmt.Errorf("file does not begin with YAML frontmatter (---)")
	}

	end := strings.Index(content[4:], "\n---")
	if end < 0 {
		return content, fmt.Errorf("frontmatter closing --- not found")
	}
	fmEnd := 4 + end
	fmBlock := content[4:fmEnd]
	afterFm := content[fmEnd:]

	lines := strings.Split(fmBlock, "\n")
	replaced := false
	for i, line := range lines {
		if strings.HasPrefix(line, key+":") {
			lines[i] = fmt.Sprintf("%s: %s", key, value)
			replaced = true
			break
		}
	}

	if !replaced {
		lines = append(lines, fmt.Sprintf("%s: %s", key, value))
	}

	newFm := strings.Join(lines, "\n")
	return "---\n" + newFm + afterFm, nil
}

// appendToSection appends text under the named ## section.
// If the section is not found, it is created at the end of the file.
func appendToSection(content, sectionName, text string) (string, error) {
	target := "## " + sectionName
	lines := strings.Split(content, "\n")

	sectionIdx := -1
	for i, line := range lines {
		if strings.TrimRight(line, " \t") == target {
			sectionIdx = i
			break
		}
	}

	if sectionIdx < 0 {
		if !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		content += fmt.Sprintf("\n%s\n\n%s\n", target, text)
		return content, nil
	}

	insertAt := len(lines)
	for i := sectionIdx + 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "## ") || strings.HasPrefix(lines[i], "# ") {
			insertAt = i
			break
		}
	}

	insertBefore := insertAt
	for insertBefore > sectionIdx+1 && strings.TrimSpace(lines[insertBefore-1]) == "" {
		insertBefore--
	}

	newLines := make([]string, 0, len(lines)+2)
	newLines = append(newLines, lines[:insertBefore]...)
	newLines = append(newLines, text)
	newLines = append(newLines, "")
	newLines = append(newLines, lines[insertBefore:]...)

	return strings.Join(newLines, "\n"), nil
}
