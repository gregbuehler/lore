package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/gbuehler/lore/internal/daemon"
)

type tab int

const (
	tabSearch tab = iota
	tabGraph
	tabStatus
	tabLibraries
	tabHealth
)

var tabNames = []string{"Search", "Graph", "Status", "Libraries", "Health"}

// editorFinishedMsg is sent when the editor process exits.
type editorFinishedMsg struct{ err error }

// Model is the top-level TUI model.
type Model struct {
	client    *daemon.Client
	vaultPath string
	width     int
	height    int

	activeTab tab

	// Search state
	searchInput    textinput.Model
	searchResults  []daemon.Result
	searchCursor   int
	searchErr      string
	lastQuery      string
	searchPending  string

	// Graph state
	graphNode      string
	graphResults   []daemon.Result
	graphBacklinks []daemon.Result
	graphCursor    int
	graphMode      string // "out" or "in"
	graphDepth     int
	graphErr       string

	// Status state
	stats   *daemon.IndexStats
	statsOK bool

	// Libraries state
	libraries []daemon.Result
	libCursor int
	libErr    string

	// Health state
	healthResults []daemon.Result
	healthCursor  int
	healthErr     string
}

// New creates a new TUI model connected to the daemon.
func New(client *daemon.Client, vaultPath string) Model {
	ti := textinput.New()
	ti.Placeholder = "Search your vault..."
	ti.Focus()
	ti.CharLimit = 200
	ti.Width = 60

	return Model{
		client:      client,
		vaultPath:   vaultPath,
		activeTab:   tabSearch,
		searchInput: ti,
		graphMode:   "out",
		graphDepth:  1,
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, m.fetchStatus())
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case searchTickMsg:
		// Debounce: only fire if the query hasn't changed since the tick was scheduled.
		if m.searchPending == m.lastQuery && m.lastQuery != "" {
			return m, m.doSearch(m.lastQuery)
		}
		return m, nil

	case searchResultMsg:
		m.searchResults = msg.results
		m.searchErr = msg.err
		return m, nil

	case graphResultMsg:
		m.graphResults = msg.outgoing
		m.graphBacklinks = msg.incoming
		m.graphErr = msg.err
		return m, nil

	case statusResultMsg:
		m.stats = msg.stats
		m.statsOK = msg.ok
		return m, nil

	case healthResultMsg:
		m.healthResults = msg.results
		m.healthErr = msg.err
		return m, nil

	case librariesResultMsg:
		m.libraries = msg.results
		m.libErr = msg.err
		return m, nil

	case editorFinishedMsg:
		return m, nil
	}

	// Update text input
	if m.activeTab == tabSearch {
		var cmd tea.Cmd
		m.searchInput, cmd = m.searchInput.Update(msg)

		// Trigger debounced search on input change
		if m.searchInput.Value() != m.lastQuery {
			m.lastQuery = m.searchInput.Value()
			if m.lastQuery != "" {
				m.searchPending = m.lastQuery
				return m, tea.Batch(cmd, m.debounceSearch())
			}
			m.searchPending = ""
			m.searchResults = nil
		}
		return m, cmd
	}

	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		if m.activeTab == tabSearch && m.searchInput.Focused() {
			if msg.String() == "q" {
				// Let 'q' go to input
				break
			}
		}
		return m, tea.Quit

	case "esc":
		if m.activeTab == tabGraph && m.graphNode != "" {
			// Back to search from graph
			m.activeTab = tabSearch
			m.searchInput.Focus()
			return m, nil
		}
		return m, tea.Quit

	case "tab":
		m.activeTab = (m.activeTab + 1) % tab(len(tabNames))
		return m, m.onTabSwitch()

	case "shift+tab":
		if m.activeTab == 0 {
			m.activeTab = tab(len(tabNames) - 1)
		} else {
			m.activeTab--
		}
		return m, m.onTabSwitch()

	case "up", "k":
		if m.activeTab == tabSearch && !m.searchInput.Focused() {
			if m.searchCursor > 0 {
				m.searchCursor--
			}
		} else if m.activeTab == tabGraph {
			if m.graphCursor > 0 {
				m.graphCursor--
			}
		} else if m.activeTab == tabHealth {
			if m.healthCursor > 0 {
				m.healthCursor--
			}
		} else if m.activeTab == tabLibraries {
			if m.libCursor > 0 {
				m.libCursor--
			}
		}
		return m, nil

	case "down", "j":
		if m.activeTab == tabSearch && !m.searchInput.Focused() {
			max := len(m.searchResults) - 1
			if m.searchCursor < max {
				m.searchCursor++
			}
		} else if m.activeTab == tabGraph {
			max := m.graphListLen() - 1
			if m.graphCursor < max {
				m.graphCursor++
			}
		} else if m.activeTab == tabHealth {
			max := len(m.healthResults) - 1
			if m.healthCursor < max {
				m.healthCursor++
			}
		} else if m.activeTab == tabLibraries {
			max := len(m.libraries) - 1
			if m.libCursor < max {
				m.libCursor++
			}
		}
		return m, nil

	case "enter":
		return m.handleEnter()

	case "e":
		if m.activeTab == tabSearch && !m.searchInput.Focused() {
			if len(m.searchResults) > 0 && m.searchCursor < len(m.searchResults) {
				return m, m.openInEditor(m.searchResults[m.searchCursor].Path)
			}
		} else if m.activeTab == tabGraph {
			items := m.graphList()
			if m.graphCursor < len(items) {
				relPath := items[m.graphCursor].RelPath
				if !strings.HasSuffix(relPath, ".md") {
					relPath += ".md"
				}
				filePath := filepath.Join(m.vaultPath, relPath)
				return m, m.openInEditor(filePath)
			}
		}

	case "/":
		if m.activeTab == tabSearch && !m.searchInput.Focused() {
			m.searchInput.Focus()
			return m, textinput.Blink
		}

	case "ctrl+n":
		if m.activeTab == tabSearch {
			m.searchInput.Blur()
			return m, nil
		}
	}

	// Pass to text input if search tab and focused
	if m.activeTab == tabSearch && m.searchInput.Focused() {
		var cmd tea.Cmd
		m.searchInput, cmd = m.searchInput.Update(msg)
		if m.searchInput.Value() != m.lastQuery {
			m.lastQuery = m.searchInput.Value()
			if m.lastQuery != "" {
				m.searchPending = m.lastQuery
				return m, tea.Batch(cmd, m.debounceSearch())
			}
			m.searchPending = ""
			m.searchResults = nil
		}
		return m, cmd
	}

	return m, nil
}

