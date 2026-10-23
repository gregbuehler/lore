package daemon

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

const defaultHTTPAddress = "127.0.0.1:7520"
const maxHTTPBody = 10 << 20

type httpSourceResponse struct {
	Sources []Source `json:"sources"`
}

type httpDocumentListResponse struct {
	Documents  []DocumentSummary `json:"documents"`
	HasMore    bool              `json:"hasMore"`
	NextCursor string            `json:"nextCursor,omitempty"`
}

type httpDocumentResponse struct {
	Document *Document `json:"document"`
}

type httpDocumentWriteRequest struct {
	Source       string `json:"source"`
	Path         string `json:"path"`
	Content      string `json:"content"`
	BaseRevision string `json:"baseRevision"`
	DocumentID   string `json:"documentID,omitempty"`
	OperationID  string `json:"operationID,omitempty"`
}

func startHTTPServer(d *Daemon) (*http.Server, net.Listener, error) {
	token := os.Getenv("LORE_API_TOKEN")
	if token == "" {
		return nil, nil, nil
	}
	address := os.Getenv("LORE_API_ADDR")
	if address == "" {
		address = defaultHTTPAddress
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, nil, err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", d.handleHTTP(token))
	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	return server, listener, nil
}

func (d *Daemon) handleHTTP(token string) http.HandlerFunc {
	tokenHash := sha256.Sum256([]byte(token))
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			if r.Method != http.MethodGet {
				writeHTTPError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
				return
			}
			if r.URL.Path == "/healthz" {
				writeHTTPJSON(w, http.StatusOK, map[string]bool{"ok": true})
				return
			}
			if !d.isReady() {
				writeHTTPJSON(w, http.StatusServiceUnavailable, map[string]bool{"ok": false})
				return
			}
			writeHTTPJSON(w, http.StatusOK, map[string]bool{"ok": true})
			return
		}
		provided := ""
		if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			provided = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		}
		providedHash := sha256.Sum256([]byte(provided))
		if subtle.ConstantTimeCompare(tokenHash[:], providedHash[:]) != 1 {
			writeHTTPJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized", "code": "unauthorized"})
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		d.routeHTTP(w, r)
	}
}

func (d *Daemon) isReady() bool {
	if d == nil || d.state == nil || d.state.Store == nil {
		return false
	}
	d.state.mu.RLock()
	ready := d.state.initialized && len(d.state.sources) > 0
	d.state.mu.RUnlock()
	return ready
}

func (d *Daemon) routeHTTP(w http.ResponseWriter, r *http.Request) {
	if d.routePortableHTTP(w, r) || d.routeKnowledgeHTTP(w, r) {
		return
	}
	switch r.URL.Path {
	case "/api/v1/resolve":
		if r.Method != http.MethodGet {
			writeHTTPError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		resp := d.dispatch(&Request{
			Type:     "document_resolve",
			SourceID: r.URL.Query().Get("source"),
			FromPath: r.URL.Query().Get("from"),
			Target:   r.URL.Query().Get("target"),
		})
		if !resp.OK {
			writeDispatchError(w, resp)
			return
		}
		writeHTTPJSON(w, http.StatusOK, resp.Resolution)
	case "/api/v1/sources":
		if r.Method != http.MethodGet {
			writeHTTPError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		resp := d.dispatch(&Request{Type: "sources"})
		if !resp.OK {
			writeDispatchError(w, resp)
			return
		}
		writeHTTPJSON(w, http.StatusOK, httpSourceResponse{Sources: resp.Sources})
	case "/api/v1/documents":
		if r.Method != http.MethodGet {
			writeHTTPError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		resp := d.dispatch(&Request{Type: "document_list", SourceID: r.URL.Query().Get("source"), Query: r.URL.Query().Get("q"), Cursor: r.URL.Query().Get("cursor")})
		if !resp.OK {
			writeDispatchError(w, resp)
			return
		}
		writeHTTPJSON(w, http.StatusOK, httpDocumentListResponse{Documents: resp.Documents, HasMore: resp.HasMore, NextCursor: resp.NextCursor})
	case "/api/v1/document":
		switch r.Method {
		case http.MethodGet:
			resp := d.dispatch(&Request{Type: "document_read", SourceID: r.URL.Query().Get("source"), RelPath: r.URL.Query().Get("path"), DocumentID: r.URL.Query().Get("id")})
			if !resp.OK {
				writeDispatchError(w, resp)
				return
			}
			writeHTTPJSON(w, http.StatusOK, httpDocumentResponse{Document: resp.Document})
		case http.MethodPut:
			var body httpDocumentWriteRequest
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxHTTPBody))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&body); err != nil {
				writeHTTPError(w, http.StatusBadRequest, "invalid_request", "invalid JSON request")
				return
			}
			if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
				writeHTTPError(w, http.StatusBadRequest, "invalid_request", "invalid JSON request")
				return
			}
			resp := d.dispatch(&Request{Type: "document_write", SourceID: body.Source, RelPath: body.Path, Content: body.Content, BaseRevision: body.BaseRevision, DocumentID: body.DocumentID, OperationID: body.OperationID})
			if !resp.OK {
				writeDispatchError(w, resp)
				return
			}
			writeHTTPJSON(w, http.StatusOK, httpDocumentResponse{Document: resp.Document})
		default:
			writeHTTPError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		}
	default:
		writeHTTPError(w, http.StatusNotFound, "not_found", "route not found")
	}
}

func writeDispatchError(w http.ResponseWriter, resp *Response) {
	status := http.StatusBadRequest
	switch resp.Code {
	case "conflict":
		status = http.StatusConflict
	case "creation_gone":
		status = http.StatusGone
	case "readonly":
		status = http.StatusForbidden
	case "not_found", "source_not_found":
		status = http.StatusNotFound
	case "invalid_path", "invalid_cursor", "invalid_source", "invalid_sources", "invalid_request", "invalid_archive", "too_large":
		status = http.StatusBadRequest
	default:
		status = http.StatusInternalServerError
	}
	message := resp.Error
	if status == http.StatusInternalServerError {
		message = "Lore request failed"
	}
	writeHTTPError(w, status, resp.Code, message)
}

func writeHTTPError(w http.ResponseWriter, status int, code, message string) {
	writeHTTPJSON(w, status, map[string]string{"error": message, "code": code})
}

func writeHTTPJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
