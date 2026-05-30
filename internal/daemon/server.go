package daemon

import (
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/gbuehler/lore/internal/pathutil"
	"github.com/gbuehler/lore/internal/store"
)

// Daemon is the long-running process that holds the index.
type Daemon struct {
	state *State
}

// Start initializes the daemon: builds the index, starts the socket server,
// and blocks until interrupted.
func Start(vaultPath string, libraryPaths []string) error {
	// Ensure socket directory exists
	sockDir := filepath.Dir(SocketPath())
	if err := os.MkdirAll(sockDir, 0o755); err != nil {
		return fmt.Errorf("creating socket dir: %w", err)
	}

	if err := prepareSocketPath(SocketPath()); err != nil {
		return err
	}

	// Write PID file
	pidContent := pidFileContent(os.Getpid(), vaultPath)
	if err := os.WriteFile(PidPath(), []byte(pidContent), 0o644); err != nil {
		return fmt.Errorf("writing pid file: %w", err)
	}
	defer removeIfContentMatches(PidPath(), pidContent)

	// Build state
	state, err := NewState(vaultPath, libraryPaths)
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}
	defer state.Store.Close()

	fmt.Printf("lore daemon: indexing %d paths...\n", len(state.Paths))
	start := time.Now()
	if err := state.BuildIndex(); err != nil {
		return fmt.Errorf("building index: %w", err)
	}
	docs, edges, _ := state.Store.Stats()
	fmt.Printf("lore daemon: indexed %d docs, %d edges in %v\n",
		docs, edges, time.Since(start).Round(time.Millisecond))
	fmt.Printf("lore daemon: store at %s\n", state.Store.Path())

	// Start listener
	listener, err := net.Listen("unix", SocketPath())
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer listener.Close()
	defer removeSocketIfSame(SocketPath(), listener)

	d := &Daemon{state: state}

	// Handle shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		fmt.Println("\nlore daemon: shutting down")
		listener.Close()
	}()

	// Start file watcher
	watcher, err := NewWatcher(state)
	if err != nil {
		fmt.Printf("lore daemon: warning: file watcher disabled: %v\n", err)
	} else {
		go watcher.Start()
		defer watcher.Stop()
	}

	fmt.Printf("lore daemon: listening on %s\n", SocketPath())

	// Accept loop
	for {
		conn, err := listener.Accept()
		if err != nil {
			return nil // listener closed
		}
		go d.handleConn(conn)
	}
}

func pidFileContent(pid int, vaultPath string) string {
	return fmt.Sprintf("%d\n%s\n", pid, normalizeVaultPath(vaultPath))
}

func removeIfContentMatches(path, expected string) {
	data, err := os.ReadFile(path)
	if err == nil && string(data) == expected {
		_ = os.Remove(path)
	}
}

func removeSocketIfSame(path string, listener net.Listener) {
	unixListener, ok := listener.(*net.UnixListener)
	if !ok {
		return
	}
	addr, ok := unixListener.Addr().(*net.UnixAddr)
	if !ok || addr.Name != path {
		return
	}
	_ = os.Remove(path)
}

func prepareSocketPath(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("checking socket path: %w", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("refusing to remove non-socket at %s", path)
	}
	if c, err := Connect(); err == nil {
		c.Close()
		return fmt.Errorf("daemon already running at %s", path)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("removing stale socket: %w", err)
	}
	return nil
}

func (d *Daemon) handleConn(conn net.Conn) {
	defer conn.Close()

	for {
		var req Request
		if err := readMessage(conn, &req); err != nil {
			return // client disconnected or read error
		}

		start := time.Now()
		resp := d.dispatch(&req)
		resp.ElapsedMs = float64(time.Since(start).Microseconds()) / 1000.0

		if err := writeMessage(conn, resp); err != nil {
			return
		}
	}
}

