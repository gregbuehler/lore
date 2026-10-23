package daemon

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

func decodeKnowledgeBody(w http.ResponseWriter, r *http.Request, body any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxHTTPBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(body); err != nil {
		writeHTTPError(w, http.StatusBadRequest, "invalid_request", "invalid JSON request")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeHTTPError(w, http.StatusBadRequest, "invalid_request", "invalid JSON request")
		return false
	}
	return true
}

func (d *Daemon) routeKnowledgeHTTP(w http.ResponseWriter, r *http.Request) bool {
	var req *Request
	var result func(*Response) any
	switch r.URL.Path {
	case "/api/v1/sources":
		if r.Method != http.MethodPost {
			return false
		}
		var body struct {
			Name string `json:"name"`
		}
		if !decodeKnowledgeBody(w, r, &body) {
			return true
		}
		req = &Request{Type: "source_create", Name: body.Name}
		result = func(resp *Response) any { return map[string]any{"source": resp.Source} }
	case "/api/v1/folders":
		switch r.Method {
		case http.MethodGet:
			req = &Request{Type: "folders", SourceID: r.URL.Query().Get("source")}
			result = func(resp *Response) any {
				folders := resp.Folders
				if folders == nil {
					folders = []Folder{}
				}
				return map[string]any{"folders": folders}
			}
		case http.MethodPost:
			var body struct {
				Source string `json:"source"`
				Path   string `json:"path"`
			}
			if !decodeKnowledgeBody(w, r, &body) {
				return true
			}
			req = &Request{Type: "folder_create", SourceID: body.Source, RelPath: body.Path}
			result = func(resp *Response) any { return map[string]string{"path": resp.Path} }
		}
	case "/api/v1/document/move":
		if r.Method == http.MethodPost {
			var body struct {
				Source       string `json:"source"`
				Path         string `json:"path"`
				NewPath      string `json:"newPath"`
				BaseRevision string `json:"baseRevision"`
				DocumentID   string `json:"documentID,omitempty"`
			}
			if !decodeKnowledgeBody(w, r, &body) {
				return true
			}
			req = &Request{Type: "document_move", SourceID: body.Source, RelPath: body.Path, NewPath: body.NewPath, BaseRevision: body.BaseRevision, DocumentID: body.DocumentID}
			result = func(resp *Response) any { return httpDocumentResponse{Document: resp.Document} }
		}
	case "/api/v1/document":
		if r.Method != http.MethodDelete {
			return false
		}
		req = &Request{Type: "document_delete", SourceID: r.URL.Query().Get("source"), RelPath: r.URL.Query().Get("path"), BaseRevision: r.URL.Query().Get("baseRevision"), DocumentID: r.URL.Query().Get("id")}
		result = func(resp *Response) any { return map[string]bool{"ok": true} }
	case "/api/v1/backlinks":
		if r.Method == http.MethodGet {
			req = &Request{Type: "document_backlinks", SourceID: r.URL.Query().Get("source"), RelPath: r.URL.Query().Get("path")}
			result = func(resp *Response) any {
				documents := resp.Documents
				if documents == nil {
					documents = []DocumentSummary{}
				}
				return map[string]any{"documents": documents}
			}
		}
	default:
		return false
	}
	if req == nil {
		writeHTTPError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return true
	}
	resp := d.dispatch(req)
	if !resp.OK {
		writeDispatchError(w, resp)
		return true
	}
	writeHTTPJSON(w, http.StatusOK, result(resp))
	return true
}
