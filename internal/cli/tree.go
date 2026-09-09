package cli

import (
	"sort"
	"strings"

	"github.com/richarddavenport/tuikit/spec"

	"github.com/richarddavenport/pgctl/internal/config"
	"github.com/richarddavenport/pgctl/internal/tui"
)

// Commands is pgctl, declared once.
func Commands() spec.Command {
	return spec.Command{
		Name:  "pgctl",
		Short: "move PostgreSQL data between environments",
		Long: "A snapshot may be named in full (prd/product-development/20260828T030000Z),\n" +
			"by its timestamp alone when that is unambiguous, or as <env>/latest.",
		Commands: []spec.Command{
			{
				Name:  "browse",
				Short: "open the terminal UI",
				// A Screen, so spec also gives this --snapshot and --script:
				// an agent can capture the interface without writing a test,
				// and finds out how from --help. Decision 10's second capture
				// mechanism, the one that reaches what a keystroke reaches.
				Screen: "browse",
				Flags:  common(),
				Run:    do(runBrowse),
			},
			{
				Name:  "snapshot",
				Short: "take a snapshot of every declared database",
				Flags: common(
					spec.Flag{Name: "from", Kind: spec.String, Complete: connections,
						Help: "connection to snapshot"},
					spec.Flag{Name: "db", Kind: spec.String,
						Help: "database to snapshot (default: every declared database)"},
					spec.Flag{Name: "verbose", Short: "v", Help: "report every table"},
					spec.Flag{Name: "to-storage", Kind: spec.String, Complete: destinations,
						Help: "where to put it: local, a declared remote, several " +
							"comma-separated, or all. Required once a remote is declared"},
				),
				Run: do(runSnapshot),
			},
			{
				Name:  "ls",
				Short: "list snapshot runs, on disk and in storage",
				Flags: common(spec.Flag{Name: "from", Kind: spec.String, Complete: connections,
					Help: "only this connection"}),
				Run: do(runList),
			},
			{
				Name:  "plan",
				Short: "show what an apply would do, and refuse if it cannot be done safely",
				Args:  []spec.Arg{{Name: "snapshot", Required: true, Complete: snapshots}},
				Flags: applyFlagSet(),
				Run:   do(runPlan),
			},
			{
				Name:  "apply",
				Short: "restore a snapshot, or part of one",
				Args:  []spec.Arg{{Name: "snapshot", Required: true, Complete: snapshots}},
				Flags: applyFlagSet(),
				// The region this acts on, and the key that does the same
				// thing from the keyboard. The Key is not optional once there
				// is a Target — guard.Reachable holds that closed, because an
				// agent cannot click and a multiplexer may eat a right-click
				// before pgctl ever sees it.
				Target: tui.RegSnapshotsRow,
				Key:    "a",
				Run:    do(runApply),
			},
			{
				Name:  "move",
				Short: "refresh one environment from another, keeping no snapshot",
				Flags: common(
					spec.Flag{Name: "from", Kind: spec.String, Complete: connections,
						Help: "connection to read"},
					spec.Flag{Name: "to", Kind: spec.String, Complete: connections,
						Help: "connection to write"},
					spec.Flag{Name: "db", Kind: spec.String,
						Help: "database to move (default: every declared database)"},
					spec.Flag{Name: "set", Kind: spec.String,
						Help: "move only this table set"},
					spec.Flag{Name: "tables", Kind: spec.String,
						Help: "move only these tables (comma separated, schema-qualified)"},
					spec.Flag{Name: "widen",
						Help: "include tables the selection references but does not name"},
					spec.Flag{Name: "keep",
						Help: "keep the staged snapshot instead of deleting it"},
					spec.Flag{Name: "verbose", Short: "v", Help: "report every table"},
					spec.Flag{Name: "yes", Help: "skip the confirmation prompt"},
					spec.Flag{Name: "confirm", Kind: spec.String, Complete: connections,
						Help: "name of the connection being written to, required for a guarded one"},
				),
				Target: tui.RegConnectionsRow,
				Key:    "m",
				Run:    do(runMove),
			},
			{
				Name:  "prune",
				Short: "report what a retention policy would delete",
				Flags: common(
					spec.Flag{Name: "from", Kind: spec.String, Complete: connections,
						Help: "only this connection"},
					spec.Flag{Name: "apply",
						Help: "actually delete; without it, prune only reports"},
				),
				Target: tui.RegSnapshotsRow,
				Key:    "p",
				Run:    do(runPrune),
			},
		},
	}
}

// common puts --config on every command.
//
// spec has no notion of an inherited flag, and that is the right call for a
// tool of this size: a flag that appears in one command's help and silently
// works in another is worse than one repeated. It is repeated HERE rather than
// at seven call sites, so the help text cannot drift between them.
func common(rest ...spec.Flag) []spec.Flag {
	return append([]spec.Flag{
		{Name: "config", Kind: spec.String, Help: "path to a pgctl config"},
	}, rest...)
}

// applyFlagSet is shared by plan and apply, since a plan is the first half of
// an apply and taking different flags would make the preview a different
// question from the thing it previews.
func applyFlagSet() []spec.Flag {
	return common(
		spec.Flag{Name: "to", Kind: spec.String, Complete: connections,
			Help: "connection to apply to"},
		spec.Flag{Name: "db", Kind: spec.String,
			Help: "restore only these databases of the run (comma separated; " +
				"default: every one it covers)"},
		spec.Flag{Name: "set", Kind: spec.String, Help: "restore only this table set"},
		spec.Flag{Name: "tables", Kind: spec.String,
			Help: "restore only these tables (comma separated, schema-qualified)"},
		spec.Flag{Name: "widen",
			Help: "include tables the selection references but does not name"},
		spec.Flag{Name: "verbose", Short: "v", Help: "report every table"},
		spec.Flag{Name: "yes", Help: "skip the confirmation prompt"},
		spec.Flag{Name: "confirm", Kind: spec.String, Complete: connections,
			Help: "name of the connection being written to, required for a guarded one"},
	)
}

// destinations completes a storage destination.
//
// From the config and nothing else, like every completer here: a tab key that
// listed what a container actually holds would open a network connection, and
// people stop pressing keys that pause.
func destinations(prefix string) []string {
	cfg, err := config.Load("")
	if err != nil {
		return nil
	}
	out := []string{}
	for _, name := range append(cfg.Destinations(), "all") {
		if strings.HasPrefix(name, prefix) {
			out = append(out, name)
		}
	}
	return out
}

// connections completes a connection name from the config.
//
// The same completer the shell and the interface both use, so they cannot offer
// different answers. It reads the config and nothing else — a completion that
// probed a server would make pressing tab open a session to production.
func connections(prefix string) []string {
	cfg, err := config.Load("")
	if err != nil {
		return nil
	}
	var out []string
	for _, conn := range cfg.All() {
		if strings.HasPrefix(conn.Name, prefix) {
			out = append(out, conn.Name)
		}
	}
	sort.Strings(out)
	return out
}

// snapshots completes a snapshot id.
//
// Local only, and deliberately: listing what is in blob storage costs a network
// round trip, and a tab key that pauses for a remote listing is a tab key
// people stop pressing. `<env>/latest` is always offered because it is the name
// a script should use.
func snapshots(prefix string) []string {
	cfg, err := config.Load("")
	if err != nil {
		return nil
	}
	out := []string{}
	for _, conn := range cfg.All() {
		if name := conn.Name + "/latest"; strings.HasPrefix(name, prefix) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}