func (d *Daemon) dispatch(req *Request) *Response {
	switch req.Type {
	case "ping":
		return &Response{OK: true}

	case "status":
		d.state.mu.RLock()
		docs, edges, _ := d.state.Store.Stats()
		d.state.mu.RUnlock()
		return &Response{
			OK: true,
			Stats: &IndexStats{
				Documents:   docs,
				Edges:       edges,
				WatchedDirs: len(d.state.Paths),
				VaultPath:   d.state.VaultPath,
				DBPath:      d.state.Store.Path(),
			},
		}

	case "query":
		limit := req.Limit
		if limit <= 0 {
			limit = 10
		}
		d.state.mu.RLock()
		results, err := d.state.Store.Search(req.Query, limit)
		d.state.mu.RUnlock()
		if err != nil {
			return &Response{OK: false, Error: err.Error()}
		}
		entityTypeFilter := req.Filter["entity_type"]
		out := make([]Result, 0, len(results))
		for _, r := range results {
			if entityTypeFilter != "" && r.EntityType != entityTypeFilter {
				continue
			}
			out = append(out, Result{
				Path:       r.Path,
				RelPath:    r.RelPath,
				Title:      r.Title,
				EntityType: r.EntityType,
				Score:      r.Rank,
				Snippet:    r.Snippet,
				Abstract:   r.Abstract,
			})
		}
		return &Response{OK: true, Results: out}

	case "graph":
		if req.Node == "" {
			return &Response{OK: false, Error: "node is required for graph queries"}
		}
		depth := req.Depth
		if depth <= 0 {
			depth = 1
		}
		d.state.mu.RLock()
		neighbors, err := d.state.Store.Neighbors(req.Node, req.EdgeType, depth)
		d.state.mu.RUnlock()
		if err != nil {
			return &Response{OK: false, Error: err.Error()}
		}
		out := make([]Result, len(neighbors))
		for i, n := range neighbors {
			out[i] = Result{
				RelPath:    n.RelPath,
				Title:      n.Title,
				EntityType: n.EntityType,
				EdgeType:   n.EdgeType,
				Depth:      n.Depth,
			}
		}
		return &Response{OK: true, Results: out}

	case "backlinks":
		if req.Node == "" {
			return &Response{OK: false, Error: "node is required for backlink queries"}
		}
		d.state.mu.RLock()
		backlinks, err := d.state.Store.Backlinks(req.Node, req.EdgeType)
		d.state.mu.RUnlock()
		if err != nil {
			return &Response{OK: false, Error: err.Error()}
		}
		out := make([]Result, len(backlinks))
		for i, n := range backlinks {
			out[i] = Result{
				RelPath:    n.RelPath,
				Title:      n.Title,
				EntityType: n.EntityType,
				EdgeType:   n.EdgeType,
			}
		}
		return &Response{OK: true, Results: out}

	case "context":
		return d.dispatchContext(req)

	case "reindex":
		start := time.Now()
		if err := d.state.BuildIndex(); err != nil {
			return &Response{OK: false, Error: err.Error()}
		}
		d.state.mu.RLock()
		docs, _, _ := d.state.Store.Stats()
		d.state.mu.RUnlock()
		return &Response{
			OK: true,
			Results: []Result{{
				Title:   fmt.Sprintf("reindexed %d documents in %v", docs, time.Since(start).Round(time.Millisecond)),
				RelPath: "reindex",
			}},
		}

	case "health":
		return d.dispatchHealth()

	case "libraries":
		return d.dispatchLibraries()

	case "entity_create":
		return d.dispatchEntityCreate(req)

	case "entity_update":
		return d.dispatchEntityUpdate(req)

	case "entity_get":
		return d.dispatchEntityGet(req)

	case "entity_delete":
		return d.dispatchEntityDelete(req)

	case "entity_list":
		return d.dispatchEntityList(req)

	default:
		return &Response{OK: false, Error: fmt.Sprintf("unknown request type: %q", req.Type)}
	}
}

func (d *Daemon) dispatchHealth() *Response {
	d.state.mu.RLock()
	issues, err := d.state.Store.HealthCheck()
	d.state.mu.RUnlock()
	if err != nil {
		return &Response{OK: false, Error: err.Error()}
	}
	out := make([]Result, len(issues))
	for i, h := range issues {
		out[i] = Result{
			Title:    h.Title,
			RelPath:  h.RelPath,
			EdgeType: h.IssueType,
		}
	}
	return &Response{OK: true, Results: out}
}

func (d *Daemon) dispatchLibraries() *Response {
	var out []Result
	for _, p := range d.state.Paths {
		if p == d.state.VaultPath {
			continue // skip vault itself
		}
		out = append(out, Result{
			Title:   filepath.Base(p),
			RelPath: p,
		})
	}
	return &Response{OK: true, Results: out}
}

