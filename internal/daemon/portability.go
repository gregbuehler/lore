package daemon

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	portableMaxArchive  = 64 << 20
	portableMaxExpanded = 256 << 20
	portableMaxFile     = 32 << 20
	portableMaxEntries  = 4096
)

// Portable paths deliberately have no exceptions for Lore's hidden metadata.
func portableRelative(input string) error {
	if input == "" || len(input) > 4096 || path.Clean(input) != input || filepath.IsAbs(input) || strings.ContainsAny(input, "\\\x00") {
		return fmt.Errorf("invalid relative path")
	}
	for _, part := range strings.Split(input, "/") {
		if part == "" || strings.HasPrefix(part, ".") || windowsDrivePrefix(part) {
			return fmt.Errorf("hidden or noncanonical path")
		}
		for _, c := range part {
			if c < 32 || c == 127 {
				return fmt.Errorf("control character in path")
			}
		}
	}
	return nil
}

// Reject Windows drive-qualified and drive-relative components without banning
// ordinary POSIX colons (for example, "Meeting: notes.md").
func windowsDrivePrefix(part string) bool {
	return len(part) >= 2 && part[1] == ':' && ((part[0] >= 'a' && part[0] <= 'z') || (part[0] >= 'A' && part[0] <= 'Z'))
}

func portableRoot(root string) (string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(root)
}

// Missing final components are allowed for creation; existing symlinks and
// special files are never followed, including symlinks pointing inside a source.
func safePortablePath(root, rel string, create bool) (string, error) {
	if err := portableRelative(rel); err != nil {
		return "", err
	}
	current, err := portableRoot(root)
	if err != nil {
		return "", err
	}
	parts := strings.Split(rel, "/")
	for i, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) && create {
			continue
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) || (i < len(parts)-1 && !info.IsDir()) {
			return "", fmt.Errorf("unsafe path component")
		}
	}
	return current, nil
}

func (d *Daemon) dispatchFolders(req *Request) *Response {
	d.state.mu.RLock()
	defer d.state.mu.RUnlock()
	source, err := d.source(req.SourceID)
	if err != nil {
		return sourceError(err)
	}
	root, err := portableRoot(source.root)
	if err != nil {
		return pathError(err)
	}
	folders := make([]Folder, 0)
	err = filepath.WalkDir(root, func(filename string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if filename == root {
			return nil
		}
		if strings.HasPrefix(entry.Name(), ".") {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			rel, err := filepath.Rel(root, filename)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if portableRelative(rel) != nil {
				return filepath.SkipDir
			}
			folders = append(folders, Folder{Path: rel})
		}
		return nil
	})
	if err != nil {
		return &Response{Error: "unable to list folders"}
	}
	return &Response{OK: true, Folders: folders}
}

func (d *Daemon) dispatchFolderCreate(req *Request) *Response {
	d.state.mu.Lock()
	defer d.state.mu.Unlock()
	source, err := d.source(req.SourceID)
	if err != nil {
		return sourceError(err)
	}
	if !source.Writable {
		return &Response{Code: "readonly", Error: "source is read-only"}
	}
	filename, err := safePortablePath(source.root, req.RelPath, true)
	if err != nil {
		return pathError(err)
	}
	if response := d.checkCreationReservation(source.SourceID, req.RelPath, ""); response != nil {
		return response
	}
	// The sentinel is never written; the shared parent helper includes the final
	// requested directory and rejects any preexisting non-directory component.
	if err := ensureDocumentParents(source.root, filepath.Join(filename, "folder")); err != nil {
		return pathError(err)
	}
	return &Response{OK: true, Path: req.RelPath}
}

func (d *Daemon) routePortableHTTP(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != "/api/v1/asset" && r.URL.Path != "/api/v1/assets" && r.URL.Path != "/api/v1/export" && r.URL.Path != "/api/v1/import" {
		return false
	}
	d.state.apiMu.Lock()
	defer d.state.apiMu.Unlock()
	allowed := r.Method == http.MethodGet
	if r.URL.Path == "/api/v1/import" {
		allowed = r.Method == http.MethodPost
	} else if r.URL.Path == "/api/v1/asset" {
		allowed = allowed || r.Method == http.MethodPost
	}
	if !allowed {
		writeHTTPError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return true
	}
	d.state.mu.Lock()
	defer d.state.mu.Unlock()
	source, err := d.source(r.URL.Query().Get("source"))
	if err != nil {
		writeDispatchError(w, sourceError(err))
		return true
	}
	if r.Method == http.MethodPost && !source.Writable {
		writeDispatchError(w, &Response{Code: "readonly", Error: "source is read-only"})
		return true
	}
	switch r.URL.Path {
	case "/api/v1/asset":
		d.portableAsset(w, r, source)
	case "/api/v1/assets":
		d.portableAssets(w, r, source)
	case "/api/v1/export":
		d.portableExport(w, r, source)
	case "/api/v1/import":
		d.portableImport(w, r, source)
	}
	return true
}

