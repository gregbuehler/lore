package daemon

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/gregbuehler/lore/internal/pathutil"
)

func documentConflict() *Response {
	return &Response{OK: false, Code: "conflict", Error: "document revision conflict"}
}

func (d *Daemon) dispatchDocumentDelete(req *Request) *Response {
	d.state.mu.Lock()
	defer d.state.mu.Unlock()
	source, err := d.source(req.SourceID)
	if err != nil {
		return sourceError(err)
	}
	if !source.Writable {
		return &Response{OK: false, Code: "readonly", Error: "source is read-only"}
	}
	path, rel, err := safeDocumentPath(source.root, req.RelPath, false)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Response{OK: false, Code: "not_found", Error: "document not found"}
		}
		return pathError(err)
	}
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &Response{OK: false, Code: "not_found", Error: "document not found"}
	}
	if err != nil {
		return &Response{OK: false, Error: "unable to read document"}
	}
	if req.BaseRevision == "" || revision(content) != req.BaseRevision {
		return documentConflict()
	}
	if err := os.Remove(path); err != nil {
		return &Response{OK: false, Error: "unable to delete document"}
	}
	if err := d.deleteIdentity(source, rel); err != nil {
		return &Response{OK: false, Error: "document deleted but identity update failed"}
	}
	if err := d.state.buildIndexLocked(); err != nil {
		return &Response{OK: false, Error: "document deleted but indexing failed"}
	}
	return &Response{OK: true}
}

type documentRewrite struct {
	file    linkedDocument
	content []byte
	staged  string
	applied bool
}