func (d *Daemon) dispatchContext(req *Request) *Response {
	node := req.Node
	if node == "" {
		node = req.Context
	}
	if node == "" {
		return &Response{OK: false, Error: "node is required for context queries"}
	}
	depth := req.Depth
	if depth <= 0 {
		depth = 1
	}

	// Find the page file across all indexed paths
	pagePath := ""
	for _, root := range d.state.Paths {
		candidate, relNode, err := pathutil.ResolveMarkdownUnderRoot(root, node)
		if err != nil {
			return &Response{OK: false, Error: err.Error()}
		}
		if _, err := os.Stat(candidate); err == nil {
			pagePath = candidate
			node = relNode
			break
		}
	}
	if pagePath == "" {
		return &Response{OK: false, Error: fmt.Sprintf("page not found: %s", node)}
	}

	content, err := os.ReadFile(pagePath)
	if err != nil {
		return &Response{OK: false, Error: fmt.Sprintf("reading page: %v", err)}
	}

	var b strings.Builder

	b.WriteString(fmt.Sprintf("# Context: %s\n\n", node))
	b.WriteString("## Page\n\n")
	if req.Brief {
		b.WriteString(truncateContextToFirstSection(string(content)))
	} else {
		b.WriteString(string(content))
	}
	b.WriteString("\n\n---\n\n")

	// Outgoing edges (typed relationships)
	d.state.mu.RLock()
	neighbors, err := d.state.Store.Neighbors(node, "", depth)
	d.state.mu.RUnlock()
	if err == nil && len(neighbors) > 0 {
		b.WriteString("## Relationships (outgoing)\n\n")
		byType := groupContextByEdgeType(neighbors)
		for edgeType, items := range byType {
			b.WriteString(fmt.Sprintf("**%s:**\n", edgeType))
			for _, item := range items {
				label := item.Title
				if item.EntityType != "" {
					label += " [" + item.EntityType + "]"
				}
				if item.Depth > 1 {
					label += fmt.Sprintf(" (depth %d)", item.Depth)
				}
				b.WriteString(fmt.Sprintf("- %s\n", label))
			}
			b.WriteString("\n")
		}
		b.WriteString("---\n\n")
	}

	// Incoming edges (backlinks)
	d.state.mu.RLock()
	backlinks, err := d.state.Store.Backlinks(node, "")
	d.state.mu.RUnlock()
	if err == nil && len(backlinks) > 0 {
		b.WriteString("## Referenced by\n\n")
		byType := groupContextByEdgeType(backlinks)
		for edgeType, items := range byType {
			b.WriteString(fmt.Sprintf("**%s:**\n", edgeType))
			shown := 0
			for _, item := range items {
				if shown >= 10 {
					b.WriteString(fmt.Sprintf("- ... and %d more\n", len(items)-10))
					break
				}
				label := item.Title
				if item.EntityType != "" {
					label += " [" + item.EntityType + "]"
				}
				b.WriteString(fmt.Sprintf("- %s\n", label))
				shown++
			}
			b.WriteString("\n")
		}
		b.WriteString("---\n\n")
	}

	// Recent mentions via search
	d.state.mu.RLock()
	searchResults, err := d.state.Store.Search(filepath.Base(node), 5)
	d.state.mu.RUnlock()
	if err == nil && len(searchResults) > 0 {
		b.WriteString("## Recent mentions\n\n")
		for _, r := range searchResults {
			if strings.TrimSuffix(r.RelPath, ".md") == node {
				continue
			}
			b.WriteString(fmt.Sprintf("- **%s** (%s)\n", r.Title, r.RelPath))
			if r.Snippet != "" {
				b.WriteString(fmt.Sprintf("  %s\n", r.Snippet))
			}
		}
		b.WriteString("\n")
	}

	return &Response{OK: true, Content: b.String()}
}

// groupContextByEdgeType groups store.GraphResult slices by their EdgeType field.
func groupContextByEdgeType(results []store.GraphResult) map[string][]store.GraphResult {
	groups := make(map[string][]store.GraphResult)
	for _, r := range results {
		groups[r.EdgeType] = append(groups[r.EdgeType], r)
	}
	return groups
}

// truncateContextToFirstSection returns content up to and including the first ## heading's body.
func truncateContextToFirstSection(content string) string {
	lines := strings.Split(content, "\n")
	inFrontmatter := false
	pastFrontmatter := false
	firstSectionFound := false
	var result []string

	for _, line := range lines {
		if line == "---" && !pastFrontmatter {
			if !inFrontmatter {
				inFrontmatter = true
			} else {
				pastFrontmatter = true
			}
			result = append(result, line)
			continue
		}

		if pastFrontmatter && strings.HasPrefix(line, "## ") {
			if firstSectionFound {
				result = append(result, "\n[... truncated, use --depth or full context for more ...]")
				break
			}
			firstSectionFound = true
		}
		result = append(result, line)
	}
	return strings.Join(result, "\n")
}
