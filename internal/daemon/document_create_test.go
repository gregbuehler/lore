package daemon

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

const testCreateOperation = "a8e122d0-e397-4cf9-9d26-d6822265cd63"

func TestCreateReplayFollowsIdentityAcrossEditMoveRestartAndDeletion(t *testing.T) {
	d, vault, _ := documentTestDaemon(t)
	request := &Request{Type: "document_write", SourceID: "vault", RelPath: "offline.md", Content: "# Offline\n", OperationID: testCreateOperation}
	created := d.dispatch(request)
	if !created.OK {
		t.Fatal(created.Error)
	}
	id := created.Document.DocumentID
	changed := d.dispatch(&Request{Type: "document_write", SourceID: "vault", RelPath: "offline.md", Content: "# Edited elsewhere\n", BaseRevision: created.Document.Revision, DocumentID: id})
	if !changed.OK {
		t.Fatal(changed.Error)
	}
	moved := d.dispatch(&Request{Type: "document_move", SourceID: "vault", RelPath: "offline.md", NewPath: "moved/offline.md", BaseRevision: changed.Document.Revision, DocumentID: id})
	if !moved.OK {
		t.Fatal(moved.Error)
	}
	restarted := &Daemon{state: &State{Store: d.state.Store, VaultPath: vault, Paths: d.state.Paths, sources: d.state.sources}}
	replayed := restarted.dispatch(request)
	if !replayed.OK || replayed.Document.DocumentID != id || replayed.Document.RelPath != "moved/offline.md" || replayed.Document.Content != changed.Document.Content {
		t.Fatalf("replay must return latest canonical artifact, not recreate: %#v", replayed)
	}
	if _, err := os.Stat(filepath.Join(vault, "offline.md")); !os.IsNotExist(err) {
		t.Fatalf("replay recreated old path: %v", err)
	}
	wrong := *request
	wrong.Content = "different payload"
	if result := restarted.dispatch(&wrong); result.Code != "conflict" {
		t.Fatalf("operation payload reuse = %#v", result)
	}
	deleted := restarted.dispatch(&Request{Type: "document_delete", SourceID: "vault", RelPath: moved.Document.RelPath, BaseRevision: moved.Document.Revision, DocumentID: id})
	if !deleted.OK {
		t.Fatal(deleted.Error)
	}
	replacement := restarted.dispatch(&Request{Type: "document_write", SourceID: "vault", RelPath: "offline.md", Content: request.Content})
	if !replacement.OK {
		t.Fatal(replacement.Error)
	}
	if result := restarted.dispatch(request); result.Code != "creation_gone" {
		t.Fatalf("deleted replay must not adopt replacement: %#v", result)
	}
	read := restarted.dispatch(&Request{Type: "document_read", SourceID: "vault", RelPath: "offline.md"})
	if !read.OK || read.Document.DocumentID != replacement.Document.DocumentID || read.Document.DocumentID == id {
		t.Fatalf("replacement identity changed: %#v", read)
	}
}

func TestReservedCreateRecoversInterruptedPublication(t *testing.T) {
	for _, published := range []bool{false, true} {
		t.Run(map[bool]string{false: "before-file", true: "after-file"}[published], func(t *testing.T) {
			d, vault, _ := documentTestDaemon(t)
			req := &Request{Type: "document_write", SourceID: "vault", RelPath: "reserved.md", Content: "# Reserved\n", OperationID: testCreateOperation}
			source, err := d.source("vault")
			if err != nil {
				t.Fatal(err)
			}
			d.state.mu.Lock()
			result := d.reserveDocumentCreate(source, req.RelPath, req)
			d.state.mu.Unlock()
			if result != nil {
				t.Fatal(result.Error)
			}
			id := d.state.identities[identityKey("vault", req.RelPath)].ID
			if published {
				if err := os.WriteFile(filepath.Join(vault, req.RelPath), []byte(req.Content), 0o644); err != nil {
					t.Fatal(err)
				}
			} else {
				other := d.dispatch(&Request{Type: "document_write", SourceID: "vault", RelPath: req.RelPath, Content: "unrelated"})
				if other.Code != "conflict" {
					t.Fatalf("reserved path stolen: %#v", other)
				}
			}
			restarted := &Daemon{state: &State{Store: d.state.Store, VaultPath: vault, Paths: d.state.Paths, sources: d.state.sources}}
			created := restarted.dispatch(req)
			if !created.OK || created.Document.DocumentID != id || created.Document.Content != req.Content {
				t.Fatalf("recovered = %#v", created)
			}
			again := restarted.dispatch(req)
			if !again.OK || again.Document.DocumentID != id {
				t.Fatalf("retry duplicated recovery: %#v", again)
			}
		})
	}
}

