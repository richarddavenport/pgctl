package engine

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/richarddavenport/pgctl/internal/config"
	"github.com/richarddavenport/pgctl/internal/pg"
	"github.com/richarddavenport/pgctl/internal/snapshot"
)

// DumpRequest is one snapshot to take.
type DumpRequest struct {
	Environment string
	Database    string

	// Dir is where the snapshot is written. Empty means the configured storage
	// directory, under the snapshot's own id.
	Dir string

	// At fixes the snapshot's timestamp and id. Zero means now.
	At time.Time

	// NoPush keeps the snapshot local even when a remote store is configured.
	NoPush bool
}

// Dump takes a snapshot of one database.
//
// The shape of the run: introspect the source, decide each table's fate from
// the rules, hand pg_dump everything it can do, and produce the rest here with
// COPY. The manifest is written last — an unfinished manifest is what stops a
// half-taken snapshot being restored.
func (e *Engine) Dump(ctx context.Context, req DumpRequest, report Reporter) (*snapshot.Manifest, error) {
	db, ok := e.database(req.Database)
	if !ok {
		return nil, fmt.Errorf("database %q is not declared in %s", req.Database, e.cfg.Source)
	}

	target, err := Resolve(ctx, e.cfg, e.root, req.Environment, req.Database)
	if err != nil {
		return nil, err
	}

	at := req.At
	if at.IsZero() {
		at = time.Now()
	}
	id := snapshot.NewID(target.Env.Name, req.Database, at)
	dir := req.Dir
	if dir == "" {
		dir = snapshot.Path(e.storageRoot(), id)
	}

	report.step("dump", fmt.Sprintf("snapshot %s from %s", id, target))

	conn, err := target.Connect(ctx, req.Database)
	if err != nil {
		return nil, err
	}
	defer conn.Close(ctx) //nolint:errcheck // nothing useful to do with a close failure

	cat, err := pg.Introspect(ctx, conn, db.ExcludeSchemas)
	if err != nil {
		return nil, err
	}
	if cat.ServerVersion < minServerVersion {
		return nil, fmt.Errorf("%s runs PostgreSQL %s: pgctl needs 16 or newer for zstd compression",
			target.Env.Name, formatVersion(cat.ServerVersion))
	}

	pgDumpVersion, err := binaryVersion(ctx, "pg_dump")
	if err != nil {
		return nil, err
	}

	extensions, err := pg.Extensions(ctx, conn)
	if err != nil {
		return nil, err
	}

	m := &snapshot.Manifest{
		ID:              id,
		Environment:     target.Env.Name,
		Database:        req.Database,
		StartedAt:       at,
		ServerVersion:   cat.ServerVersion,
		PgDumpVersion:   pgDumpVersion,
		Compression:     e.cfg.Defaults.Compression,
		Jobs:            target.Jobs(e.cfg.Defaults),
		ExcludedSchemas: db.ExcludeSchemas,
		Extensions:      extensions,
		ForeignKeys:     cat.FKs,
	}

	// Every table's fate, decided before anything runs, so that the plan the
	// operator was shown is the plan that executes.
	var excludeData []string
	for _, t := range cat.Tables {
		if !includedSchema(db, t.Name) {
			continue
		}
		rule := e.cfg.RuleFor(t.Name)
		entry := snapshot.TableEntry{
			Name:        t.Name,
			Data:        rule.Data,
			Where:       rule.Where,
			Why:         rule.Why,
			SourceBytes: t.Bytes,
			SourceRows:  t.EstimatedRows,
		}
		if rule.Data != config.DataAll {
			// pg_dump keeps the table's definition and skips its rows;
			// whatever rows are wanted are produced below.
			excludeData = append(excludeData, t.Name)
		}
		if rule.Data == config.DataFiltered {
			entry.File = filepath.ToSlash(filepath.Join(snapshot.FilteredDir, t.Name+".bin.zst"))
		}
		m.Tables = append(m.Tables, entry)
	}
	m.Warnings = append(m.Warnings, e.unmatchedRules(cat)...)

	// Excluding a schema is not free: a trigger on a table that is staying may
	// call a function in the schema that is going. Caught here rather than an
	// hour into the restore that fails on it.
	dangling, err := pg.DanglingTriggers(ctx, conn, db.ExcludeSchemas)
	if err != nil {
		return nil, err
	}
	if len(dangling) > 0 {
		m.Warnings = append(m.Warnings, fmt.Sprintf(
			"%d trigger(s) on retained tables call functions in excluded schema(s) — "+
				"restoring this snapshot will fail on every CREATE TRIGGER. "+
				"First: %s on %s calls %s. Keep the schema and exclude its data with a rule instead.",
			len(dangling), dangling[0].Trigger, dangling[0].Table, dangling[0].Function))
	}
	for _, w := range m.Warnings {
		report.warn(w)
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create snapshot directory: %w", err)
	}

	report.step("dump", fmt.Sprintf("reading %d tables with %d parallel jobs, compressing with %s",
		len(m.Tables), m.Jobs, m.Compression))
	report.step("dump", "writing to "+dir)
	if err := e.runPgDump(ctx, target, db, m, dir, excludeData, report); err != nil {
		return nil, err
	}

	if filtered := m.Filtered(); len(filtered) > 0 {
		report.step("filtered copy", fmt.Sprintf("%d tables pg_dump cannot filter", len(filtered)))
		if err := e.copyFiltered(ctx, target, m, dir, report); err != nil {
			return nil, err
		}
	}

	m.Bytes = dirSize(dir)
	m.FinishedAt = time.Now()
	if err := snapshot.Write(dir, m); err != nil {
		return nil, err
	}
	report.send(Event{Kind: EventProgress, Step: "dump", Bytes: m.Bytes,
		Message: fmt.Sprintf("snapshot %s written to %s (%s)", id, dir, humanBytes(m.Bytes))})

	// Uploaded as part of taking it, not as a separate command someone has to
	// remember: a nightly whose artifact is still on the runner when the runner
	// is recycled has not backed anything up.
	if !req.NoPush {
		if err := e.Push(ctx, id, report); err != nil {
			// The snapshot exists and is complete; failing to upload it is
			// worth failing the run over, but not worth deleting it over.
			return m, fmt.Errorf("snapshot %s is on disk but could not be uploaded: %w", id, err)
		}
	}

	report.send(Event{Kind: EventDone, Step: "dump", Bytes: m.Bytes,
		Message: fmt.Sprintf("snapshot %s complete (%s)", id, humanBytes(m.Bytes))})
	return m, nil
}

