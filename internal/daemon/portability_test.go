package daemon

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

type portableTestEntry struct {
	name    string
	content string
	mode    os.FileMode
}

func portableTestZIP(t *testing.T, entries ...portableTestEntry) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Store}
		if entry.mode != 0 {
			header.SetMode(entry.mode)
		}
		file, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(file, entry.content); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func portableTestRequest(d *Daemon, method, endpoint string, body []byte) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, endpoint, bytes.NewReader(body))
	recorder := httptest.NewRecorder()
	if !d.routePortableHTTP(recorder, request) {
		panic("unhandled portability test route")
	}
	return recorder
}

func portableTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := make(map[string]string)
	err := filepath.Walk(root, func(filename string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if filename == root {
			return nil
		}
		rel, err := filepath.Rel(root, filename)
		if err != nil {
			return err
		}
		if info.IsDir() {
			out[filepath.ToSlash(rel)+"/"] = ""
		} else if info.Mode()&os.ModeSymlink != 0 {
			out[filepath.ToSlash(rel)] = "symlink"
		} else {
			content, err := os.ReadFile(filename)
			if err != nil {
				return err
			}
			out[filepath.ToSlash(rel)] = string(content)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestPortableAssetManifestCanonicalFiles(t *testing.T) {
	// Exercise the authenticated handler as well as the portable route helper.
	d, vault, library := documentTestDaemon(t)
	handler := d.handleHTTP("manifest-token")
	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/api/v1/assets?source=vault", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated manifest = %d", unauthorized.Code)
	}
	if err := syscall.Mkfifo(filepath.Join(vault, "special.pipe"), 0600); err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{
		"z.bin":               []byte{0, 255, 1},
		"Attachments/a.png":   []byte("binary\x00image"),
		"Attachments/empty":   {},
		"note.md":             []byte("# Note"),
		"upper.MD":            []byte("# Note"),
		".hidden/secret.bin":  []byte("secret"),
		"Attachments/.secret": []byte("secret"),
	}
	for name, content := range files {
		filename := filepath.Join(vault, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, content, 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(library, "other.bin"), []byte("other"), 0644); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{"linked": library, "linked.bin": filepath.Join(vault, "z.bin")} {
		if err := os.Symlink(target, filepath.Join(vault, name)); err != nil {
			t.Fatal(err)
		}
	}
	readManifest := func(source string) []portableAssetSummary {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "/api/v1/assets?source="+url.QueryEscape(source), nil)
		request.Header.Set("Authorization", "Bearer manifest-token")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("manifest = %d: %s", response.Code, response.Body)
		}
		var result struct {
			Assets []portableAssetSummary `json:"assets"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result.Assets
	}
	assets := readManifest("vault")
	wantPaths := []string{"Attachments/a.png", "Attachments/empty", "z.bin"}
	if len(assets) != len(wantPaths) {
		t.Fatalf("manifest = %+v", assets)
	}
	for i, asset := range assets {
		if asset.Path != wantPaths[i] {
			t.Fatalf("unsorted or unsafe manifest: %+v", assets)
		}
		content := files[asset.Path]
		info, err := os.Stat(filepath.Join(vault, filepath.FromSlash(asset.Path)))
		if err != nil {
			t.Fatal(err)
		}
		if asset.Revision != fmt.Sprintf("%x", sha256.Sum256(content)) || asset.Size != int64(len(content)) || asset.ModifiedAt != info.ModTime().UTC().Format(time.RFC3339Nano) {
			t.Fatalf("incorrect metadata: %+v", asset)
		}
	}
	if err := os.WriteFile(filepath.Join(vault, "z.bin"), []byte{0, 255, 2}, 0644); err != nil {
		t.Fatal(err)
	}
	changed := readManifest("vault")
	if changed[2].Revision == assets[2].Revision || changed[2].Size != assets[2].Size {
		t.Fatal("same-sized binary change did not change revision")
	}
	scoped := readManifest("library:notes")
	if len(scoped) != 1 || scoped[0].Path != "other.bin" {
		t.Fatalf("source boundary violated: %+v", scoped)
	}
	if response := portableTestRequest(d, http.MethodPost, "/api/v1/assets?source=vault", nil); response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("manifest mutation = %d", response.Code)
	}
	if response := portableTestRequest(d, http.MethodGet, "/api/v1/assets?source=missing", nil); response.Code != http.StatusNotFound {
		t.Fatalf("unknown source = %d", response.Code)
	}
}

func TestPortableAssetManifestErrorsAndCancellation(t *testing.T) {
	root := t.TempDir()
	assets, err := portableAssetManifest(context.Background(), root)
	if err != nil || assets == nil || len(assets) != 0 {
		t.Fatalf("empty manifest = %+v, %v", assets, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := portableAssetManifest(ctx, root); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation = %v", err)
	}
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	if _, err := portableAssetManifest(context.Background(), root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing root reported as complete manifest: %v", err)
	}
	d, vault, _ := documentTestDaemon(t)
	if err := os.RemoveAll(vault); err != nil {
		t.Fatal(err)
	}
	if response := portableTestRequest(d, http.MethodGet, "/api/v1/assets?source=vault", nil); response.Code != http.StatusInternalServerError {
		t.Fatalf("unreadable manifest = %d: %s", response.Code, response.Body)
	}
}

func TestPortableImportPreflightsUnsafeArchiveWithoutMutation(t *testing.T) {
	cases := map[string][]portableTestEntry{
		"traversal":           {{name: "../escape.md"}},
		"absolute":            {{name: "/escape.md"}},
		"backslash":           {{name: `folder\escape.md`}},
		"drive":               {{name: "C:/escape.md"}},
		"drive relative":      {{name: "C:escape.md"}},
		"nested drive":        {{name: "folder/C:escape.md"}},
		"UNC":                 {{name: "//server/share/escape.md"}},
		"backslash UNC":       {{name: `\\server\share\escape.md`}},
		"dot component":       {{name: "folder/./note.md"}},
		"double slash":        {{name: "folder//note.md"}},
		"hidden credential":   {{name: "folder/.env", content: "secret"}},
		"git":                 {{name: ".git/config"}},
		"Lore metadata":       {{name: ".lore/LORE.md"}},
		"symlink":             {{name: "escape", content: "../outside", mode: os.ModeSymlink | 0o777}},
		"device":              {{name: "device", mode: os.ModeDevice | 0o600}},
		"duplicate":           {{name: "duplicate.md"}, {name: "duplicate.md"}},
		"case duplicate":      {{name: "Note.md"}, {name: "note.md"}},
		"case parent alias":   {{name: "Folder/a.md"}, {name: "folder/b.md"}},
		"file parent":         {{name: "parent"}, {name: "parent/child.md"}},
		"reverse file parent": {{name: "parent/child.md"}, {name: "parent"}},
		"duplicate directory": {{name: "empty/"}, {name: "empty/"}},
		"control character":   {{name: "bad\nname.md"}},
		"DEL character":       {{name: "bad\x7fname.md"}},
		"colon duplicate":     {{name: "Meeting: notes.md"}, {name: "Meeting: notes.md"}},
		"space duplicate":     {{name: " note .md"}, {name: " note .md"}},
		"colon case alias":    {{name: "Meeting: notes/a.md"}, {name: "meeting: notes/b.md"}},
	}
	for name, entries := range cases {
		t.Run(name, func(t *testing.T) {
			d, vault, library := documentTestDaemon(t)
			before := portableTree(t, vault)
			// A valid entry before the invalid one must not leak through preflight.
			entries = append([]portableTestEntry{{name: "first.md", content: "# First"}}, entries...)
			response := portableTestRequest(d, http.MethodPost, "/api/v1/import?source=vault&folder=new/nested", portableTestZIP(t, entries...))
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body)
			}
			if after := portableTree(t, vault); !reflect.DeepEqual(before, after) {
				t.Fatalf("invalid archive mutated source: before=%v after=%v", before, after)
			}
			if got := portableTree(t, library); len(got) != 0 {
				t.Fatalf("other source mutated: %v", got)
			}
		})
	}
}

func TestPortableImportCorruptDataIsAtomic(t *testing.T) {
	d, vault, _ := documentTestDaemon(t)
	before := portableTree(t, vault)
	archive := portableTestZIP(t, portableTestEntry{name: "valid.md", content: "# Valid"}, portableTestEntry{name: "corrupt.md", content: "corrupt-me"})
	offset := bytes.Index(archive, []byte("corrupt-me"))
	if offset < 0 {
		t.Fatal("missing archive payload")
	}
	archive[offset] ^= 1
	response := portableTestRequest(d, http.MethodPost, "/api/v1/import?source=vault", archive)
	if response.Code != http.StatusBadRequest || !reflect.DeepEqual(before, portableTree(t, vault)) {
		t.Fatalf("corrupt archive applied: status=%d body=%s", response.Code, response.Body)
	}
}

func TestPortableImportRejectsCollisionsAndSymlinkParents(t *testing.T) {
	for _, name := range []string{"same.md", "existing/", "linked/note.md"} {
		t.Run(name, func(t *testing.T) {
			d, vault, library := documentTestDaemon(t)
			if err := os.Mkdir(filepath.Join(vault, "existing"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(library, filepath.Join(vault, "linked")); err != nil {
				t.Fatal(err)
			}
			before := portableTree(t, vault)
			archive := portableTestZIP(t, portableTestEntry{name: "new.md", content: "# New"}, portableTestEntry{name: name})
			response := portableTestRequest(d, http.MethodPost, "/api/v1/import?source=vault", archive)
			want := http.StatusConflict
			if strings.HasPrefix(name, "linked/") {
				want = http.StatusBadRequest
			}
			if response.Code != want || !reflect.DeepEqual(before, portableTree(t, vault)) || len(portableTree(t, library)) != 0 {
				t.Fatalf("collision handling: status=%d body=%s", response.Code, response.Body)
			}
		})
	}
}

func TestPortableImportBounds(t *testing.T) {
	root, staging := t.TempDir(), t.TempDir()
	tooMany := &zip.Reader{File: make([]*zip.File, portableMaxEntries+1)}
	tooLarge := &zip.Reader{File: []*zip.File{{FileHeader: zip.FileHeader{Name: "large.bin", UncompressedSize64: portableMaxFile + 1}}}}
	directoryPayload := &zip.Reader{File: []*zip.File{{FileHeader: zip.FileHeader{Name: "empty/", UncompressedSize64: 1}}}}
	for _, archive := range []*zip.Reader{tooMany, tooLarge, directoryPayload} {
		if _, err := preflightPortableImport(root, "", archive, staging); err == nil {
			t.Fatal("oversized archive accepted")
		}
	}
	if len(portableTree(t, root)) != 0 {
		t.Fatal("bounded archive mutated source")
	}
}

func TestPortableFoldersPreserveEmptyNestedDirectories(t *testing.T) {
	d, vault, library := documentTestDaemon(t)
	response := d.dispatchFolderCreate(&Request{SourceID: "vault", RelPath: "one/two/empty"})
	if !response.OK || response.Path != "one/two/empty" {
		t.Fatalf("create = %#v", response)
	}
	response = d.dispatchFolders(&Request{SourceID: "vault"})
	want := []Folder{{Path: "one"}, {Path: "one/two"}, {Path: "one/two/empty"}}
	if !response.OK || !reflect.DeepEqual(response.Folders, want) {
		t.Fatalf("folders = %#v", response)
	}
	if err := os.Symlink(library, filepath.Join(vault, "linked")); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"../escape", ".secret", "one/.git", "linked/escape", "same.md/child", "one//two", ""} {
		if response := d.dispatchFolderCreate(&Request{SourceID: "vault", RelPath: rel}); response.OK {
			t.Errorf("unsafe folder %q accepted", rel)
		}
	}
	if response := d.dispatchFolderCreate(&Request{SourceID: "library:notes", RelPath: "denied"}); response.OK || response.Code != "readonly" {
		t.Fatalf("readonly create = %#v", response)
	}
	if got := portableTree(t, library); len(got) != 0 {
		t.Fatalf("folders escaped source: %v", got)
	}
}

func TestPortableAttachmentsAreScopedAndNeverOverwrite(t *testing.T) {
	d, vault, library := documentTestDaemon(t)
	payload := []byte("\x89PNG\r\n\x1a\nimage payload")
	endpoint := "/api/v1/asset?source=vault&path=attachments/image.png"
	response := portableTestRequest(d, http.MethodPost, endpoint, payload)
	if response.Code != http.StatusCreated {
		t.Fatalf("upload = %d: %s", response.Code, response.Body)
	}
	response = portableTestRequest(d, http.MethodPost, endpoint, []byte("replacement"))
	if response.Code != http.StatusConflict {
		t.Fatalf("overwrite = %d", response.Code)
	}
	response = portableTestRequest(d, http.MethodGet, endpoint, nil)
	if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), payload) || response.Header().Get("Content-Type") != "image/png" || !strings.HasPrefix(response.Header().Get("Content-Disposition"), "inline") || response.Header().Get("X-Content-Type-Options") != "nosniff" || response.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("download = %d headers=%v body=%q", response.Code, response.Header(), response.Body.String())
	}
	response = portableTestRequest(d, http.MethodGet, strings.Replace(endpoint, "source=vault", "source=library:notes", 1), nil)
	if response.Code != http.StatusNotFound {
		t.Fatalf("cross-source attachment = %d", response.Code)
	}
	response = portableTestRequest(d, http.MethodPost, "/api/v1/asset?source=library:notes&path=file.bin", payload)
	if response.Code != http.StatusForbidden {
		t.Fatalf("read-only upload = %d", response.Code)
	}
	if err := os.WriteFile(filepath.Join(library, "private.bin"), []byte("private"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(library, filepath.Join(vault, "linked")); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"../private.bin", ".git/config", ".env", ".lore/LORE.md", "linked/private.bin"} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			response = portableTestRequest(d, method, "/api/v1/asset?source=vault&path="+url.QueryEscape(rel), payload)
			if response.Code != http.StatusBadRequest {
				t.Errorf("unsafe %s %q = %d", method, rel, response.Code)
			}
		}
	}
}

func TestPortableActiveAttachmentsUseSafeDisposition(t *testing.T) {
	d, _, _ := documentTestDaemon(t)
	name := "quoted\"name.html"
	endpoint := "/api/v1/asset?source=vault&path=" + url.QueryEscape(name)
	payload := []byte("<!DOCTYPE html><script>alert(1)</script>")
	if response := portableTestRequest(d, http.MethodPost, endpoint, payload); response.Code != http.StatusCreated {
		t.Fatalf("upload = %d: %s", response.Code, response.Body)
	}
	response := portableTestRequest(d, http.MethodGet, endpoint, nil)
	disposition, parameters, err := mime.ParseMediaType(response.Header().Get("Content-Disposition"))
	if response.Code != http.StatusOK || err != nil || disposition != "attachment" || parameters["filename"] != name || response.Header().Get("Content-Security-Policy") == "" {
		t.Fatalf("unsafe disposition: status=%d headers=%v error=%v", response.Code, response.Header(), err)
	}
}

func TestPortableExportImportRoundTripAndIdentity(t *testing.T) {
	d, vault, library := documentTestDaemon(t)
	d.state.sources[1].Writable = true
	if response := d.dispatchFolderCreate(&Request{SourceID: "vault", RelPath: "portable/nested/empty"}); !response.OK {
		t.Fatal(response.Error)
	}
	for name, content := range map[string]string{"portable/nested/note.md": "# Portable\n\nA note", "portable/image.bin": "\x00\x01\xff", "portable/.env": "private"} {
		if err := os.WriteFile(filepath.Join(vault, filepath.FromSlash(name)), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(vault, "same.md"), filepath.Join(vault, "portable", "linked.md")); err != nil {
		t.Fatal(err)
	}
	response := portableTestRequest(d, http.MethodGet, "/api/v1/export?source=vault&path=portable", nil)
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("export = %d: %s", response.Code, response.Body)
	}
	archive := append([]byte(nil), response.Body.Bytes()...)
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(reader.File))
	for _, file := range reader.File {
		names = append(names, file.Name)
	}
	want := []string{"image.bin", "nested/", "nested/empty/", "nested/note.md"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("export layout = %v, want %v", names, want)
	}
	response = portableTestRequest(d, http.MethodPost, "/api/v1/import?source=library:notes&folder=restored", archive)
	if response.Code != http.StatusOK {
		t.Fatalf("import = %d: %s", response.Code, response.Body)
	}
	var result struct {
		Imported int `json:"imported"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.Imported != 2 {
		t.Fatalf("import result = %s, error=%v", response.Body, err)
	}
	if info, err := os.Stat(filepath.Join(library, "restored", "nested", "empty")); err != nil || !info.IsDir() {
		t.Fatalf("empty folder lost: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(library, "restored", "image.bin"))
	if err != nil || string(content) != "\x00\x01\xff" {
		t.Fatalf("attachment roundtrip: %q, %v", content, err)
	}
	document := d.dispatchDocumentRead(&Request{SourceID: "library:notes", RelPath: "restored/nested/note.md"})
	if !document.OK || document.Document == nil || document.Document.DocumentID == "" || document.Document.SourceID != "library:notes" {
		t.Fatalf("imported document identity = %#v", document)
	}
	if _, err := os.Stat(filepath.Join(vault, "restored")); !os.IsNotExist(err) {
		t.Fatalf("import crossed source boundary: %v", err)
	}
}

func TestPortableImportRejectsUnsafeDestinationAndReadonlySource(t *testing.T) {
	d, vault, _ := documentTestDaemon(t)
	archive := portableTestZIP(t, portableTestEntry{name: "note.md", content: "# Note"})
	before := portableTree(t, vault)
	for _, folder := range []string{"../escape", ".git", "same.md"} {
		response := portableTestRequest(d, http.MethodPost, "/api/v1/import?source=vault&folder="+url.QueryEscape(folder), archive)
		if response.Code != http.StatusBadRequest || !reflect.DeepEqual(before, portableTree(t, vault)) {
			t.Fatalf("unsafe folder %q: %d %s", folder, response.Code, response.Body)
		}
	}
	response := portableTestRequest(d, http.MethodPost, "/api/v1/import?source=library:notes", archive)
	if response.Code != http.StatusForbidden {
		t.Fatalf("read-only import = %d", response.Code)
	}
	response = portableTestRequest(d, http.MethodGet, "/api/v1/export?source=vault&path=../escape", nil)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unsafe export = %d", response.Code)
	}
}

func TestPortableCanonicalFilenamesRoundTripWithoutRenaming(t *testing.T) {
	d, vault, library := documentTestDaemon(t)
	d.state.sources[1].Writable = true
	documents := map[string]string{
		"Meeting: notes.md":          "# Meeting",
		" leading.md":                "# Leading",
		"trailing .md":               "# Trailing stem",
		" folder: notes / 雪 #|?%.md": "# Nested reserved characters",
		"%2e%2e.md":                  "# Literal percent encoding",
	}
	for name, content := range documents {
		response := d.dispatchDocumentWrite(&Request{SourceID: "vault", RelPath: name, Content: content})
		if !response.OK || response.Document == nil || response.Document.RelPath != name {
			t.Fatalf("create %q = %#v", name, response)
		}
	}
	folder := " empty: folder "
	if response := d.dispatchFolderCreate(&Request{SourceID: "vault", RelPath: folder}); !response.OK || response.Path != folder {
		t.Fatalf("folder create = %#v", response)
	}
	assets := map[string]string{
		" assets: images / image: one.bin ": "binary\x00payload",
		" assets: images / ":                "space filename",
	}
	for name, content := range assets {
		endpoint := "/api/v1/asset?source=vault&path=" + url.QueryEscape(name)
		created := portableTestRequest(d, http.MethodPost, endpoint, []byte(content))
		if created.Code != http.StatusCreated {
			t.Fatalf("asset create %q = %d: %s", name, created.Code, created.Body)
		}
		read := portableTestRequest(d, http.MethodGet, endpoint, nil)
		if read.Code != http.StatusOK || read.Body.String() != content {
			t.Fatalf("asset read %q = %d: %s", name, read.Code, read.Body)
		}
	}
	exported := portableTestRequest(d, http.MethodGet, "/api/v1/export?source=vault", nil)
	if exported.Code != http.StatusOK {
		t.Fatalf("full export = %d: %s", exported.Code, exported.Body)
	}
	reader, err := zip.NewReader(bytes.NewReader(exported.Body.Bytes()), int64(exported.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[string]string)
	for _, entry := range reader.File {
		file, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		content, readErr := io.ReadAll(file)
		closeErr := file.Close()
		if readErr != nil || closeErr != nil {
			t.Fatalf("read exported %q: %v, %v", entry.Name, readErr, closeErr)
		}
		got[entry.Name] = string(content)
	}
	want := map[string]string{
		"same.md":           "# Source",
		" folder: notes /":  "",
		" assets: images /": "",
		folder + "/":        "",
	}
	for name, content := range documents {
		want[name] = content
	}
	for name, content := range assets {
		want[name] = content
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("export changed names or content: got=%v want=%v", got, want)
	}
	imported := portableTestRequest(d, http.MethodPost, "/api/v1/import?source=library:notes", exported.Body.Bytes())
	if imported.Code != http.StatusOK {
		t.Fatalf("import = %d: %s", imported.Code, imported.Body)
	}
	// Import creates fresh Lore identity metadata, not archive content.
	restored := portableTree(t, library)
	for name := range restored {
		if strings.HasPrefix(name, ".lore/") {
			delete(restored, name)
		}
	}
	if !reflect.DeepEqual(restored, want) {
		t.Fatalf("import changed names or content: got=%v want=%v", restored, want)
	}
	for name := range documents {
		document := d.dispatchDocumentRead(&Request{SourceID: "library:notes", RelPath: name})
		if !document.OK || document.Document == nil || document.Document.RelPath != name {
			t.Fatalf("read imported %q = %#v", name, document)
		}
	}
	// Importing over any of the exact names remains atomic and never clobbers.
	before := portableTree(t, library)
	collision := portableTestRequest(d, http.MethodPost, "/api/v1/import?source=library:notes", exported.Body.Bytes())
	if collision.Code != http.StatusConflict || !reflect.DeepEqual(before, portableTree(t, library)) {
		t.Fatalf("collision = %d: %s", collision.Code, collision.Body)
	}
	if got := readLinkTestFile(t, vault, "Meeting: notes.md"); got != documents["Meeting: notes.md"] {
		t.Fatalf("export changed source: %q", got)
	}
}

func TestDocumentFilenamePolicyRejectsUnsafePortableNames(t *testing.T) {
	d, root, _ := documentTestDaemon(t)
	before := portableTree(t, root)
	for _, name := range []string{"C:note.md", "C:/note.md", "folder/C:note.md", "//server/note.md", `folder\note.md`, "bad\nname.md", "bad\x7fname.md", "../note.md", "folder/.secret.md"} {
		response := d.dispatchDocumentWrite(&Request{SourceID: "vault", RelPath: name, Content: "# Unsafe"})
		if response.OK || response.Code != "invalid_path" {
			t.Fatalf("unsafe document %q = %#v", name, response)
		}
		if err := portableRelative(name); err == nil {
			t.Fatalf("unsafe portable filename accepted: %q", name)
		}
	}
	if after := portableTree(t, root); !reflect.DeepEqual(before, after) {
		t.Fatalf("unsafe writes changed source: before=%v after=%v", before, after)
	}
}
