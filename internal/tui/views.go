package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/gbuehler/lore/internal/daemon"
)

func (m Model) renderSearch(height int) string {
	var b strings.Builder

	// Input
	b.WriteString(searchInput.Render(m.searchInput.View()))
	b.WriteString("\n\n")

	if m.searchErr != "" {
		b.WriteString(lipgloss.NewStyle().Foreground(errColor).Render("  " + m.searchErr))
		b.WriteString("\n")
	}

	if len(m.searchResults) == 0 && m.lastQuery != "" && m.searchErr == "" {
		b.WriteString("  No results.\n")
		return b.String()
	}

	// Results list
	maxVisible := height - 4
	if maxVisible < 1 {
		maxVisible = 1
	}

	start := 0
	if m.searchCursor >= maxVisible {
		start = m.searchCursor - maxVisible + 1
	}

	for i := start; i < len(m.searchResults) && i < start+maxVisible; i++ {
		r := m.searchResults[i]
		selected := i == m.searchCursor && !m.searchInput.Focused()

		line := formatSearchResult(i+1, r, selected)
		b.WriteString(line)
	}

	return b.String()
}

func formatSearchResult(num int, r daemon.Result, selected bool) string {
	var b strings.Builder

	prefix := "  "
	if selected {
		prefix = "▸ "
	}

	// Line 1: number, title, type, score
	title := resultTitle.Render(r.Title)
	var entityTag string
	if r.EntityType != "" {
		entityTag = " " + resultType.Render("["+r.EntityType+"]")
	}
	score := resultScore.Render(fmt.Sprintf("(%.1f)", r.Score))

	line1 := fmt.Sprintf("%s%d. %s%s  %s", prefix, num, title, entityTag, score)

	// Line 2: path
	line2 := fmt.Sprintf("     %s", resultPath.Render(r.RelPath))

	// Line 3: abstract or snippet (if available)
	var line3 string
	if r.Abstract != "" {
		// Trim long abstracts
		abs := r.Abstract
		if len(abs) > 100 {
			abs = abs[:97] + "..."
		}
		line3 = fmt.Sprintf("     %s", resultAbstract.Render(abs))
	} else if r.Snippet != "" {
		snip := r.Snippet
		if len(snip) > 100 {
			snip = snip[:97] + "..."
		}
		line3 = fmt.Sprintf("     %s", resultAbstract.Render(snip))
	}

	if selected {
		b.WriteString(selectedResult.Render(line1) + "\n")
		b.WriteString(selectedResult.Render(line2) + "\n")
		if line3 != "" {
			b.WriteString(selectedResult.Render(line3) + "\n")
		}
	} else {
		b.WriteString(line1 + "\n")
		b.WriteString(line2 + "\n")
		if line3 != "" {
			b.WriteString(line3 + "\n")
		}
	}
	b.WriteString("\n")

	return b.String()
}

func (m Model) renderGraph(height int) string {
	var b strings.Builder

	if m.graphNode == "" {
		b.WriteString("  No node selected. Search and press Enter to explore.\n")
		return b.String()
	}

	// Header
	header := nodeTitle.Render(m.graphNode)
	b.WriteString("  " + header + "\n\n")

	if m.graphErr != "" {
		b.WriteString(lipgloss.NewStyle().Foreground(errColor).Render("  " + m.graphErr))
		b.WriteString("\n")
		return b.String()
	}

	items := m.graphList()
	if len(items) == 0 {
		b.WriteString("  No connections.\n")
		return b.String()
	}

	outCount := len(m.graphResults)

	maxVisible := height - 5
	if maxVisible < 1 {
		maxVisible = 1
	}

	start := 0
	if m.graphCursor >= maxVisible {
		start = m.graphCursor - maxVisible + 1
	}

	for i := start; i < len(items) && i < start+maxVisible; i++ {
		// Section dividers
		if i == 0 && outCount > 0 {
			b.WriteString(fmt.Sprintf("  ─── Outgoing (%d) ───\n", outCount))
		}
		if i == outCount && len(m.graphBacklinks) > 0 {
			if i > start {
				b.WriteString("\n")
			}
			b.WriteString(fmt.Sprintf("  ─── Incoming (%d) ───\n", len(m.graphBacklinks)))
		}

		r := items[i]
		selected := i == m.graphCursor
		prefix := "  "
		if selected {
			prefix = "▸ "
		}

		indent := strings.Repeat("  ", r.Depth)
		edge := edgeType.Render(r.EdgeType)
		title := nodeTitle.Render(r.Title)
		var entityTag string
		if r.EntityType != "" {
			entityTag = " " + nodeType.Render("["+r.EntityType+"]")
		}
		path := resultPath.Render(r.RelPath)

		line := fmt.Sprintf("%s%s%s → %s%s  %s", prefix, indent, edge, title, entityTag, path)
		if selected {
			line = selectedResult.Render(line)
		}
		b.WriteString(line + "\n")
	}

	return b.String()
}