// runPgDump invokes pg_dump in directory format.
func (e *Engine) runPgDump(ctx context.Context, target *Target, db config.Database,
	m *snapshot.Manifest, dir string, excludeData []string, report Reporter) error {

	args := make([]string, 0, 10+len(db.Schemas)+len(db.ExcludeSchemas)+len(excludeData))
	args = append(args,
		"--format=directory",
		fmt.Sprintf("--jobs=%d", m.Jobs),
		"--compress="+m.Compression,
		// The archive is restored into environments whose roles differ from
		// the source's, and Azure hands out no superuser to restore them with.
		"--no-owner",
		"--no-acl",
		// Skipping fsync trades durability against a crash *during the dump*
		// for a materially faster dump. What protects a snapshot is the copy
		// in blob storage, not whether this local one survived a power cut.
		"--no-sync",
		// Never prompt. Without this, a wrong or missing password makes pg_dump
		// block on a password prompt it reads from /dev/tty — not stdin — so
		// redirecting stdin does not save it. In CI, or in any backgrounded
		// run, that is an indefinite hang with no output: a far worse failure
		// than an error message.
		"--no-password",
		"--file="+filepath.Join(dir, snapshot.DumpDir),
		"--host="+target.Host,
		fmt.Sprintf("--port=%d", target.Port),
		"--username="+target.User,
	)
	for _, s := range db.Schemas {
		args = append(args, "--schema="+s)
	}
	for _, s := range db.ExcludeSchemas {
		args = append(args, "--exclude-schema="+s)
	}
	for _, t := range excludeData {
		args = append(args, "--exclude-table-data="+t)
	}
	args = append(args, target.Database)

	cmd := exec.CommandContext(ctx, "pg_dump", args...)
	cmd.Env = target.SubprocessEnv(os.Environ())
	var errb bytes.Buffer
	cmd.Stderr = &errb

	// pg_dump says nothing until it is finished, and a dump of a real database
	// takes minutes. Watching the archive grow is the only progress signal
	// available without --verbose (whose output is per-table noise, not a
	// measure of how far along it is), and it is the one that answers the
	// question an operator actually has: is this working, and how fast.
	stop := watchGrowth(ctx, filepath.Join(dir, snapshot.DumpDir), "dump", report)
	err := cmd.Run()
	stop()

	if err != nil {
		return fmt.Errorf("pg_dump: %w: %s", err, lastLines(errb.String(), 5))
	}
	return nil
}

