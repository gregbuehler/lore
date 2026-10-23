package daemon

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"time"
)

// Receipts live in the canonical identity manifest, not the disposable index.
// Reserving the receipt and identity in one atomic write makes a lost create
// response recoverable even after the document is moved or deleted.
type documentCreate struct {
	Path      string `json:"path"`
	Revision  string `json:"revision"`
	Committed bool   `json:"committed"`
}

func createKey(source, operation string) string { return "\x00create\n" + source + "\n" + operation }

func validCreateOperation(operation string) bool {
	if len(operation) != 36 {
		return false
	}
	for i, c := range operation {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

// Called with the document write lock held. A nil result continues an initial
// create or a reserved create whose file was not published before interruption.
func (d *Daemon) replayDocumentCreate(source sourceRoot, req *Request) *Response {
	if req.OperationID == "" {
		return nil
	}
	if !validCreateOperation(req.OperationID) || req.BaseRevision != "" || req.DocumentID != "" {
		return &Response{Code: "invalid_request", Error: "operationID must be a UUID used only for creation"}
	}
	s := d.state
	s.identityMu.Lock()
	if err := s.loadIdentitiesLocked(); err != nil {
		s.identityMu.Unlock()
		return identityError(err)
	}
	receipt, exists := s.identities[createKey(source.SourceID, req.OperationID)]
	s.identityMu.Unlock()
	if !exists {
		return nil
	}
	if receipt.Create == nil || receipt.ID == "" {
		return identityError(fmt.Errorf("invalid creation receipt"))
	}
	if receipt.Create.Path != req.RelPath || receipt.Create.Revision != revision([]byte(req.Content)) {
		return &Response{Code: "conflict", Error: "operationID was already used for different creation content or path"}
	}
	rel, err := d.pathForDocumentID(source.SourceID, receipt.ID)
	if errors.Is(err, os.ErrNotExist) {
		return creationGone()
	}
	if err != nil {
		return identityError(err)
	}
	filename, _, err := safeDocumentPath(source.root, rel, true)
	if err != nil {
		return pathError(err)
	}
	content, err := os.ReadFile(filename)
	if errors.Is(err, os.ErrNotExist) {
		if !receipt.Create.Committed && rel == req.RelPath {
			return nil
		}
		return creationGone()
	}
	if err != nil {
		return &Response{Error: "unable to read created document"}
	}
	if !receipt.Create.Committed {
		if err := s.buildIndexLocked(); err != nil {
			return &Response{Error: "document saved but indexing failed"}
		}
		if err := d.commitDocumentCreate(source.SourceID, req.OperationID); err != nil {
			return identityError(err)
		}
	}
	doc, err := d.document(source, rel, content)
	if err != nil {
		return identityError(err)
	}
	return &Response{OK: true, Document: doc}
}

func creationGone() *Response {
	return &Response{Code: "creation_gone", Error: "this creation was accepted but its document has since been deleted"}
}

// The reservation also prevents a different create from taking an interrupted
// operation's path. Ordinary updates still require their existing revision.
func (d *Daemon) reserveDocumentCreate(source sourceRoot, rel string, req *Request) *Response {
	s := d.state
	s.identityMu.Lock()
	defer s.identityMu.Unlock()
	if err := s.loadIdentitiesLocked(); err != nil {
		return identityError(err)
	}
	key := identityKey(source.SourceID, rel)
	previous, hadPrevious := s.identities[key]
	if s.creationReservedLocked(source.SourceID, rel, req.OperationID) {
		return &Response{Code: "conflict", Error: "a pending creation has reserved this path"}
	}
	if req.OperationID == "" {
		return nil
	}
	receiptKey := createKey(source.SourceID, req.OperationID)
	if _, exists := s.identities[receiptKey]; exists {
		return nil
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return identityError(err)
	}
	identity := documentIdentity{ID: hex.EncodeToString(random[:]), CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), OperationID: req.OperationID}
	receipt := identity
	receipt.Create = &documentCreate{Path: req.RelPath, Revision: revision([]byte(req.Content))}
	s.identities[key], s.identities[receiptKey] = identity, receipt
	if err := s.saveIdentitiesLocked(); err != nil {
		delete(s.identities, receiptKey)
		if hadPrevious {
			s.identities[key] = previous
		} else {
			delete(s.identities, key)
		}
		return identityError(err)
	}
	return nil
}

func (s *State) creationReservedLocked(source, rel, operation string) bool {
	for candidate := rel; candidate != "." && candidate != "/"; candidate = path.Dir(candidate) {
		identity := s.identities[identityKey(source, candidate)]
		if identity.OperationID == "" || identity.OperationID == operation {
			continue
		}
		receipt := s.identities[createKey(source, identity.OperationID)]
		if receipt.Create != nil && !receipt.Create.Committed {
			return true
		}
	}
	return false
}

func (d *Daemon) checkCreationReservation(source, rel, operation string) *Response {
	s := d.state
	s.identityMu.Lock()
	defer s.identityMu.Unlock()
	if err := s.loadIdentitiesLocked(); err != nil {
		return identityError(err)
	}
	if s.creationReservedLocked(source, rel, operation) {
		return &Response{Code: "conflict", Error: "a pending creation has reserved this path"}
	}
	return nil
}

func (d *Daemon) commitDocumentCreate(source, operation string) error {
	if operation == "" {
		return nil
	}
	s := d.state
	s.identityMu.Lock()
	defer s.identityMu.Unlock()
	key := createKey(source, operation)
	previous, exists := s.identities[key]
	if !exists || previous.Create == nil {
		return fmt.Errorf("missing creation receipt")
	}
	if previous.Create.Committed {
		return nil
	}
	updated, metadata := previous, *previous.Create
	metadata.Committed = true
	updated.Create = &metadata
	s.identities[key] = updated
	if err := s.saveIdentitiesLocked(); err != nil {
		s.identities[key] = previous
		return err
	}
	return nil
}
