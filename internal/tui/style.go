// Package tui is pgctl's terminal UI. It drives the same engine the headless
// commands do and holds no logic of its own about what is safe: a refusal is
// the engine's decision, rendered here.
package tui

import "github.com/charmbracelet/lipgloss"

// Colours are named for their role rather than their hue, so that a theme
// change is one edit here.
var (
	accent     = lipgloss.AdaptiveColor{Light: "#2563eb", Dark: "#60a5fa"}
	muted      = lipgloss.AdaptiveColor{Light: "#6b7280", Dark: "#9ca3af"}
	danger     = lipgloss.AdaptiveColor{Light: "#b91c1c", Dark: "#f87171"}
	warning    = lipgloss.AdaptiveColor{Light: "#b45309", Dark: "#fbbf24"}
	ok         = lipgloss.AdaptiveColor{Light: "#15803d", Dark: "#4ade80"}
	selectFg   = lipgloss.AdaptiveColor{Light: "#ffffff", Dark: "#111827"}
	selectedBg = lipgloss.AdaptiveColor{Light: "#2563eb", Dark: "#60a5fa"}
)

var (
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(accent)
	headerStyle   = lipgloss.NewStyle().Bold(true).Foreground(muted)
	mutedStyle    = lipgloss.NewStyle().Foreground(muted)
	dangerStyle   = lipgloss.NewStyle().Foreground(danger).Bold(true)
	warnStyle     = lipgloss.NewStyle().Foreground(warning)
	okStyle       = lipgloss.NewStyle().Foreground(ok)
	selectedStyle = lipgloss.NewStyle().Foreground(selectFg).Background(selectedBg).Bold(true)
	accentStyle   = lipgloss.NewStyle().Foreground(accent)
	footerStyle   = lipgloss.NewStyle().Foreground(muted)
	boxStyle      = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).
			BorderForeground(muted).Padding(0, 1)
)
