package daemon

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gregbuehler/lore/internal/parse"
	"github.com/gregbuehler/lore/internal/pathutil"
)

// This is canonical metadata, deliberately outside the disposable search index.
// Frontmatter belongs to the author and is never modified to establish identity.
type documentIdentity struct {
	ID          string          `json:"document_id"`
	CreatedAt   string          `json:"created_at"`
	OperationID string          `json:"operation_id,omitempty"`
	Create      *documentCreate `json:"create,omitempty"`
}

func identityKey(sourceID, rel string) string { return sourceID + "\n" + rel }

func (s *State) identityPath() (string, error) {
	root, err := filepath.EvalSymlinks(s.VaultPath)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, ".lore")
	if err := os.Mkdir(dir, 0o755); err != nil && !os.IsExist(err) {
		return "", err
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("unsafe Lore metadata directory")
	}
	path := filepath.Join(dir, "document-identities.json")
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("unsafe identity metadata file")
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	return path, nil
}

func (s *State) loadIdentitiesLocked() error {
	if s.identities != nil {
		return nil
	}
	path, err := s.identityPath()
	if err != nil {
		return err
	}
	identities := make(map[string]documentIdentity)
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil {
		if err := json.Unmarshal(data, &identities); err != nil {
			return fmt.Errorf("invalid canonical document identities: %w", err)
		}
		if identities == nil {
			return fmt.Errorf("invalid canonical document identities")
		}
	}
	s.identities = identities
	return nil
}

func (s *State) saveIdentitiesLocked() error {
	path, err := s.identityPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.identities, "", "  ")
	if err != nil {
		return err
	}
	return pathutil.AtomicWriteFile(path, append(data, '\n'), 0o600)
}

func (d *Daemon) document(source sourceRoot, rel string, content []byte) (*Document, error) {
	path, _, err := safeDocumentPath(source.root, rel, false)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	s := d.state
	s.identityMu.Lock()
	defer s.identityMu.Unlock()
	if err := s.loadIdentitiesLocked(); err != nil {
		return nil, err
	}
	key := identityKey(source.SourceID, rel)
	identity, exists := s.identities[key]
	if !exists {
		var id [16]byte
		if _, err := rand.Read(id[:]); err != nil {
			return nil, err
		}
		identity = documentIdentity{ID: hex.EncodeToString(id[:]), CreatedAt: info.ModTime().UTC().Format(time.RFC3339Nano)}
		s.identities[key] = identity
		if err := s.saveIdentitiesLocked(); err != nil {
			delete(s.identities, key)
			return nil, err
		}
	}
	doc := makeDocument(source.SourceID, rel, content)
	doc.DocumentID = identity.ID
	doc.CreatedAt = identity.CreatedAt
	doc.ModifiedAt = info.ModTime().UTC().Format(time.RFC3339Nano)
	doc.Metadata = jsonMetadata(parse.ParseFrontmatterMap(string(content))).(map[string]any)
	return doc, nil
}

// jsonMetadata normalizes YAML's richer values only in the API projection;
// canonical frontmatter remains untouched. Nonfinite numbers retain explicit
// YAML scalar spellings because JSON has no corresponding numeric values.
func jsonMetadata(value any) any {
	switch value := value.(type) {
	case map[string]any:
		for key, item := range value {
			value[key] = jsonMetadata(item)
		}
		return value
	case map[any]any:
		out := make(map[string]any, len(value))
		for key, item := range value {
			out[fmt.Sprint(jsonMetadata(key))] = jsonMetadata(item)
		}
		return out
	case []any:
		for i, item := range value {
			value[i] = jsonMetadata(item)
		}
		return value
	case float64:
		switch {
		case math.IsNaN(value):
			return ".nan"
		case math.IsInf(value, 1):
			return ".inf"
		case math.IsInf(value, -1):
			return "-.inf"
		}
	}
	return value
}

func (d *Daemon) pathForDocumentID(sourceID, id string) (string, error) {
	s := d.state
	s.identityMu.Lock()
	defer s.identityMu.Unlock()
	if err := s.loadIdentitiesLocked(); err != nil {
		return "", err
	}
	prefix := sourceID + "\n"
	for key, identity := range s.identities {
		if identity.ID == id && strings.HasPrefix(key, prefix) {
			return strings.TrimPrefix(key, prefix), nil
		}
	}
	return "", os.ErrNotExist
}

func (d *Daemon) moveIdentity(source sourceRoot, oldRel, newRel string) error {
	s := d.state
	s.identityMu.Lock()
	defer s.identityMu.Unlock()
	if err := s.loadIdentitiesLocked(); err != nil {
		return err
	}
	oldKey, newKey := identityKey(source.SourceID, oldRel), identityKey(source.SourceID, newRel)
	identity, ok := s.identities[oldKey]
	if !ok {
		return fmt.Errorf("document identity must be established before moving")
	}
	previous, hadPrevious := s.identities[newKey]
	delete(s.identities, oldKey)
	s.identities[newKey] = identity
	if err := s.saveIdentitiesLocked(); err != nil {
		s.identities[oldKey] = identity
		if hadPrevious {
			s.identities[newKey] = previous
		} else {
			delete(s.identities, newKey)
		}
		return err
	}
	return nil
}

func (d *Daemon) deleteIdentity(source sourceRoot, rel string) error {
	s := d.state
	s.identityMu.Lock()
	defer s.identityMu.Unlock()
	if err := s.loadIdentitiesLocked(); err != nil {
		return err
	}
	key := identityKey(source.SourceID, rel)
	previous, exists := s.identities[key]
	delete(s.identities, key)
	if err := s.saveIdentitiesLocked(); err != nil {
		if exists {
			s.identities[key] = previous
		}
		return err
	}
	return nil
}

func summaryOf(doc *Document) DocumentSummary {
	return DocumentSummary{DocumentID: doc.DocumentID, SourceID: doc.SourceID, RelPath: doc.RelPath, Title: doc.Title, Excerpt: excerpt([]byte(doc.Content)), Revision: doc.Revision, CreatedAt: doc.CreatedAt, ModifiedAt: doc.ModifiedAt}
}

func identityError(err error) *Response {
	return &Response{OK: false, Code: "identity_error", Error: "unable to persist document identity: " + err.Error()}
}
