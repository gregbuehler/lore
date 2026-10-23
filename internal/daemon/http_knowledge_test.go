package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gregbuehler/lore/internal/config"
)

func TestKnowledgeHTTPRoutesAndStableIDNavigation(t *testing.T) {
	d, vault, _ := documentTestDaemon(t)
	if err := (&config.Config{Vault: config.VaultConfig{Path: vault}}).Save(vault); err != nil {
		t.Fatal(err)
	}
	handler := d.handleHTTP("contract-token")
	call := func(method, target, body string, status int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, target, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer contract-token")
		req.Header.Set("Content-Type", "application/json")
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, req)
		if resp.Code != status {
			t.Fatalf("%s %s = %d %s; want %d", method, target, resp.Code, resp.Body.String(), status)
		}
		return resp
	}
	readDocument := func(resp *httptest.ResponseRecorder) *Document {
		t.Helper()
		var body httpDocumentResponse
		if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Document == nil || body.Document.DocumentID == "" {
			t.Fatalf("missing document identity: %s", resp.Body.String())
		}
		return body.Document
	}
	initial := readDocument(call(http.MethodGet, "/api/v1/document?source=vault&path=same.md", "", http.StatusOK))
	call(http.MethodPut, "/api/v1/document", `{"source":"vault","path":"same.md","content":"must not overwrite","baseRevision":"stale"}`, http.StatusConflict)
	call(http.MethodPost, "/api/v1/folders", `{"source":"vault","path":"empty/nested"}`, http.StatusOK)
	folders := call(http.MethodGet, "/api/v1/folders?source=vault", "", http.StatusOK)
	if !strings.Contains(folders.Body.String(), `"path":"empty/nested"`) {
		t.Fatalf("missing empty folder: %s", folders.Body.String())
	}
	body, err := json.Marshal(map[string]string{"source": "vault", "path": "same.md", "newPath": "moved/note.md", "baseRevision": initial.Revision})
	if err != nil {
		t.Fatal(err)
	}
	moved := readDocument(call(http.MethodPost, "/api/v1/document/move", string(body), http.StatusOK))
	if moved.DocumentID != initial.DocumentID || moved.CreatedAt != initial.CreatedAt {
		t.Fatal("move changed identity")
	}
	byID := readDocument(call(http.MethodGet, "/api/v1/document?source=vault&id="+url.QueryEscape(initial.DocumentID), "", http.StatusOK))
	if byID.RelPath != "moved/note.md" {
		t.Fatalf("ID navigation = %#v", byID)
	}
	call(http.MethodGet, "/api/v1/document?source=vault&path=same.md", "", http.StatusNotFound)
	call(http.MethodGet, "/api/v1/backlinks?source=vault&path=moved%2Fnote.md", "", http.StatusOK)
	call(http.MethodDelete, "/api/v1/document?source=vault&path=moved%2Fnote.md&baseRevision=stale", "", http.StatusConflict)
	call(http.MethodDelete, "/api/v1/document?source=vault&path=moved%2Fnote.md&baseRevision="+moved.Revision, "", http.StatusOK)
	call(http.MethodGet, "/api/v1/document?source=vault&id="+initial.DocumentID, "", http.StatusNotFound)
	created := call(http.MethodPost, "/api/v1/sources", `{"name":"HTTP Library"}`, http.StatusOK)
	if !strings.Contains(created.Body.String(), `"source_id":"library:HTTP Library"`) {
		t.Fatalf("created source = %s", created.Body.String())
	}
	call(http.MethodPost, "/api/v1/sources", `{"name":"HTTP Library"}`, http.StatusConflict)
	call(http.MethodPost, "/api/v1/sources", `{"name":"../escape"}`, http.StatusBadRequest)
	call(http.MethodPost, "/api/v1/folders", `{"source":"vault","path":"ok","extra":true}`, http.StatusBadRequest)
}
