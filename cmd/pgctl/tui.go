package main

import "errors"

// runTUI opens the terminal UI. Not yet built — the engine and the headless
// commands came first so that the TUI has something to drive and nothing of its
// own to be correct about.
func runTUI(_ string) error {
	return errors.New("the terminal UI is not built yet — see `pgctl help` for the headless commands")
}
