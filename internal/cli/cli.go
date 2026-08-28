// Package cli is pgctl's headless front end: the same engine the TUI drives,
// with flags instead of keystrokes, so that a nightly CI job and a person at a
// terminal perform the same operation and report it the same way.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/richarddavenport/pgctl/internal/config"
	"github.com/richarddavenport/pgctl/internal/engine"
)

// Version is stamped by main.
var Version = "dev"

// Commands are the subcommand names main routes here.
var Commands = map[string]bool{
	"snapshot": true,
	"ls":       true,
	"plan":     true,
	"apply":    true,
}

// Run dispatches a headless command and returns a process exit code.
func Run(args []string) int {
	// Interrupt cancels the operation rather than killing the process, so that
	// the failure hooks that bring an environment back up still run.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch args[0] {
	case "snapshot":
		err = runSnapshot(ctx, args[1:])
	case "ls":
		err = runList(ctx, args[1:])
	case "plan":
		err = runPlan(ctx, args[1:])
	case "apply":
		err = runApply(ctx, args[1:])
	default:
		err = fmt.Errorf("unknown command %q", args[0])
	}

	if err != nil {
		var refusal *engine.RefusalError
		if errors.As(err, &refusal) {
			// A refusal is a considered decision, not a crash. Say it without
			// the "pgctl:" prefix that makes a deliberate stop look like a bug.
			fmt.Fprintln(os.Stderr, refusal.Reason)
			return 2
		}
		fmt.Fprintln(os.Stderr, "pgctl:", err)
		return 1
	}
	return 0
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
// first non-flag, and an operator should not have to know that.
func Permute(fs *flag.FlagSet, args []string) []string {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			positional = append(positional, a)
			continue
		}
		flags = append(flags, a)
		name := strings.TrimLeft(strings.SplitN(a, "=", 2)[0], "-")
		// A flag that takes a value and was not written as --flag=value
		// consumes the next argument.
		if !strings.Contains(a, "=") && i+1 < len(args) && takesValue(fs, name) {
			i++
			flags = append(flags, args[i])
		}
	}
	return append(flags, positional...)
}

func takesValue(fs *flag.FlagSet, name string) bool {
	f := fs.Lookup(name)
	if f == nil {
		return false
	}
	if b, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && b.IsBoolFlag() {
		return false
	}
	return true
}
