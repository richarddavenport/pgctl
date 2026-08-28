// pgctl moves PostgreSQL data between environments: nightly snapshots, whole
// database refreshes, and migrations of individual tables or table sets that
// respect the foreign keys between them.
//
// Everything project-specific lives in a pgctl.yaml — see the README.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/richarddavenport/pgctl/internal/cli"
)

// Stamped by the release workflow via -ldflags; "dev" for local builds.
var (
	version = "dev"
	commit  = "none"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "--version", "-v":
			fmt.Printf("pgctl %s (commit %s)\n", version, commit)
			return
		case "help", "--help", "-h":
			usage()
			return
		}
		if cli.Commands[os.Args[1]] {
			cli.Version = version
			os.Exit(cli.Run(os.Args[1:]))
		}
	}

	configPath := flag.String("config", "", "path to a pgctl config")
	_ = flag.CommandLine.Parse(cli.Permute(flag.CommandLine, os.Args[1:]))

	if err := runTUI(*configPath); err != nil {
		fmt.Fprintln(os.Stderr, "pgctl:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Print(`pgctl — move PostgreSQL data between environments

  pgctl                       open the terminal UI
  pgctl snapshot --env prd    take a snapshot of every declared database
  pgctl ls                    list snapshots
  pgctl plan <snapshot> --to qat [--set claims]
                              show what an apply would do, and refuse if it cannot be done safely
  pgctl apply <snapshot> --to qat [--set claims] [--widen] [--yes]
                              restore a snapshot, or part of one

A snapshot may be named in full (prd/product-development/20260828T030000Z), by
its timestamp alone when that is unambiguous, or as <env>/latest.

  pgctl move --from prd --to qat [--set claims]
                              refresh one environment from another, keeping no snapshot

  pgctl prune                 report what a retention policy would delete (--apply to do it)

  pgctl version               print the version
  pgctl help                  this text
`)
}