func (m Model) renderStatus(height int) string {
	var b strings.Builder

	if !m.statsOK {
		b.WriteString("  Daemon not responding.\n")
		return b.String()
	}

	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("  %s %s\n",
		statusLabel.Render("Documents:"),
		statusValue.Render(fmt.Sprintf("%d", m.stats.Documents))))
	b.WriteString(fmt.Sprintf("  %s %s\n",
		statusLabel.Render("Edges:"),
		statusValue.Render(fmt.Sprintf("%d", m.stats.Edges))))
	b.WriteString(fmt.Sprintf("  %s %s\n",
		statusLabel.Render("Watched dirs:"),
		statusValue.Render(fmt.Sprintf("%d", m.stats.WatchedDirs))))
	b.WriteString(fmt.Sprintf("  %s %s\n",
		statusLabel.Render("Vault:"),
		statusValue.Render(m.stats.VaultPath)))
	b.WriteString(fmt.Sprintf("  %s %s\n",
		statusLabel.Render("DB path:"),
		statusValue.Render(m.stats.DBPath)))

	return b.String()
}

func (m Model) renderLibraries(height int) string {
	var b strings.Builder
	b.WriteString("\n")

	if m.libErr != "" {
		b.WriteString(lipgloss.NewStyle().Foreground(errColor).Render("  " + m.libErr))
		b.WriteString("\n")
		return b.String()
	}

	if len(m.libraries) == 0 {
		b.WriteString("  No subscribed libraries. Use 'lore subscribe' to add one.\n")
		return b.String()
	}

	for i, lib := range m.libraries {
		selected := i == m.libCursor
		prefix := "  "
		if selected {
			prefix = "▸ "
		}

		title := nodeTitle.Render(lib.Title)
		path := resultPath.Render(lib.RelPath)
		line := fmt.Sprintf("%s%s  %s", prefix, title, path)
		if selected {
			line = selectedResult.Render(line)
		}
		b.WriteString(line + "\n")
	}

	return b.String()
}

func (m Model) renderHealth(height int) string {
	var b strings.Builder
	b.WriteString("\n")

	if m.healthErr != "" {
		b.WriteString(lipgloss.NewStyle().Foreground(errColor).Render("  " + m.healthErr))
		b.WriteString("\n")
		return b.String()
	}

	if len(m.healthResults) == 0 {
		b.WriteString("  No issues found. Vault is healthy.\n")
		return b.String()
	}

	maxVisible := height - 3
	if maxVisible < 1 {
		maxVisible = 1
	}

	start := 0
	if m.healthCursor >= maxVisible {
		start = m.healthCursor - maxVisible + 1
	}

	for i := start; i < len(m.healthResults) && i < start+maxVisible; i++ {
		r := m.healthResults[i]
		selected := i == m.healthCursor
		prefix := "  "
		if selected {
			prefix = "▸ "
		}

		issueType := edgeType.Render(r.EdgeType)
		title := nodeTitle.Render(r.Title)
		path := resultPath.Render(r.RelPath)

		line := fmt.Sprintf("%s%s  %s  %s", prefix, issueType, title, path)
		if selected {
			line = selectedResult.Render(line)
		}
		b.WriteString(line + "\n")
	}

	return b.String()
}
