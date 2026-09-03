// Command pgctl moves PostgreSQL data between environments: nightly snapshots,
// whole-database refreshes, and migrations of individual tables or table sets
// that respect the foreign keys between them.
//
//	pgctl                    open the terminal UI
//	pgctl ls                 the same thing, for a script
//	pgctl describe --json    the whole surface, in one call
//
// Everything project-specific lives in a pgctl.yaml — see the README.
package main

import (
	"fmt"
	"os"

	"github.com/richarddavenport/tuikit/spec"

	"github.com/richarddavenport/pgctl/internal/cli"
	"github.com/richarddavenport/pgctl/internal/tui"
)

// Stamped by the release workflow via -ldflags; "dev" for local builds.
var (
	version = "dev"
	commit  = "none"
)

// main decides when the process ends.
//
// Nothing in spec calls os.Exit, which is why this is not cobra: pgctl's
// exit-code contract is richer than ok-or-not — 2 is a refusal, a considered
// decision rather than a crash — and a framework that owns the exit is a
// framework that flattens it. Run returns a code; this function spends it.
//
// The dual-mode entry lives here too, because main is the only place that
// knows a bare `pgctl` means "show me": no arguments opens the interface,
// anything else is a command.
func main() {
	root := cli.Commands()
	argv := os.Args[1:]
	if len(argv) == 0 {
		argv = []string{"browse"}
	}

	switch argv[0] {
	case "version", "--version":
		// Not -v. It used to mean version here and verbose on every
		// subcommand, which is two meanings for one letter separated only by
		// position; verbose is the one worth having on the short flag.
		fmt.Printf("pgctl %s (commit %s)\n", version, commit)
		os.Exit(spec.OK)

	case "describe":
		// The whole surface in one call, so an agent never has to grep for it:
		// the commands, their flags and args, the regions, the palette's nine
		// roles with the reason for each, and the glyph set.
		out, err := spec.Describe(root, version, tui.Palette, tui.Glyphs).JSON()
		if err != nil {
			fmt.Fprintln(os.Stderr, "pgctl:", err)
			os.Exit(spec.Fail)
		}
		fmt.Println(string(out))
		os.Exit(spec.OK)

	case "completion":
		if len(argv) < 2 {
			fmt.Fprintln(os.Stderr, "usage: pgctl completion <bash|zsh|fish>")
			os.Exit(spec.Fail)
		}
		if err := spec.CompletionScript(argv[1], "pgctl", os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "pgctl:", err)
			os.Exit(spec.Fail)
		}
		os.Exit(spec.OK)

	case "__complete":
		// What the shell calls back into. The scripts know nothing about the
		// commands, so they cannot go stale when one is added — and the
		// completers are the same functions the interface's own pickers use.
		for _, word := range spec.Complete(root, argv[1:]) {
			fmt.Println(word)
		}
		os.Exit(spec.OK)
	}

	os.Exit(spec.Run(root, argv, os.Stdout, os.Stderr))
}