type portableAssetSummary struct {
	Path       string `json:"path"`
	Revision   string `json:"revision"`
	Size       int64  `json:"size"`
	ModifiedAt string `json:"modified_at"`
}

// Hash directly from canonical files; only manifest metadata and a fixed read
// buffer are retained, never attachment contents or a separate persistent index.
func portableAssetManifest(ctx context.Context, root string) ([]portableAssetSummary, error) {
	root, err := portableRoot(root)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("source root is not a directory")
	}
	assets := make([]portableAssetSummary, 0)
	buffer := make([]byte, 32<<10)
	err = filepath.WalkDir(root, func(filename string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		if filename == root {
			return nil
		}
		rel, err := filepath.Rel(root, filename)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if portableRelative(rel) != nil {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || strings.EqualFold(filepath.Ext(rel), ".md") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if _, err := safePortablePath(root, rel, false); err != nil {
			return err
		}
		file, err := os.Open(filename)
		if err != nil {
			return err
		}
		defer file.Close()
		opened, err := file.Stat()
		if err != nil {
			return err
		}
		if !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
			return fmt.Errorf("attachment changed while listing")
		}
		hash := sha256.New()
		var size int64
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			n, readErr := file.Read(buffer)
			if n > 0 {
				_, _ = hash.Write(buffer[:n])
				size += int64(n)
			}
			if errors.Is(readErr, io.EOF) {
				break
			}
			if readErr != nil {
				return readErr
			}
		}
		after, err := file.Stat()
		if err != nil {
			return err
		}
		if size != opened.Size() || after.Size() != opened.Size() || !after.ModTime().Equal(opened.ModTime()) {
			return fmt.Errorf("attachment changed while listing")
		}
		assets = append(assets, portableAssetSummary{
			Path: rel, Revision: fmt.Sprintf("%x", hash.Sum(nil)), Size: size,
			ModifiedAt: opened.ModTime().UTC().Format(time.RFC3339Nano),
		})
		return file.Close()
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(assets, func(i, j int) bool { return assets[i].Path < assets[j].Path })
	return assets, ctx.Err()
}

