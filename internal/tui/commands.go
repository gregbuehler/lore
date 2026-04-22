package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gbuehler/lore/internal/daemon"
)

// searchTickMsg fires after the debounce delay to trigger a search.
type searchTickMsg struct{}

// Messages
type searchResultMsg struct {
	results []daemon.Result
	err     string
}

type graphResultMsg struct {
	outgoing []daemon.Result
	incoming []daemon.Result
	err      string
}

type statusResultMsg struct {
	stats *daemon.IndexStats
	ok    bool
}

type healthResultMsg struct {
	results []daemon.Result
	err     string
}

type librariesResultMsg struct {
	results []daemon.Result
	err     string
}

// Commands

// debounceSearch returns a command that waits 150 ms then sends a searchTickMsg.
// The Update handler checks whether the query is still current before firing the
// real search, so rapid keystrokes only result in one network call.
func (m Model) debounceSearch() tea.Cmd {
	return tea.Tick(150*time.Millisecond, func(_ time.Time) tea.Msg {
		return searchTickMsg{}
	})
}

func (m Model) doSearch(query string) tea.Cmd {
	return func() tea.Msg {
		resp, err := m.client.Send(&daemon.Request{
			Type:  "query",
			Query: query,
			Limit: 20,
		})
		if err != nil {
			return searchResultMsg{err: err.Error()}
		}
		if !resp.OK {
			return searchResultMsg{err: resp.Error}
		}
		return searchResultMsg{results: resp.Results}
	}
}

func (m Model) fetchGraph(node string) tea.Cmd {
	return func() tea.Msg {
		// Fetch outgoing
		outResp, err := m.client.Send(&daemon.Request{
			Type:  "graph",
			Node:  node,
			Depth: m.graphDepth,
		})
		if err != nil {
			return graphResultMsg{err: err.Error()}
		}

		// Fetch incoming
		inResp, err := m.client.Send(&daemon.Request{
			Type: "backlinks",
			Node: node,
		})
		if err != nil {
			return graphResultMsg{outgoing: outResp.Results, err: err.Error()}
		}

		var outgoing, incoming []daemon.Result
		if outResp.OK {
			outgoing = outResp.Results
		}
		if inResp.OK {
			incoming = inResp.Results
		}

		return graphResultMsg{outgoing: outgoing, incoming: incoming}
	}
}

func (m Model) fetchStatus() tea.Cmd {
	return func() tea.Msg {
		resp, err := m.client.Send(&daemon.Request{Type: "status"})
		if err != nil {
			return statusResultMsg{ok: false}
		}
		if !resp.OK {
			return statusResultMsg{ok: false}
		}
		return statusResultMsg{stats: resp.Stats, ok: true}
	}
}

func (m Model) fetchHealth() tea.Cmd {
	return func() tea.Msg {
		resp, err := m.client.Send(&daemon.Request{Type: "health"})
		if err != nil {
			return healthResultMsg{err: err.Error()}
		}
		if !resp.OK {
			return healthResultMsg{err: resp.Error}
		}
		return healthResultMsg{results: resp.Results}
	}
}

func (m Model) fetchLibraries() tea.Cmd {
	return func() tea.Msg {
		resp, err := m.client.Send(&daemon.Request{Type: "libraries"})
		if err != nil {
			return librariesResultMsg{err: err.Error()}
		}
		if !resp.OK {
			return librariesResultMsg{err: resp.Error}
		}
		return librariesResultMsg{results: resp.Results}
	}
}