func TestConditionalMutationRejectsReusedPathWithIdenticalContent(t *testing.T) {
	for _, operation := range []string{"document_write", "document_move", "document_delete"} {
		t.Run(operation, func(t *testing.T) {
			d, _, _ := documentTestDaemon(t)
			original := d.dispatch(&Request{Type: "document_read", SourceID: "vault", RelPath: "same.md"})
			if !original.OK {
				t.Fatal(original.Error)
			}
			doc := original.Document
			deleted := d.dispatch(&Request{Type: "document_delete", SourceID: "vault", RelPath: doc.RelPath, BaseRevision: doc.Revision, DocumentID: doc.DocumentID})
			if !deleted.OK {
				t.Fatal(deleted.Error)
			}
			replacement := d.dispatch(&Request{Type: "document_write", SourceID: "vault", RelPath: doc.RelPath, Content: doc.Content})
			if !replacement.OK {
				t.Fatal(replacement.Error)
			}
			stale := d.dispatch(&Request{Type: operation, SourceID: "vault", RelPath: doc.RelPath, NewPath: "stolen.md", Content: "stale mutation", BaseRevision: doc.Revision, DocumentID: doc.DocumentID})
			if stale.Code != "conflict" {
				t.Fatalf("stale identity accepted despite equal revision: %#v", stale)
			}
			current := d.dispatch(&Request{Type: "document_read", SourceID: "vault", DocumentID: replacement.Document.DocumentID})
			if !current.OK || current.Document.Content != doc.Content || current.Document.RelPath != doc.RelPath {
				t.Fatalf("replacement modified: %#v", current)
			}
		})
	}
}

func TestCreateReplayHTTPGoneAndPayloadConflict(t *testing.T) {
	d, _, _ := documentTestDaemon(t)
	body := map[string]string{"source": "vault", "path": "http-offline.md", "content": "# HTTP\n", "baseRevision": "", "operationID": testCreateOperation}
	put := func() *httptest.ResponseRecorder {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		recorder := httptest.NewRecorder()
		d.routeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/api/v1/document", bytes.NewReader(data)))
		return recorder
	}
	first := put()
	if first.Code != http.StatusOK {
		t.Fatalf("create: %d %s", first.Code, first.Body.String())
	}
	var result httpDocumentResponse
	if err := json.Unmarshal(first.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	body["content"] = "wrong replay"
	if response := put(); response.Code != http.StatusConflict {
		t.Fatalf("payload conflict = %d", response.Code)
	}
	body["content"] = "# HTTP\n"
	deleted := d.dispatch(&Request{Type: "document_delete", SourceID: "vault", RelPath: result.Document.RelPath, BaseRevision: result.Document.Revision, DocumentID: result.Document.DocumentID})
	if !deleted.OK {
		t.Fatal(deleted.Error)
	}
	if response := put(); response.Code != http.StatusGone {
		t.Fatalf("deleted creation = %d %s", response.Code, response.Body.String())
	}
	body["operationID"] = "invalid"
	if response := put(); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid operation = %d", response.Code)
	}
}
