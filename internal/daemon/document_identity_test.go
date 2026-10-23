package daemon

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/gregbuehler/lore/internal/config"
)

func TestDocumentIdentityPersistsWithoutChangingFrontmatter(t *testing.T) {
	d, vault, _ := documentTestDaemon(t)
	content := "---\ntitle: Authored title\ntags: [one, two]\ncustom:\n  nested: true\n---\n# Heading\nBody\n"
	path := filepath.Join(vault, "metadata.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	original := d.dispatch(&Request{Type: "document_read", SourceID: "vault", RelPath: "metadata.md"})
	if !original.OK {
		t.Fatal(original.Error)
	}
	doc := original.Document
	if len(doc.DocumentID) != 32 || doc.Content != content || doc.Metadata["title"] != "Authored title" {
		t.Fatalf("document = %#v", doc)
	}
	for _, value := range []string{doc.CreatedAt, doc.ModifiedAt} {
		if _, err := time.Parse(time.RFC3339Nano, value); err != nil {
			t.Fatal(err)
		}
	}
	updated := d.dispatch(&Request{Type: "document_write", SourceID: "vault", RelPath: "metadata.md", Content: content + "More\n", BaseRevision: doc.Revision})
	if !updated.OK {
		t.Fatal(updated.Error)
	}
	if updated.Document.DocumentID != doc.DocumentID || updated.Document.CreatedAt != doc.CreatedAt {
		t.Fatal("write changed identity")
	}
	if err := d.state.BuildIndex(); err != nil {
		t.Fatal(err)
	}
	// A fresh state has no in-memory identity cache; the sidecar is canonical.
	restarted := &Daemon{state: &State{Store: d.state.Store, VaultPath: vault, Paths: d.state.Paths, sources: d.state.sources}}
	read := restarted.dispatch(&Request{Type: "document_read", SourceID: "vault", DocumentID: doc.DocumentID})
	if !read.OK || read.Document.DocumentID != doc.DocumentID || read.Document.CreatedAt != doc.CreatedAt {
		t.Fatalf("restart read = %#v", read)
	}
	listed := restarted.dispatch(&Request{Type: "document_list", SourceID: "vault", Query: "Heading"})
	if !listed.OK || len(listed.Documents) != 1 || listed.Documents[0].DocumentID != doc.DocumentID {
		t.Fatalf("listed = %#v", listed)
	}
	wrongSource := restarted.dispatch(&Request{Type: "document_read", SourceID: "library:notes", DocumentID: doc.DocumentID})
	if wrongSource.OK || wrongSource.Code != "not_found" {
		t.Fatalf("cross-source identity read = %#v", wrongSource)
	}
}

func TestDocumentConcurrentRevisionWritesNeverOverwrite(t *testing.T) {
	d, vault, _ := documentTestDaemon(t)
	initial := d.dispatch(&Request{Type: "document_read", SourceID: "vault", RelPath: "same.md"})
	if !initial.OK {
		t.Fatal(initial.Error)
	}
	responses := make(chan *Response, 2)
	var wg sync.WaitGroup
	for _, content := range []string{"# Writer one", "# Writer two"} {
		wg.Add(1)
		go func(content string) {
			defer wg.Done()
			responses <- d.dispatch(&Request{Type: "document_write", SourceID: "vault", RelPath: "same.md", Content: content, BaseRevision: initial.Document.Revision})
		}(content)
	}
	wg.Wait()
	close(responses)
	successes, conflicts := 0, 0
	var winner string
	for resp := range responses {
		if resp.OK {
			successes++
			winner = resp.Document.Content
		} else if resp.Code == "conflict" {
			conflicts++
		} else {
			t.Fatal(resp.Error)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}
	data, err := os.ReadFile(filepath.Join(vault, "same.md"))
	if err != nil || string(data) != winner {
		t.Fatalf("winner overwritten: %q, %v", data, err)
	}
	create := d.dispatch(&Request{Type: "document_write", SourceID: "vault", RelPath: "same.md", Content: "replace"})
	if create.OK || create.Code != "conflict" {
		t.Fatalf("create overwrote existing: %#v", create)
	}
}

func TestSourceCreationPersistsAsRealSubscription(t *testing.T) {
	d, vault, library := documentTestDaemon(t)
	cfg := &config.Config{Vault: config.VaultConfig{Path: vault}, Subscriptions: []config.SubscriptionConfig{{Name: "existing", Repo: "local:" + library, Path: library, Access: "read-write"}}}
	if err := cfg.Save(vault); err != nil {
		t.Fatal(err)
	}
	created := d.dispatch(&Request{Type: "source_create", Name: "Research"})
	if !created.OK || created.Source.SourceID != "library:Research" {
		t.Fatalf("create = %#v", created)
	}
	duplicate := d.dispatch(&Request{Type: "source_create", Name: "research"})
	if duplicate.OK || duplicate.Code != "conflict" {
		t.Fatalf("duplicate = %#v", duplicate)
	}
	written := d.dispatch(&Request{Type: "document_write", SourceID: created.Source.SourceID, RelPath: "nested/Note.md", Content: "# Shared canonical Markdown"})
	if !written.OK {
		t.Fatal(written.Error)
	}
	cfg, err := config.Load(vault)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Subscriptions) != 2 {
		t.Fatalf("subscriptions = %#v", cfg.Subscriptions)
	}
	root := cfg.Subscriptions[1].ContentPath()
	data, err := os.ReadFile(filepath.Join(root, "nested", "Note.md"))
	if err != nil || string(data) != written.Document.Content {
		t.Fatalf("canonical content = %q, %v", data, err)
	}
	t.Setenv("LORE_DB", t.TempDir())
	restarted, err := NewState(vault, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Store.Close()
	if len(restarted.sources) != 3 {
		t.Fatalf("subscription discovery = %#v", restarted.sources)
	}
	read := (&Daemon{state: restarted}).dispatch(&Request{Type: "document_read", SourceID: created.Source.SourceID, DocumentID: written.Document.DocumentID})
	if !read.OK || read.Document.Content != written.Document.Content {
		t.Fatalf("restart = %#v", read)
	}
}

func TestIdentitySidecarRejectsSymlinksAndCorruption(t *testing.T) {
	for _, mode := range []string{"symlink", "corrupt"} {
		t.Run(mode, func(t *testing.T) {
			d, vault, _ := documentTestDaemon(t)
			if err := os.Mkdir(filepath.Join(vault, ".lore"), 0o755); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(vault, ".lore", "document-identities.json")
			if mode == "symlink" {
				if err := os.Symlink(filepath.Join(t.TempDir(), "outside"), path); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(path, []byte("not JSON"), 0o600); err != nil {
				t.Fatal(err)
			}
			resp := d.dispatch(&Request{Type: "document_read", SourceID: "vault", RelPath: "same.md"})
			if resp.OK || resp.Code != "identity_error" {
				t.Fatalf("unsafe identity = %#v", resp)
			}
		})
	}
}

func TestSourceDiscoveryReflectsCLIConfigChanges(t *testing.T) {
	d, vault, _ := documentTestDaemon(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# Newly subscribed knowledge"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Vault: config.VaultConfig{Path: vault}, Subscriptions: []config.SubscriptionConfig{{Name: "from-cli", Repo: "local:" + root, Path: root, Access: "read-write"}}}
	if err := cfg.Save(vault); err != nil {
		t.Fatal(err)
	}
	discovered := d.dispatch(&Request{Type: "sources"})
	if !discovered.OK {
		t.Fatal(discovered.Error)
	}
	listed := d.dispatch(&Request{Type: "document_list", SourceID: "library:from-cli", Query: "subscribed"})
	if !listed.OK || len(listed.Documents) != 1 || listed.Documents[0].RelPath != "README.md" {
		t.Fatalf("new subscription = %#v", listed)
	}
	isolated := d.dispatch(&Request{Type: "document_list", SourceID: "vault", Query: "subscribed"})
	if !isolated.OK || len(isolated.Documents) != 0 {
		t.Fatalf("source search leaked: %#v", isolated)
	}
	cfg.Subscriptions = nil
	if err := cfg.Save(vault); err != nil {
		t.Fatal(err)
	}
	if resp := d.dispatch(&Request{Type: "sources"}); !resp.OK {
		t.Fatal(resp.Error)
	}
	removed := d.dispatch(&Request{Type: "document_read", SourceID: "library:from-cli", RelPath: "README.md"})
	if removed.OK || removed.Code != "source_not_found" {
		t.Fatalf("removed subscription = %#v", removed)
	}
}

func TestKnownIdentityReadsDoNotRewriteManifest(t *testing.T) {
	d, vault, _ := documentTestDaemon(t)
	first := d.dispatch(&Request{Type: "document_read", SourceID: "vault", RelPath: "same.md"})
	if !first.OK {
		t.Fatal(first.Error)
	}
	path := filepath.Join(vault, ".lore", "document-identities.json")
	sentinel := time.Date(2001, time.January, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(path, sentinel, sentinel); err != nil {
		t.Fatal(err)
	}
	if err := d.state.BuildIndex(); err != nil {
		t.Fatal(err)
	}
	for _, req := range []*Request{
		{Type: "document_read", SourceID: "vault", DocumentID: first.Document.DocumentID},
		{Type: "document_list", SourceID: "vault"},
	} {
		if resp := d.dispatch(req); !resp.OK {
			t.Fatal(resp.Error)
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(sentinel) {
		t.Fatal("known identity read rewrote canonical manifest")
	}
}

func TestDocumentHTTPNormalizesYAMLMetadataWithoutChangingContent(t *testing.T) {
	d, vault, _ := documentTestDaemon(t)
	handler := d.handleHTTP("metadata-token")
	content := "---\nmeasurements:\n  2024: baseline\nvalue: .nan\nnested:\n  - flags:\n      true: [.inf, -.Inf, .NaN, 1.5, null]\n  - [text, {7: [false, {deep: .inf}]}]\n---\n# Metadata\n"
	wantMetadata := map[string]any{
		"measurements": map[string]any{"2024": "baseline"},
		"value":        ".nan",
		"nested": []any{
			map[string]any{"flags": map[string]any{"true": []any{".inf", "-.inf", ".nan", 1.5, nil}}},
			[]any{"text", map[string]any{"7": []any{false, map[string]any{"deep": ".inf"}}}},
		},
	}
	if err := os.WriteFile(filepath.Join(vault, "metadata.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	var identity, revision string
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodGet} {
		var body []byte
		if method == http.MethodPut {
			content += "Saved without rewriting YAML.\n"
			var err error
			body, err = json.Marshal(httpDocumentWriteRequest{Source: "vault", Path: "metadata.md", Content: content, BaseRevision: revision})
			if err != nil {
				t.Fatal(err)
			}
		}
		req := httptest.NewRequest(method, "/api/v1/document?source=vault&path=metadata.md", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer metadata-token")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s status = %d, body = %s", method, recorder.Code, recorder.Body.String())
		}
		var response httpDocumentResponse
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatalf("%s response is not JSON: %v, body = %q", method, err, recorder.Body.String())
		}
		doc := response.Document
		if doc == nil || doc.Content != content || !reflect.DeepEqual(doc.Metadata, wantMetadata) {
			t.Fatalf("%s document = %#v", method, doc)
		}
		if identity != "" && identity != doc.DocumentID {
			t.Fatalf("%s changed identity: %q != %q", method, identity, doc.DocumentID)
		}
		identity, revision = doc.DocumentID, doc.Revision
		data, err := os.ReadFile(filepath.Join(vault, "metadata.md"))
		if err != nil || string(data) != content {
			t.Fatalf("%s changed canonical YAML: %q, %v", method, data, err)
		}
	}
}