func (m Model) handleEnter() (tea.Model, tea.Cmd) {
	switch m.activeTab {
	case tabSearch:
		if len(m.searchResults) > 0 && m.searchCursor < len(m.searchResults) {
			r := m.searchResults[m.searchCursor]
			// Navigate to graph view for this node
			node := strings.TrimSuffix(r.RelPath, ".md")
			m.graphNode = node
			m.activeTab = tabGraph
			m.graphCursor = 0
			m.searchInput.Blur()
			return m, m.fetchGraph(node)
		}

	case tabGraph:
		// Drill into selected node
		items := m.graphList()
		if m.graphCursor < len(items) {
			node := strings.TrimSuffix(items[m.graphCursor].RelPath, ".md")
			m.graphNode = node
			m.graphCursor = 0
			return m, m.fetchGraph(node)
		}
	}
	return m, nil
}

func (m Model) onTabSwitch() tea.Cmd {
	switch m.activeTab {
	case tabSearch:
		m.searchInput.Focus()
		return textinput.Blink
	case tabStatus:
		return m.fetchStatus()
	case tabHealth:
		return m.fetchHealth()
	case tabLibraries:
		return m.fetchLibraries()
	}
	return nil
}

func (m Model) View() string {
	if m.width == 0 {
		return "Loading..."
	}

	var sections []string

	// Tab bar
	sections = append(sections, m.renderTabBar())

	// Content area
	contentHeight := m.height - 4 // tabs + help
	switch m.activeTab {
	case tabSearch:
		sections = append(sections, m.renderSearch(contentHeight))
	case tabGraph:
		sections = append(sections, m.renderGraph(contentHeight))
	case tabStatus:
		sections = append(sections, m.renderStatus(contentHeight))
	case tabLibraries:
		sections = append(sections, m.renderLibraries(contentHeight))
	case tabHealth:
		sections = append(sections, m.renderHealth(contentHeight))
	}

	// Help
	sections = append(sections, m.renderHelp())

	return lipgloss.JoinVertical(lipgloss.Left, sections...)
}

func (m Model) renderTabBar() string {
	var tabs []string
	for i, name := range tabNames {
		if tab(i) == m.activeTab {
			tabs = append(tabs, activeTab.Render(name))
		} else {
			tabs = append(tabs, inactiveTab.Render(name))
		}
	}
	row := lipgloss.JoinHorizontal(lipgloss.Top, tabs...)
	return tabBar.Width(m.width).Render(row)
}

func (m Model) renderHelp() string {
	var help string
	switch m.activeTab {
	case tabSearch:
		if m.searchInput.Focused() {
			help = "ctrl+n: navigate results • tab: next pane • ctrl+c: quit"
		} else {
			help = "j/k: move • enter: graph view • e: edit • /: search • tab: next pane • q: quit"
		}
	case tabGraph:
		help = fmt.Sprintf("[%s] j/k: move • enter: drill in • e: edit • esc: back • tab: next pane • q: quit", m.graphNode)
	case tabStatus:
		help = "tab: next pane • q: quit"
	case tabLibraries:
		help = "j/k: move • tab: next pane • q: quit"
	case tabHealth:
		help = "j/k: move • tab: next pane • q: quit"
	}
	return helpStyle.Width(m.width).Render(help)
}

// graphListLen returns the total number of items in graph view.
func (m Model) graphListLen() int {
	return len(m.graphResults) + len(m.graphBacklinks)
}

// graphList returns the combined graph items (outgoing then incoming).
func (m Model) graphList() []daemon.Result {
	items := make([]daemon.Result, 0, len(m.graphResults)+len(m.graphBacklinks))
	items = append(items, m.graphResults...)
	items = append(items, m.graphBacklinks...)
	return items
}

// openInEditor launches $EDITOR (or vim) for the given file path.
func (m Model) openInEditor(filePath string) tea.Cmd {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vim"
	}
	c := exec.Command(editor, filePath)
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return editorFinishedMsg{err}
	})
}
