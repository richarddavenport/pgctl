// Package cli is pgctl's command tree, declared ONCE.
//
// The headless front end, the TUI screen a command opens, the entry an agent
// reads in `pgctl describe --json`, the shell completions and the context menu
// for a region all come from this one declaration. Not five descriptions kept
// in step — one, read five ways.
//
// It is a PEER of the interface over the same engine rather than a wrapper
// around it, so a nightly CI job and a person at a terminal perform the same
// operation, and a refusal comes from the engine and reads the same in both.
package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/richarddavenport/tuikit/spec"

	"github.com/richarddavenport/pgctl/internal/config"
	"github.com/richarddavenport/pgctl/internal/engine"
)

// do adapts a command body to spec's exit-code contract.
//
// One place, so that every command reports a refusal the same way and none of
// them has to remember. Interrupt cancels the operation rather than killing the
// process, so the failure hooks that bring an environment back up still run.
func do(fn func(context.Context, spec.Call) error) func(spec.Call) int {
	return func(c spec.Call) int {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		err := fn(ctx, c)
		if err == nil {
			return spec.OK
		}

		var refusal *engine.RefusalError
		if errors.As(err, &refusal) {
			// A refusal is a considered decision, not a crash. Said without
			// the "pgctl:" prefix that makes a deliberate stop look like a bug.
			//
			// spec.Drift, and the reading is deliberate rather than a
			// coincidence of numbers: its contract calls 2 "a dry run that
			// found something — a finding, not a failure", and a refusal is
			// exactly that. pgctl looked, and what it found is a reason not to
			// proceed. `pgctl plan` is a dry run by definition, and an apply
			// refuses before it has touched anything, so both are findings and
			// neither is a failure to look.
			// Nothing useful to do if stderr will not take it: the exit code
			// carries the refusal too, which is the half a script reads.
			fmt.Fprintln(c.Err, refusal.Reason) //nolint:errcheck // see above
			return spec.Drift
		}
		fmt.Fprintln(c.Err, "pgctl:", err) //nolint:errcheck // as above
		return spec.Fail
	}
}

// load finds the config and builds an engine.
func load(configPath string) (*engine.Engine, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, err
	}
	for _, w := range cfg.Warnings {
		fmt.Fprintln(os.Stderr, "warning:", w)
	}
	return engine.New(cfg), nil
}

// printer renders the engine's event stream as lines. Steps are the structure,
// tables are the detail, and a warning is prefixed so it survives a skim of a
// CI log.
func printer(verbose bool) engine.Reporter {
	return func(ev engine.Event) {
		switch ev.Kind {
		case engine.EventStep:
			fmt.Printf("→ %s: %s\n", ev.Step, ev.Message)
		case engine.EventTable:
			if verbose {
				fmt.Printf("  %s: %s\n", ev.Table, ev.Message)
			}
		case engine.EventProgress:
			if verbose {
				fmt.Printf("  %s\n", ev.Message)
			}
		case engine.EventWarning:
			fmt.Printf("! %s\n", ev.Message)
		case engine.EventDone:
			fmt.Printf("✓ %s\n", ev.Message)
		case engine.EventFailed:
			fmt.Fprintf(os.Stderr, "✗ %s\n", ev.Message)
		}
	}
}

// splitList parses a comma-separated flag value.
func splitList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Permute moves flags ahead of positional arguments so that `pgctl apply ID
// --to qat` works as well as `pgctl apply --to qat ID`. flag.Parse stops at the