func (d *Daemon) portableAssets(w http.ResponseWriter, r *http.Request, source sourceRoot) {
	assets, err := portableAssetManifest(r.Context(), source.root)
	if err != nil {
		writeHTTPError(w, http.StatusInternalServerError, "read_failed", "unable to list attachments")
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeHTTPJSON(w, http.StatusOK, map[string]any{"assets": assets})
}

func portablePathError(w http.ResponseWriter, err error) {
	if errors.Is(err, os.ErrNotExist) {
		writeHTTPError(w, http.StatusNotFound, "not_found", "path not found")
		return
	}
	writeDispatchError(w, pathError(err))
}

func portableDownloadHeaders(w http.ResponseWriter, filename, contentType string, inline bool) {
	disposition := "attachment"
	if inline {
		disposition = "inline"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": filename}))
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
}

func (d *Daemon) portableAsset(w http.ResponseWriter, r *http.Request, source sourceRoot) {
	rel := r.URL.Query().Get("path")
	filename, err := safePortablePath(source.root, rel, r.Method == http.MethodPost)
	if err != nil {
		portablePathError(w, err)
		return
	}
	if r.Method == http.MethodGet {
		file, err := os.Open(filename)
		if err != nil {
			portablePathError(w, err)
			return
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			writeDispatchError(w, pathError(err))
			return
		}
		var sample [512]byte
		n, err := file.Read(sample[:])
		if err != nil && !errors.Is(err, io.EOF) {
			writeHTTPError(w, http.StatusInternalServerError, "read_failed", "unable to read attachment")
			return
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			writeHTTPError(w, http.StatusInternalServerError, "read_failed", "unable to read attachment")
			return
		}
		contentType := http.DetectContentType(sample[:n])
		inline := contentType == "image/png" || contentType == "image/jpeg" || contentType == "image/gif" || contentType == "image/webp"
		portableDownloadHeaders(w, path.Base(rel), contentType, inline)
		http.ServeContent(w, r, path.Base(rel), info.ModTime(), file)
		return
	}
	if _, err := os.Lstat(filename); err == nil {
		writeHTTPError(w, http.StatusConflict, "conflict", "attachment already exists")
		return
	} else if !errors.Is(err, os.ErrNotExist) {
		portablePathError(w, err)
		return
	}
	if response := d.checkCreationReservation(source.SourceID, rel, ""); response != nil {
		writeDispatchError(w, response)
		return
	}
	content, err := io.ReadAll(http.MaxBytesReader(w, r.Body, portableMaxFile))
	if err != nil {
		writeHTTPError(w, http.StatusRequestEntityTooLarge, "invalid_body", "attachment exceeds limit or could not be read")
		return
	}
	if err := ensureDocumentParents(source.root, filename); err != nil {
		portablePathError(w, err)
		return
	}
	if _, err := safePortablePath(source.root, rel, true); err != nil {
		portablePathError(w, err)
		return
	}
	if err := portableWriteExclusive(filename, bytes.NewReader(content)); err != nil {
		if errors.Is(err, os.ErrExist) {
			writeHTTPError(w, http.StatusConflict, "conflict", "attachment already exists")
		} else {
			writeHTTPError(w, http.StatusInternalServerError, "write_failed", "unable to save attachment")
		}
		return
	}
	if strings.HasSuffix(rel, ".md") {
		if _, err := d.document(source, rel, content); err != nil {
			writeHTTPError(w, http.StatusInternalServerError, "identity_failed", "attachment saved but document identity failed")
			return
		}
		if err := d.state.buildIndexLocked(); err != nil {
			writeHTTPError(w, http.StatusInternalServerError, "index_failed", "attachment saved but indexing failed")
			return
		}
	}
	writeHTTPJSON(w, http.StatusCreated, map[string]string{"path": rel})
}

func portableWriteExclusive(filename string, content io.Reader) error {
	// Only a complete, synced file becomes visible at the canonical path.
	// A crash during copying leaves a hidden staging file, never a partial note.
	file, err := os.CreateTemp(filepath.Dir(filename), ".lore-create-*")
	if err != nil {
		return err
	}
	staged := file.Name()
	defer os.Remove(staged)
	_, copyErr := io.Copy(file, content)
	var syncErr error
	if copyErr == nil {
		syncErr = file.Sync()
	}
	closeErr := file.Close()
	if err := errors.Join(copyErr, syncErr, closeErr); err != nil {
		return err
	}
	parent, err := os.Open(filepath.Dir(filename))
	if err != nil {
		return err
	}
	defer parent.Close()
	if err := os.Link(staged, filename); err != nil {
		return err
	}
	if err := parent.Sync(); err != nil {
		// Roll back only our published inode; never unlink an external replacement.
		stagedInfo, stagedErr := os.Stat(staged)
		finalInfo, finalErr := os.Lstat(filename)
		if stagedErr == nil && finalErr == nil && os.SameFile(stagedInfo, finalInfo) {
			return errors.Join(err, os.Remove(filename))
		}
		return err
	}
	return nil
}

type portableImportEntry struct {
	rel    string
	dir    bool
	staged string
}

// Preflight reads every byte (including CRC verification) into staging outside
// the source. No source directories, documents, or identities exist until all
// names, sizes, parent relationships and filesystem collisions are validated.
func preflightPortableImport(root, folder string, archive *zip.Reader, staging string) ([]portableImportEntry, error) {
	if len(archive.File) > portableMaxEntries {
		return nil, fmt.Errorf("too many archive entries")
	}
	if folder != "" {
		filename, err := safePortablePath(root, folder, true)
		if err != nil {
			return nil, err
		}
		if info, err := os.Lstat(filename); err == nil && !info.IsDir() {
			return nil, fmt.Errorf("destination is not a folder")
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	entries := make([]portableImportEntry, 0, len(archive.File))
	seen := make(map[string]bool, len(archive.File))
	var expanded uint64
	for i, file := range archive.File {
		dir := file.FileInfo().IsDir()
		rel := file.Name
		if dir {
			rel = strings.TrimSuffix(rel, "/")
		}
		if err := portableRelative(rel); err != nil {
			return nil, err
		}
		if file.Mode()&os.ModeSymlink != 0 || (!dir && !file.Mode().IsRegular()) || (dir && file.UncompressedSize64 != 0) {
			return nil, fmt.Errorf("archive contains nonregular entries")
		}
		if file.UncompressedSize64 > portableMaxFile || file.UncompressedSize64 > portableMaxExpanded-expanded {
			return nil, fmt.Errorf("archive expansion exceeds limit")
		}
		expanded += file.UncompressedSize64
		if folder != "" {
			rel = folder + "/" + rel
		}
		key := strings.ToLower(rel)
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("duplicate archive path")
		}
		seen[key] = dir
		filename, err := safePortablePath(root, rel, true)
		if err != nil {
			return nil, err
		}
		if _, err := os.Lstat(filename); err == nil {
			return nil, fmt.Errorf("%w: archive path already exists", os.ErrExist)
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		entry := portableImportEntry{rel: rel, dir: dir}
		reader, err := file.Open()
		if err != nil {
			return nil, err
		}
		entry.staged = filepath.Join(staging, fmt.Sprint(i))
		staged, err := os.OpenFile(entry.staged, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			_ = reader.Close()
			return nil, err
		}
		n, copyErr := io.Copy(staged, io.LimitReader(reader, portableMaxFile+1))
		err = errors.Join(copyErr, reader.Close(), staged.Close())
		if err != nil || n > portableMaxFile || uint64(n) != file.UncompressedSize64 {
			return nil, fmt.Errorf("invalid archive data or size")
		}
		entries = append(entries, entry)
	}
	// Validate after collecting every entry: files may precede their parents in
	// the central directory, and explicit directory records are optional.
	paths := make(map[string]string)
	for _, entry := range entries {
		for name := entry.rel; name != "."; name = path.Dir(name) {
			key := strings.ToLower(name)
			if previous, exists := paths[key]; exists && previous != name {
				return nil, fmt.Errorf("archive paths have ambiguous casing")
			}
			paths[key] = name
			if len(paths) > portableMaxEntries {
				return nil, fmt.Errorf("too many archive paths including parent folders")
			}
		}
	}
	for key := range seen {
		for parent := path.Dir(key); parent != "."; parent = path.Dir(parent) {
			if dir, exists := seen[parent]; exists && !dir {
				return nil, fmt.Errorf("archive file is also a parent directory")
			}
		}
	}
	return entries, nil
}

func (d *Daemon) portableImport(w http.ResponseWriter, r *http.Request, source sourceRoot) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, portableMaxArchive))
	if err != nil {
		writeHTTPError(w, http.StatusRequestEntityTooLarge, "invalid_archive", "archive exceeds compressed limit or could not be read")
		return
	}
	archive, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		writeHTTPError(w, http.StatusBadRequest, "invalid_archive", "invalid ZIP archive")
		return
	}
	staging, err := os.MkdirTemp("", "lore-import-*")
	if err != nil {
		writeHTTPError(w, http.StatusInternalServerError, "import_failed", "unable to stage archive")
		return
	}
	defer os.RemoveAll(staging)
	entries, err := preflightPortableImport(source.root, r.URL.Query().Get("folder"), archive, staging)
	if err != nil {
		status, code := http.StatusBadRequest, "invalid_archive"
		if errors.Is(err, os.ErrExist) {
			status, code = http.StatusConflict, "conflict"
		}
		writeHTTPError(w, status, code, "archive contains unsafe, conflicting, corrupt, or oversized entries")
		return
	}
	for _, entry := range entries {
		if response := d.checkCreationReservation(source.SourceID, entry.rel, ""); response != nil {
			writeDispatchError(w, response)
			return
		}
	}
	createdFiles := make([]string, 0, len(entries))
	createdDirs := make([]string, 0)
	committed := false
	defer func() {
		if committed {
			return
		}
		for _, rel := range createdFiles {
			_ = os.Remove(filepath.Join(source.root, filepath.FromSlash(rel)))
			if strings.HasSuffix(rel, ".md") {
				_ = d.deleteIdentity(source, rel)
			}
		}
		for i := len(createdDirs) - 1; i >= 0; i-- {
			_ = os.Remove(createdDirs[i])
		}
	}()
	for _, entry := range entries {
		filename, err := safePortablePath(source.root, entry.rel, true)
		if err == nil {
			directory := filepath.Dir(filename)
			if entry.dir {
				directory = filename
			}
			err = portableMakeDirectories(source.root, directory, &createdDirs)
		}
		if err == nil && !entry.dir {
			var file *os.File
			file, err = os.Open(entry.staged)
			if err == nil {
				err = portableWriteExclusive(filename, file)
				_ = file.Close()
				if err == nil {
					createdFiles = append(createdFiles, entry.rel)
				}
			}
		}
		if err != nil {
			writeHTTPError(w, http.StatusInternalServerError, "import_failed", "unable to apply archive")
			return
		}
	}
	for _, entry := range entries {
		if entry.dir || !strings.HasSuffix(entry.rel, ".md") {
			continue
		}
		content, err := os.ReadFile(entry.staged)
		if err == nil {
			_, err = d.document(source, entry.rel, content)
		}
		if err != nil {
			writeHTTPError(w, http.StatusInternalServerError, "import_failed", "unable to establish document identity")
			return
		}
	}
	committed = true
	if err := d.state.buildIndexLocked(); err != nil {
		writeHTTPError(w, http.StatusInternalServerError, "index_failed", "archive imported but indexing failed")
		return
	}
	writeHTTPJSON(w, http.StatusOK, map[string]int{"imported": len(createdFiles)})
}

