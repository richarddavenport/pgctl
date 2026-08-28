package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/richarddavenport/pgctl/internal/config"
	"github.com/richarddavenport/pgctl/internal/engine"
)

// Run opens the UI against a config.
func Run(configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	model := New(engine.New(cfg))
	for _, w := range cfg.Warnings {
		model.events = append(model.events, engine.Event{Kind: engine.EventWarning, Message: w})
	}

	// The alternate screen keeps the operator's scrollback: a refresh is
	// something people run in a terminal they were already working in.
	p := tea.NewProgram(model, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("terminal ui: %w", err)
	}
	return nil
}
