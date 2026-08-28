package tui

import tea "github.com/charmbracelet/bubbletea"

// filterKey handles typing in the / filter.
//
// The filter narrows only the focused panel. Filtering all of them from one box
// would empty the panels above and below the one being searched, which reads as
// data loss rather than as a filter.
func (m *Model) filterKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc":
		m.filtering = false
		m.filter = ""
		return m, m.onSelectionChanged()
	case "enter":
		m.filtering = false
		return m, m.onSelectionChanged()
	case "backspace":
		if m.filter != "" {
			m.filter = m.filter[:len(m.filter)-1]
		}
	case "ctrl+u":
		m.filter = ""
	default:
		if len(key) == 1 {
			m.filter += key
		}
	}
	// The cursor goes to the top of what is left: keeping its index would leave
	// it pointing at a different row than it was on.
	m.cursors[m.focus] = 0
	return m, m.onSelectionChanged()
}
