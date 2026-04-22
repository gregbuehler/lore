package daemon

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
)

// Request is the wire format for client → daemon messages.
type Request struct {
	Type     string            `json:"type"`               // "query", "graph", "context", "ping", "status", "reindex"
	Query    string            `json:"query,omitempty"`    // for type=query
	Node     string            `json:"node,omitempty"`     // for type=graph/context
	Context  string            `json:"context,omitempty"`  // for type=context: node path
	Brief    bool              `json:"brief,omitempty"`    // for type=context: truncate to first section
	EdgeType string            `json:"edge_type,omitempty"`
	Depth    int               `json:"depth,omitempty"`
	Limit    int               `json:"limit,omitempty"`
	Filter   map[string]string `json:"filter,omitempty"`   // e.g. {"entity_type": "service"}

	// Entity CRUD fields
	EntityPath  string            `json:"entity_path,omitempty"`  // e.g. "Wiki/Services/foo"
	EntityType  string            `json:"entity_type,omitempty"`  // for create: service, person, etc.
	EntityTitle string            `json:"entity_title,omitempty"` // for create
	SetFields   map[string]string `json:"set_fields,omitempty"`   // for update: key=value pairs
	Changelog   string            `json:"changelog,omitempty"`    // for update: append to Change Log
	Confirm     bool              `json:"confirm,omitempty"`      // for delete: safety flag
}

// Response is the wire format for daemon → client messages.
type Response struct {
	OK        bool          `json:"ok"`
	Error     string        `json:"error,omitempty"`
	Results   []Result      `json:"results,omitempty"`
	Stats     *IndexStats   `json:"stats,omitempty"`
	Content   string        `json:"content,omitempty"` // for type=context: assembled markdown
	ElapsedMs float64       `json:"elapsed_ms"`
}

// Result is a single item in a query response.
type Result struct {
	Path       string  `json:"path"`
	RelPath    string  `json:"rel_path"`
	Title      string  `json:"title"`
	EntityType string  `json:"entity_type,omitempty"`
	Score      float64 `json:"score,omitempty"`
	Snippet    string  `json:"snippet,omitempty"`
	Abstract   string  `json:"abstract,omitempty"`
	EdgeType   string  `json:"edge_type,omitempty"`
	Depth      int     `json:"depth,omitempty"`
}

// IndexStats reports the state of the daemon's index.
type IndexStats struct {
	Documents   int    `json:"documents"`
	Edges       int    `json:"edges"`
	WatchedDirs int    `json:"watched_dirs"`
	VaultPath   string `json:"vault_path"`
	DBPath      string `json:"db_path"`
}

// writeMessage sends a length-prefixed JSON message over a connection.
func writeMessage(conn net.Conn, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	// Write 4-byte big-endian length prefix
	lenBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(lenBuf, uint32(len(data)))
	if _, err := conn.Write(lenBuf); err != nil {
		return fmt.Errorf("write length: %w", err)
	}
	if _, err := conn.Write(data); err != nil {
		return fmt.Errorf("write payload: %w", err)
	}
	return nil
}

// readMessage reads a length-prefixed JSON message from a connection.
func readMessage(conn net.Conn, v any) error {
	lenBuf := make([]byte, 4)
	if _, err := io.ReadFull(conn, lenBuf); err != nil {
		return fmt.Errorf("read length: %w", err)
	}

	length := binary.BigEndian.Uint32(lenBuf)
	if length > 10*1024*1024 { // 10MB sanity limit
		return fmt.Errorf("message too large: %d bytes", length)
	}

	data := make([]byte, length)
	if _, err := io.ReadFull(conn, data); err != nil {
		return fmt.Errorf("read payload: %w", err)
	}

	return json.Unmarshal(data, v)
}