// watchGrowth reports the size of a directory as it fills, once a second, and
// returns a function that stops it.
func watchGrowth(ctx context.Context, dir, step string, report Reporter) func() {
	if report == nil {
		return func() {}
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})

	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()

		// Rate over the last interval, not an average since the watch began.
		// pg_dump spends its first seconds reading the catalog and writing
		// nothing, so a cumulative average reports a throughput the dump never
		// had and takes minutes to recover from — it read 78 KB/s for a link
		// doing several megabytes a second.
		var lastBytes int64
		lastAt := time.Now()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				n := dirSize(dir)
				rate := ""
				if seconds := now.Sub(lastAt).Seconds(); seconds > 0 && n > lastBytes {
					rate = fmt.Sprintf(", %s/s", humanBytes(int64(float64(n-lastBytes)/seconds)))
				}
				lastBytes, lastAt = n, now
				report.send(Event{Kind: EventProgress, Step: step, Bytes: n,
					Message: fmt.Sprintf("%s written%s", humanBytes(n), rate)})
			}
		}
	}()

	return func() {
		cancel()
		<-done
	}
}

// unmatchedRules reports rules that select nothing. A rule naming a table that
// has been renamed silently stops filtering it, and the symptom — a 20 GB table
// in a snapshot that used to be small — shows up far from the cause.
func (e *Engine) unmatchedRules(cat *pg.Catalog) []string {
	var warnings []string
	for _, r := range e.cfg.Rules {
		matched := false
		for _, t := range cat.Tables {
			if config.MatchPattern(r.Table, t.Name) {
				matched = true
				break
			}
		}
		if !matched {
			warnings = append(warnings, fmt.Sprintf("rule %q matches no table in this database", r.Table))
		}
	}
	return warnings
}

func includedSchema(db config.Database, table string) bool {
	schema, _, _ := strings.Cut(table, ".")
	for _, s := range db.ExcludeSchemas {
		if s == schema {
			return false
		}
	}
	if len(db.Schemas) == 0 {
		return true
	}
	for _, s := range db.Schemas {
		if s == schema {
			return true
		}
	}
	return false
}

// minServerVersion is 16: below it there is no zstd, and pgctl's whole claim to
// being faster than a shell script starts with compression.
const minServerVersion = 160000

func formatVersion(v int) string {
	if v == 0 {
		return "unknown"
	}
	return fmt.Sprintf("%d.%d", v/10000, v%10000)
}

var versionNumber = regexp.MustCompile(`\d+(\.\d+)*`)

// binaryVersion records which pg_dump or pg_restore ran. A restore needs a
// pg_restore at least as new as the archive's writer, and that is a rule
// nobody remembers until an archive will not load.
func binaryVersion(ctx context.Context, bin string) (string, error) {
	out, err := exec.CommandContext(ctx, bin, "--version").Output()
	if err != nil {
		return "", fmt.Errorf("%s is not on PATH (install the PostgreSQL 17 client tools): %w", bin, err)
	}
	return versionNumber.FindString(string(out)), nil
}

func dirSize(dir string) int64 {
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		// A size is a report, not a guarantee: an entry that cannot be walked
		// is skipped rather than turned into a failed snapshot.
		if err != nil || d.IsDir() {
			return nil //nolint:nilerr // deliberate: see above
		}
		if info, err := d.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

// lastLines trims a subprocess's stderr to the part that says what went wrong.
// pg_dump's failures end with the reason; its beginning is progress.
func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "; ")
}

// HumanBytes formats a byte count for a person.
func HumanBytes(b int64) string { return humanBytes(b) }

func humanBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	units := []string{"KB", "MB", "GB", "TB", "PB"}
	value := float64(b)
	for _, u := range units {
		value /= unit
		if value < unit {
			return fmt.Sprintf("%.1f %s", value, u)
		}
	}
	return fmt.Sprintf("%.1f EB", value)
}
