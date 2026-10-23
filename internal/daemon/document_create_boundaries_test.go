package daemon

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type creationReadProbe func([]byte) (int, error)

func (read creationReadProbe) Read(buffer []byte) (int, error) { return read(buffer) }

func TestFailedCreationNeverPublishesPartialMarkdown(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "complete.md")
	read := creationReadProbe(func(buffer []byte) (int, error) {
		if _, err := os.Stat(filename); !os.IsNotExist(err) {
			t.Errorf("canonical path became visible before complete publication: %v", err)
		}
		return copy(buffer, "partial body"), io.ErrUnexpectedEOF
	})
	if err := portableWriteExclusive(filename, read); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("copy error = %v", err)
	}
	if _, err := os.Stat(filename); !os.IsNotExist(err) {
		t.Fatalf("partial creation survived: %v", err)
	}
	if err := portableWriteExclusive(filename, strings.NewReader("complete body")); err != nil {
		t.Fatal(err)
	}
	if err := portableWriteExclusive(filename, strings.NewReader("replacement")); !errors.Is(err, os.ErrExist) {
		t.Fatalf("exclusive publication = %v", err)
	}
	data, err := os.ReadFile(filename)
	if err != nil || string(data) != "complete body" {
		t.Fatalf("canonical content = %q, %v", data, err)
	}
}

func TestEveryDestinationRouteRespectsInterruptedCreationReservation(t *testing.T) {
	for _, route := range []string{"document", "asset", "import", "move", "folder", "nested-document", "nested-asset", "nested-import", "nested-folder"} {
		t.Run(route, func(t *testing.T) {
			d, vault, _ := documentTestDaemon(t)
			req := &Request{Type: "document_write", SourceID: "vault", RelPath: "reserved.md", Content: "# Reserved\n", OperationID: testCreateOperation}
			source, err := d.source("vault")
			if err != nil {
				t.Fatal(err)
			}
			d.state.mu.Lock()
			reservation := d.reserveDocumentCreate(source, req.RelPath, req)
			d.state.mu.Unlock()
			if reservation != nil {
				t.Fatal(reservation.Error)
			}
			identity := d.state.identities[identityKey("vault", req.RelPath)].ID
			target := req.RelPath
			if strings.HasPrefix(route, "nested-") {
				target += "/child.md"
			}
			kind := strings.TrimPrefix(route, "nested-")
			switch kind {
			case "document", "folder":
				operation := "document_write"
				if kind == "folder" {
					operation = "folder_create"
				}
				result := d.dispatch(&Request{Type: operation, SourceID: "vault", RelPath: target, Content: req.Content})
				if result.Code != "conflict" {
					t.Fatalf("reservation bypassed: %#v", result)
				}
			case "move":
				original := d.dispatch(&Request{Type: "document_read", SourceID: "vault", RelPath: "same.md"})
				if !original.OK {
					t.Fatal(original.Error)
				}
				result := d.dispatch(&Request{Type: "document_move", SourceID: "vault", RelPath: "same.md", NewPath: target, BaseRevision: original.Document.Revision, DocumentID: original.Document.DocumentID})
				if result.Code != "conflict" {
					t.Fatalf("move stole reservation: %#v", result)
				}
			case "asset", "import":
				body := []byte(req.Content)
				query := url.Values{"source": {"vault"}}
				if kind == "asset" {
					query.Set("path", target)
				} else {
					var archive bytes.Buffer
					writer := zip.NewWriter(&archive)
					entry, err := writer.Create(target)
					if err != nil {
						t.Fatal(err)
					}
					if _, err = entry.Write(body); err != nil {
						t.Fatal(err)
					}
					if err = writer.Close(); err != nil {
						t.Fatal(err)
					}
					body = archive.Bytes()
				}
				recorder := httptest.NewRecorder()
				d.routeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/"+kind+"?"+query.Encode(), bytes.NewReader(body)))
				if recorder.Code != http.StatusConflict {
					t.Fatalf("%s reservation bypassed: %d %s", kind, recorder.Code, recorder.Body.String())
				}
			}
			if _, err := os.Stat(filepath.Join(vault, req.RelPath)); !os.IsNotExist(err) {
				t.Fatalf("competing route published reserved path: %v", err)
			}
			completed := d.dispatch(req)
			if !completed.OK || completed.Document.DocumentID != identity || completed.Document.Content != req.Content {
				t.Fatalf("rightful creator could not resume: %#v", completed)
			}
		})
	}
}

func TestSamePathMoveRejectsReplacementIdentity(t *testing.T) {
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
	result := d.dispatch(&Request{Type: "document_move", SourceID: "vault", RelPath: doc.RelPath, NewPath: doc.RelPath, BaseRevision: doc.Revision, DocumentID: doc.DocumentID})
	if result.Code != "conflict" {
		t.Fatalf("same-path move adopted another identity: %#v", result)
	}
}
