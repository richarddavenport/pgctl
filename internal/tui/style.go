package tui

import "github.com/charmbracelet/lipgloss"

// Colours are named for their role rather than their hue, so a theme change is
// one edit here.
var (
	accent  = lipgloss.AdaptiveColor{Light: "#2563eb", Dark: "#60a5fa"}
	muted   = lipgloss.AdaptiveColor{Light: "#6b7280", Dark: "#9ca3af"}
	danger  = lipgloss.AdaptiveColor{Light: "#b91c1c", Dark: "#f87171"}
	warning = lipgloss.AdaptiveColor{Light: "#b45309", Dark: "#fbbf24"}
	ok      = lipgloss.AdaptiveColor{Light: "#15803d", Dark: "#4ade80"}

	selectFg   = lipgloss.AdaptiveColor{Light: "#ffffff", Dark: "#111827"}
	selectedBg = lipgloss.AdaptiveColor{Light: "#2563eb", Dark: "#60a5fa"}
)

var (
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(accent)
	headerStyle   = lipgloss.NewStyle().Bold(true).Foreground(muted)
	mutedStyle    = lipgloss.NewStyle().Foreground(muted)
	accentStyle   = lipgloss.NewStyle().Foreground(accent)
	dangerStyle   = lipgloss.NewStyle().Foreground(danger).Bold(true)
	warnStyle     = lipgloss.NewStyle().Foreground(warning)
	okStyle       = lipgloss.NewStyle().Foreground(ok)
	footerStyle   = lipgloss.NewStyle().Foreground(muted)
	selectedStyle = lipgloss.NewStyle().Foreground(selectFg).Background(selectedBg).Bold(true)

	// currentStyle marks the selected row of a panel that does not have the
	// keys, so the hierarchy stays legible: you can see which environment the
	// databases belong to while your cursor is somewhere else entirely.
	currentStyle = lipgloss.NewStyle().Bold(true).Foreground(accent)

	panelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).BorderForeground(muted).Padding(0, 1)
	focusedPanelStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).BorderForeground(accent).Padding(0, 1)

	tabStyle       = lipgloss.NewStyle().Foreground(muted).Padding(0, 1)
	activeTabStyle = lipgloss.NewStyle().Foreground(accent).Bold(true).Underline(true).Padding(0, 1)

	boxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).BorderForeground(accent).Padding(1, 2)
)
