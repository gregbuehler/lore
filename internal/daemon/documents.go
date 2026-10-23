package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/gregbuehler/lore/internal/pathutil"
)

const documentPageSize = 200

func (d *Daemon) dispatchSources() *Response {
	if err := d.refreshSources(); err != nil {
		return &Response{OK: false, Error: "unable to refresh Lore subscriptions"}
	}
	if err := d.validateSources(); err != nil {
		return sourceError(err)
	}
	out := make([]Source, 0, len(d.state.sources))
	for _, source := range d.state.sources {
		out = append(out, source.Source)
	}
	return &Response{OK: true, Sources: out}
}

func (d *Daemon) validateSources() error {
	seen := make(map[string]bool, len(d.state.sources))
	for _, source := range d.state.sources {
		if strings.TrimSpace(source.Name) == "" || filepath.IsAbs(source.Name) || strings.ContainsAny(source.Name, `/\`) || seen[source.SourceID] {
			return fmt.Errorf("source names must be non-empty, path-free, and unique")
		}
		seen[source.SourceID] = true
	}
	return nil
}

func (d *Daemon) source(sourceID string) (sourceRoot, error) {
	if strings.TrimSpace(sourceID) == "" {
		return sourceRoot{}, fmt.Errorf("source_id is required")
	}
	if err := d.validateSources(); err != nil {
		return sourceRoot{}, err
	}
	for _, source := range d.state.sources {
		if source.SourceID == sourceID {
			return source, nil
		}
	}
	return sourceRoot{}, fmt.Errorf("unknown source_id")
}

func sourceError(err error) *Response {
	if strings.Contains(err.Error(), "unknown source_id") {
		return &Response{OK: false, Code: "source_not_found", Error: "source not found"}
	}
	return &Response{OK: false, Code: "invalid_source", Error: "source is invalid"}
}

func (d *Daemon) dispatchDocumentRead(req *Request) *Response {
	source, err := d.source(req.SourceID)
	if err != nil {
		return sourceError(err)
	}
	if req.DocumentID != "" {
		rel, err := d.pathForDocumentID(source.SourceID, req.DocumentID)
		if errors.Is(err, os.ErrNotExist) {
			return &Response{OK: false, Code: "not_found", Error: "document not found"}
		}
		if err != nil {
			return identityError(err)
		}
		req.RelPath = rel
	}
	path, rel, err := safeDocumentPath(source.root, req.RelPath, false)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Response{OK: false, Code: "not_found", Error: "document not found"}
		}
		return pathError(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Response{OK: false, Code: "not_found", Error: "document not found"}
		}
		return &Response{OK: false, Error: "unable to read document"}
	}
	doc, err := d.document(source, rel, content)
	if err != nil {
		return identityError(err)
	}
	return &Response{OK: true, Document: doc}
}

func (d *Daemon) dispatchDocumentResolve(req *Request) *Response {
	source, err := d.source(req.SourceID)
	if err != nil {
		return sourceError(err)
	}
	fromPath, fromRel, err := safeDocumentPath(source.root, req.FromPath, false)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Response{OK: false, Code: "not_found", Error: "source document not found"}
		}
		return pathError(err)
	}
	if _, err := os.Stat(fromPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Response{OK: false, Code: "not_found", Error: "source document not found"}
		}
		return pathError(err)
	}
	candidate, err := resolveDocumentLink(source, fromRel, req.Target)
	if err != nil {
		return pathError(err)
	}
	if candidate == "" {
		return &Response{OK: true, Resolution: &DocumentResolution{Resolved: false}}
	}
	path, rel, err := safeDocumentPath(source.root, candidate, false)
	if err != nil {
		return &Response{OK: true, Resolution: &DocumentResolution{Resolved: false}}
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return &Response{OK: true, Resolution: &DocumentResolution{Resolved: false}}
	}
	doc, err := d.document(source, rel, content)
	if err != nil {
		return identityError(err)
	}
	return &Response{OK: true, Resolution: &DocumentResolution{
		Resolved:   true,
		DocumentID: doc.DocumentID,
		SourceID:   source.SourceID,
		RelPath:    rel,
		Title:      doc.Title,
	}}
}

func (d *Daemon) dispatchDocumentWrite(req *Request) *Response {
	d.state.mu.Lock()
	defer d.state.mu.Unlock()
	source, err := d.source(req.SourceID)
	if err != nil {
		return sourceError(err)
	}
	if !source.Writable {
		return &Response{OK: false, Code: "readonly", Error: "source is read-only"}
	}
	path, rel, err := safeDocumentPath(source.root, req.RelPath, true)
	if err != nil {
		return pathError(err)
	}
	d.state.identityMu.Lock()
	identityErr := d.state.loadIdentitiesLocked()
	d.state.identityMu.Unlock()
	if identityErr != nil {
		return identityError(identityErr)
	}
	current, readErr := os.ReadFile(path)
	if readErr == nil {
		if req.BaseRevision == "" || revision(current) != req.BaseRevision {
			return &Response{OK: false, Code: "conflict", Error: "document revision conflict"}
		}
		if _, err := d.document(source, rel, current); err != nil {
			return identityError(err)
		}
	} else if errors.Is(readErr, os.ErrNotExist) {
		if req.BaseRevision != "" {
			return &Response{OK: false, Code: "conflict", Error: "document revision conflict"}
		}
	} else {
		return &Response{OK: false, Error: "unable to read document"}
	}
	if err := ensureDocumentParents(source.root, path); err != nil {
		return pathError(err)
	}
	if _, _, err := safeDocumentPath(source.root, req.RelPath, true); err != nil {
		return pathError(err)
	}
	mode := os.FileMode(0o644)
	if info, statErr := os.Stat(path); statErr == nil {
		mode = info.Mode().Perm()
	}
	if errors.Is(readErr, os.ErrNotExist) {
		if err := portableWriteExclusive(path, strings.NewReader(req.Content)); err != nil {
			if errors.Is(err, os.ErrExist) {
				return &Response{OK: false, Code: "conflict", Error: "document already exists"}
			}
			return &Response{OK: false, Error: "unable to create document"}
		}
	} else if err := pathutil.AtomicWriteFile(path, []byte(req.Content), mode); err != nil {
		return &Response{OK: false, Error: "unable to write document"}
	}
	if err := d.state.buildIndexLocked(); err != nil {
		return &Response{OK: false, Error: "document saved but indexing failed"}
	}
	doc, err := d.document(source, rel, []byte(req.Content))
	if err != nil {
		return identityError(err)
	}
	return &Response{OK: true, Document: doc}
}

func (d *Daemon) dispatchDocumentList(req *Request) *Response {
	source, err := d.source(req.SourceID)
	if err != nil {
		return sourceError(err)
	}
	offset := 0
	if req.Cursor != "" {
		offset, err = strconv.Atoi(req.Cursor)
		if err != nil || offset < 0 {
			return &Response{OK: false, Code: "invalid_cursor", Error: "invalid cursor"}
		}
	}
	limit := documentPageSize
	hiddenDocs := loreDocuments(source, req.Query)
	storeOffset := offset - len(hiddenDocs)
	if storeOffset < 0 {
		storeOffset = 0
	}
	d.state.mu.RLock()
	rows, err := d.state.Store.SourceDocuments(source.root, req.Query, limit+1, storeOffset)
	d.state.mu.RUnlock()
	if err != nil {
		return &Response{OK: false, Error: err.Error()}
	}
	all := make([]DocumentSummary, 0, len(hiddenDocs)+len(rows))
	if offset < len(hiddenDocs) {
		all = append(all, hiddenDocs[offset:]...)
	}
	for i := range all {
		path, _, err := safeDocumentPath(source.root, all[i].RelPath, false)
		if err != nil {
			return pathError(err)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return &Response{OK: false, Error: "unable to read document"}
		}
		doc, err := d.document(source, all[i].RelPath, content)
		if err != nil {
			return identityError(err)
		}
		all[i] = summaryOf(doc)
	}
	for _, row := range rows {
		path, _, pathErr := safeDocumentPath(source.root, filepath.ToSlash(row.RelPath), false)
		if pathErr != nil {
			continue
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			continue
		}
		doc, err := d.document(source, filepath.ToSlash(row.RelPath), content)
		if err != nil {
			return identityError(err)
		}
		all = append(all, summaryOf(doc))
	}
	hasMore := len(all) > limit
	if hasMore {
		all = all[:limit]
	}
	resp := &Response{OK: true, Documents: all, HasMore: hasMore}
	if hasMore {
		resp.NextCursor = strconv.Itoa(offset + limit)
	}
	return resp
}

func loreDocuments(source sourceRoot, query string) []DocumentSummary {
	var paths []string
	guide, _, guideErr := safeDocumentPath(source.root, ".lore/LORE.md", false)
	if guideErr == nil {
		if info, err := os.Lstat(guide); err == nil && info.Mode().IsRegular() {
			paths = append(paths, guide)
		}
	}
	skillsPath, _, skillsErr := safeDocumentPath(source.root, ".lore/skills/placeholder.md", false)
	if skillsErr == nil {
		skills := filepath.Dir(skillsPath)
		_ = filepath.WalkDir(skills, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if entry.Type()&os.ModeSymlink != 0 {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if entry.IsDir() && strings.HasPrefix(entry.Name(), ".") {
				return filepath.SkipDir
			}
			if !entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") && strings.HasSuffix(entry.Name(), ".md") {
				paths = append(paths, path)
			}
			return nil
		})
	}
	out := make([]DocumentSummary, 0, len(paths))
	for _, path := range paths {
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(source.root, path)
		if err != nil {
			continue
		}
		title := strings.TrimSuffix(filepath.Base(rel), ".md")
		for _, line := range strings.Split(string(content), "\n") {
			if strings.HasPrefix(line, "# ") {
				title = strings.TrimSpace(strings.TrimPrefix(line, "# "))
				break
			}
		}
		if query != "" && !matchesDocumentQuery(title+"\n"+string(content), query) {
			continue
		}
		out = append(out, DocumentSummary{
			SourceID: source.SourceID,
			RelPath:  filepath.ToSlash(rel),
			Title:    title,
			Excerpt:  excerpt(content),
			Revision: revision(content),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RelPath < out[j].RelPath })
	return out
}

func matchesDocumentQuery(content, query string) bool {
	text := strings.ToLower(content)
	for _, term := range strings.Fields(strings.ToLower(query)) {
		if !strings.Contains(text, term) {
			return false
		}
	}
	return true
}

func pathError(_ error) *Response {
	return &Response{OK: false, Code: "invalid_path", Error: "invalid or unsafe document path"}
}
func safeDocumentPath(root, input string, create bool) (string, string, error) {
	allowedLore := isAllowedLoreDocument(input)
	if input == "" || filepath.IsAbs(input) || strings.Contains(input, `\`) || strings.HasPrefix(input, "/") {
		return "", "", fmt.Errorf("relative markdown path is required")
	}
	if !strings.HasSuffix(input, ".md") || filepath.ToSlash(filepath.Clean(filepath.FromSlash(input))) != input {
		return "", "", fmt.Errorf("path must be a canonical relative .md path")
	}
	parts := strings.Split(input, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || (strings.HasPrefix(part, ".") && !allowedLore) {
			return "", "", fmt.Errorf("hidden and traversal paths are not allowed")
		}
		if windowsDrivePrefix(part) {
			return "", "", fmt.Errorf("drive paths are not allowed")
		}
		for _, c := range part {
			if c < 32 || c == 127 {
				return "", "", fmt.Errorf("control character in path")
			}
		}
	}
	if _, _, err := pathutil.ResolveMarkdownUnderRoot(root, input); err != nil {
		return "", "", err
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", "", err
	}
	rootResolved, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return "", "", err
	}
	candidate := filepath.Join(rootResolved, filepath.FromSlash(input))
	rel, err := filepath.Rel(rootResolved, candidate)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", "", fmt.Errorf("path escapes source root")
	}
	current := rootResolved
	for i, part := range parts {
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		last := i == len(parts)-1
		if os.IsNotExist(statErr) {
			if last || create {
				continue
			}
			return "", "", statErr
		}
		if statErr != nil {
			return "", "", statErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", "", fmt.Errorf("symlinks are not allowed")
		}
		if !last && !info.IsDir() {
			return "", "", fmt.Errorf("parent path is not a directory")
		}
		if last && !info.Mode().IsRegular() {
			return "", "", fmt.Errorf("document must be a regular file")
		}
	}
	return candidate, input, nil
}

func isAllowedLoreDocument(path string) bool {
	if path == ".lore/LORE.md" {
		return true
	}
	parts := strings.Split(path, "/")
	if len(parts) < 3 || parts[0] != ".lore" || parts[1] != "skills" || !strings.HasSuffix(path, ".md") {
		return false
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || (strings.HasPrefix(part, ".") && part != ".lore") {
			return false
		}
	}
	return true
}

func ensureDocumentParents(root, path string) error {
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, filepath.Dir(path))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path escapes source root")
	}
	current := root
	if rel == "." {
		return nil
	}
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if os.IsNotExist(statErr) {
			if err := os.Mkdir(current, 0o755); err != nil && !os.IsExist(err) {
				return err
			}
			info, statErr = os.Lstat(current)
		}
		if statErr != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("unsafe parent directory")
		}
	}
	return nil
}

func revision(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func makeDocument(sourceID, rel string, content []byte) *Document {
	title := strings.TrimSuffix(filepath.Base(rel), ".md")
	for _, line := range strings.Split(string(content), "\n") {
		if strings.HasPrefix(line, "# ") {
			title = strings.TrimSpace(strings.TrimPrefix(line, "# "))
			break
		}
	}
	return &Document{SourceID: sourceID, RelPath: rel, Title: title, Content: string(content), Revision: revision(content)}
}

func excerpt(content []byte) string {
	text := strings.TrimSpace(string(content))
	if len(text) > 240 {
		text = text[:240]
	}
	return text
}