func portableMakeDirectories(root, directory string, created *[]string) error {
	root, err := portableRoot(root)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, directory)
	if err != nil {
		return err
	}
	if rel == "." {
		return nil
	}
	if err := portableRelative(filepath.ToSlash(rel)); err != nil {
		return err
	}
	current := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(current, 0o755); err != nil {
				return err
			}
			*created = append(*created, current)
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("unsafe parent directory")
		}
	}
	return nil
}

func (d *Daemon) portableExport(w http.ResponseWriter, r *http.Request, source sourceRoot) {
	root, err := portableRoot(source.root)
	if err != nil {
		portablePathError(w, err)
		return
	}
	if rel := r.URL.Query().Get("path"); rel != "" {
		root, err = safePortablePath(source.root, rel, false)
		if err != nil {
			portablePathError(w, err)
			return
		}
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		writeDispatchError(w, pathError(err))
		return
	}
	staged, err := os.CreateTemp("", "lore-export-*.zip")
	if err != nil {
		writeHTTPError(w, http.StatusInternalServerError, "export_failed", "unable to stage export")
		return
	}
	defer os.Remove(staged.Name())
	defer staged.Close()
	archive := zip.NewWriter(staged)
	entries := 0
	var total int64
	err = filepath.WalkDir(root, func(filename string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if filename == root {
			return nil
		}
		if strings.HasPrefix(entry.Name(), ".") {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		rel, err := filepath.Rel(root, filename)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if err := portableRelative(rel); err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return nil
		}
		entries++
		if entries > portableMaxEntries || (!info.IsDir() && (info.Size() > portableMaxFile || info.Size() > portableMaxExpanded-total)) {
			return fmt.Errorf("export exceeds portability limits")
		}
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = rel
		header.Method = zip.Deflate
		if info.IsDir() {
			header.Name += "/"
			header.Method = zip.Store
		}
		writer, err := archive.CreateHeader(header)
		if err != nil || info.IsDir() {
			return err
		}
		// Recheck each file immediately before opening it; WalkDir never follows
		// symlinks and neither does portable path validation.
		if _, err := safePortablePath(root, rel, false); err != nil {
			return err
		}
		file, err := os.Open(filename)
		if err != nil {
			return err
		}
		n, copyErr := io.Copy(writer, io.LimitReader(file, portableMaxFile+1))
		err = errors.Join(copyErr, file.Close())
		total += n
		if err != nil || n > portableMaxFile || total > portableMaxExpanded {
			return fmt.Errorf("unable to export file within limits")
		}
		return nil
	})
	err = errors.Join(err, archive.Close())
	if err != nil {
		writeHTTPError(w, http.StatusBadRequest, "export_failed", "unable to export source within portability limits")
		return
	}
	info, err = staged.Stat()
	if err != nil {
		writeHTTPError(w, http.StatusInternalServerError, "export_failed", "unable to read export")
		return
	}
	if info.Size() > portableMaxArchive {
		writeHTTPError(w, http.StatusBadRequest, "export_failed", "export exceeds compressed archive limit")
		return
	}
	if _, err := staged.Seek(0, io.SeekStart); err != nil {
		writeHTTPError(w, http.StatusInternalServerError, "export_failed", "unable to read export")
		return
	}
	portableDownloadHeaders(w, source.Name+".zip", "application/zip", false)
	http.ServeContent(w, r, "export.zip", info.ModTime(), staged)
}