func (d *Daemon) dispatchDocumentMove(req *Request) *Response {
	d.state.mu.Lock()
	defer d.state.mu.Unlock()
	source, err := d.source(req.SourceID)
	if err != nil {
		return sourceError(err)
	}
	if !source.Writable {
		return &Response{OK: false, Code: "readonly", Error: "source is read-only"}
	}
	oldPath, oldRel, err := safeDocumentPath(source.root, req.RelPath, false)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Response{OK: false, Code: "not_found", Error: "document not found"}
		}
		return pathError(err)
	}
	newPath, newRel, err := safeDocumentPath(source.root, req.NewPath, true)
	if err != nil {
		return pathError(err)
	}
	original, err := os.ReadFile(oldPath)
	if errors.Is(err, os.ErrNotExist) {
		return &Response{OK: false, Code: "not_found", Error: "document not found"}
	}
	if err != nil {
		return &Response{OK: false, Error: "unable to read document"}
	}
	if req.BaseRevision == "" || revision(original) != req.BaseRevision {
		return documentConflict()
	}
	if oldRel == newRel {
		doc, err := d.document(source, oldRel, original)
		if err != nil {
			return &Response{OK: false, Error: "unable to read document identity"}
		}
		return &Response{OK: true, Document: doc}
	}
	if _, err := os.Lstat(newPath); err == nil {
		return &Response{OK: false, Code: "conflict", Error: "destination already exists"}
	} else if !errors.Is(err, os.ErrNotExist) {
		return pathError(err)
	}
	index, err := scanDocumentLinks(source)
	if err != nil {
		return &Response{OK: false, Error: "unable to read source documents"}
	}
	var moved linkedDocument
	var rewrites []documentRewrite
	var movedContent []byte
	for _, file := range index.docs {
		content := rewriteDocumentLinks(index, file, oldRel, newRel)
		if file.rel == oldRel {
			moved, movedContent = file, content
		} else if string(content) != string(file.content) {
			rewrites = append(rewrites, documentRewrite{file: file, content: content})
		}
	}
	if moved.path == "" || revision(moved.content) != req.BaseRevision {
		return documentConflict()
	}
	// Materialize identity before changing paths, preserving its original creation
	// time and ID even when the document has never been opened through the API.
	if _, err := d.document(source, oldRel, moved.content); err != nil {
		return &Response{OK: false, Error: "unable to read document identity"}
	}
	if err := ensureDocumentParents(source.root, newPath); err != nil {
		return pathError(err)
	}
	if _, _, err := safeDocumentPath(source.root, newRel, true); err != nil {
		return pathError(err)
	}
	stagedMove, err := stageDocument(newPath, movedContent, moved.mode)
	if err != nil {
		return &Response{OK: false, Error: "unable to prepare document move"}
	}
	defer os.Remove(stagedMove)
	defer func() {
		for _, change := range rewrites {
			if change.staged != "" {
				_ = os.Remove(change.staged)
			}
		}
	}()
	for i := range rewrites {
		if _, err := d.document(source, rewrites[i].file.rel, rewrites[i].file.content); err != nil {
			return &Response{OK: false, Error: "unable to read linked document identity"}
		}
		rewrites[i].staged, err = stageDocument(rewrites[i].file.path, rewrites[i].content, rewrites[i].file.mode)
		if err != nil {
			return &Response{OK: false, Error: "unable to prepare link rewrites"}
		}
	}
	// The API lock serializes writers. Recheck every planned revision after the
	// scan/staging, catching filesystem edits before applying any changes.
	if !unchangedDocument(source, moved) {
		return documentConflict()
	}
	for _, change := range rewrites {
		if !unchangedDocument(source, change.file) {
			return documentConflict()
		}
	}
	if _, _, err := safeDocumentPath(source.root, newRel, true); err != nil {
		return pathError(err)
	}
	// Hard-linking the fully written temporary file provides atomic no-replace
	// publication on both Darwin and Linux; Rename would overwrite a collision.
	if err := os.Link(stagedMove, newPath); err != nil {
		if errors.Is(err, os.ErrExist) {
			return &Response{OK: false, Code: "conflict", Error: "destination already exists"}
		}
		return &Response{OK: false, Error: "unable to move document"}
	}
	oldRemoved := false
	rollback := func(response *Response) *Response {
		failed := false
		for i := len(rewrites) - 1; i >= 0; i-- {
			change := rewrites[i]
			if !change.applied {
				continue
			}
			current, readErr := os.ReadFile(change.file.path)
			if readErr != nil || revision(current) != revision(change.content) {
				failed = true
				continue
			}
			if err := pathutil.AtomicWriteFile(change.file.path, change.file.content, change.file.mode); err != nil {
				failed = true
			}
		}
		if oldRemoved {
			backup, err := stageDocument(oldPath, moved.content, moved.mode)
			if err != nil {
				failed = true
			} else {
				if err := os.Link(backup, oldPath); err != nil {
					failed = true
				}
				_ = os.Remove(backup)
			}
		}
		current, err := os.ReadFile(newPath)
		if err != nil || revision(current) != revision(movedContent) {
			failed = true
		} else if err := os.Remove(newPath); err != nil {
			failed = true
		}
		if failed {
			return &Response{OK: false, Error: "move interrupted; unable to restore every affected document"}
		}
		return response
	}
	for i := range rewrites {
		change := &rewrites[i]
		if !unchangedDocument(source, change.file) {
			return rollback(documentConflict())
		}
		if err := os.Rename(change.staged, change.file.path); err != nil {
			return rollback(&Response{OK: false, Error: "unable to rewrite document links"})
		}
		change.applied = true
	}
	if !unchangedDocument(source, moved) {
		return rollback(documentConflict())
	}
	if err := os.Remove(oldPath); err != nil {
		return rollback(&Response{OK: false, Error: "unable to remove original document"})
	}
	oldRemoved = true
	if err := d.moveIdentity(source, oldRel, newRel); err != nil {
		return rollback(&Response{OK: false, Error: "unable to move document identity"})
	}
	doc, err := d.document(source, newRel, movedContent)
	if err != nil {
		return &Response{OK: false, Error: "document moved but identity update failed"}
	}
	for _, change := range rewrites {
		if _, err := d.document(source, change.file.rel, change.content); err != nil {
			return &Response{OK: false, Error: "document moved but linked document identity update failed"}
		}
	}
	if err := d.state.buildIndexLocked(); err != nil {
		return &Response{OK: false, Error: "document moved but indexing failed"}
	}
	return &Response{OK: true, Document: doc}
}

