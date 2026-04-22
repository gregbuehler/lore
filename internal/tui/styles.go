package tui

import "github.com/charmbracelet/lipgloss"

var (
	// Colors
	accent    = lipgloss.Color("#7C3AED") // purple
	subtle    = lipgloss.Color("#6B7280")
	highlight = lipgloss.Color("#F59E0B") // amber
	dimText   = lipgloss.Color("#9CA3AF")
	errColor  = lipgloss.Color("#EF4444")

	// Tab bar
	activeTab = lipgloss.NewStyle().
			Bold(true).
			Foreground(accent).
			Padding(0, 2)

	inactiveTab = lipgloss.NewStyle().
			Foreground(subtle).
			Padding(0, 2)

	tabBar = lipgloss.NewStyle().
		BorderStyle(lipgloss.NormalBorder()).
		BorderBottom(true).
		BorderForeground(subtle)

	// Search
	searchInput = lipgloss.NewStyle().
			Padding(0, 1)

	resultTitle = lipgloss.NewStyle().
			Bold(true)

	resultType = lipgloss.NewStyle().
			Foreground(accent)

	resultPath = lipgloss.NewStyle().
			Foreground(dimText)

	resultScore = lipgloss.NewStyle().
			Foreground(subtle)

	resultAbstract = lipgloss.NewStyle().
			Foreground(dimText).
			Italic(true)

	selectedResult = lipgloss.NewStyle().
			Background(lipgloss.Color("#1F2937")).
			Padding(0, 1)

	// Graph
	edgeType = lipgloss.NewStyle().
			Foreground(highlight).
			Bold(true)

	nodeTitle = lipgloss.NewStyle().
			Bold(true)

	nodeType = lipgloss.NewStyle().
			Foreground(accent)

	depthIndent = lipgloss.NewStyle().
			Foreground(subtle)

	// Status
	statusLabel = lipgloss.NewStyle().
			Foreground(subtle).
			Width(16)

	statusValue = lipgloss.NewStyle().
			Bold(true)

	// Help
	helpStyle = lipgloss.NewStyle().
			Foreground(dimText).
			Padding(0, 1)
)