func unchangedDocument(source sourceRoot, file linkedDocument) bool {
	path, _, err := safeDocumentPath(source.root, file.rel, false)
	if err != nil {
		return false
	}
	content, err := os.ReadFile(path)
	return err == nil && revision(content) == revision(file.content)
}

func stageDocument(path string, content []byte, mode os.FileMode) (string, error) {
	file, err := os.CreateTemp(filepath.Dir(path), ".lore-move-*")
	if err != nil {
		return "", err
	}
	name := file.Name()
	ok := false
	defer func() {
		_ = file.Close()
		if !ok {
			_ = os.Remove(name)
		}
	}()
	if err := file.Chmod(mode); err != nil {
		return "", err
	}
	if _, err := file.Write(content); err != nil {
		return "", err
	}
	if err := file.Sync(); err != nil {
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	ok = true
	return name, nil
}

func rewriteDocumentLinks(index *documentLinkIndex, file linkedDocument, oldRel, newRel string) []byte {
	text := string(file.content)
	var out strings.Builder
	last := 0
	for _, link := range markdownDocumentLinks(text) {
		raw := text[link.start:link.end]
		target := raw
		if link.wiki {
			target = "[[" + raw + "]]"
		}
		resolved, err := index.resolve(file.rel, target)
		if err != nil {
			continue
		}
		moving := file.rel == oldRel
		if resolved != oldRel && !moving {
			continue
		}
		if resolved == oldRel {
			resolved = newRel
		}
		from := file.rel
		if moving {
			from = newRel
		}
		cut := strings.IndexAny(raw, "#?")
		if link.wiki {
			cut = strings.IndexAny(raw, "#?|")
			if cut > 0 && raw[cut] == '|' && raw[cut-1] == '\\' {
				cut--
			}
		}
		if cut < 0 {
			cut = len(raw)
		}
		pathPart, suffix := strings.TrimSpace(raw[:cut]), raw[cut:]
		if pathPart == "" {
			continue
		}
		var replacement string
		if link.wiki {
			if resolved == "" {
				continue
			}
			replacement = strings.TrimSuffix(resolved, ".md")
			// A stem ending in .md needs an explicit extension to distinguish
			// the literal filename suffix from the optional wiki extension.
			if strings.HasSuffix(pathPart, ".md") || strings.HasSuffix(replacement, ".md") {
				replacement += ".md"
			}
			replacement = markdownPathEscaper.Replace((&url.URL{Path: replacement}).EscapedPath())
		} else {
			pathPart = markdownPathUnescaper.Replace(pathPart)
			// Rebase attachments and unresolved relative destinations too: their
			// location must not change merely because their containing note moved.
			if resolved == "" {
				parsed, parseErr := url.Parse(pathPart)
				if !moving || parseErr != nil || parsed.IsAbs() || parsed.Host != "" || strings.HasPrefix(pathPart, "/") {
					continue
				}
				decoded, decodeErr := url.PathUnescape(pathPart)
				if decodeErr != nil || strings.Contains(decoded, `\`) {
					continue
				}
				resolved = filepath.ToSlash(filepath.Clean(filepath.Join(filepath.Dir(file.rel), decoded)))
				if resolved == ".." || strings.HasPrefix(resolved, "../") {
					continue
				}
			}
			relative, relErr := filepath.Rel(filepath.Dir(filepath.FromSlash(from)), filepath.FromSlash(resolved))
			if relErr != nil {
				continue
			}
			replacement = (&url.URL{Path: filepath.ToSlash(relative)}).EscapedPath()
			// Parentheses delimit Markdown destinations, even inside URL paths.
			replacement = markdownPathEscaper.Replace(replacement)
		}
		replacement += suffix
		if replacement == raw {
			continue
		}
		out.WriteString(text[last:link.start])
		out.WriteString(replacement)
		last = link.end
	}
	if last == 0 {
		return file.content
	}
	out.WriteString(text[last:])
	return []byte(out.String())
}
