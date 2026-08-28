// Package snapshot describes what a dump is: an immutable, catalogued artifact
// that knows where it came from and what is inside it.
//
// The tooling this replaces wrote `$db.sql` into a working directory and left
// the operator to remember the rest. Every question that matters afterwards —
// which environment, when, at what schema version, did it include the 20 GB
// table — was unanswerable. A manifest is the answer to all of them, and it is
// what makes a restore something other than an act of faith.
package snapshot

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/richarddavenport/pgctl/internal/config"
	"github.com/richarddavenport/pgctl/internal/pg"
)

// ManifestName is the manifest's filename inside a snapshot directory.
const ManifestName = "manifest.json"

// DumpDir and FilteredDir are the two producers' output, side by side inside a
// snapshot: pg_dump's directory-format tree, and the COPY sidecars for tables
// pg_dump cannot filter.
const (
	DumpDir     = "dump"
	FilteredDir = "filtered"
)

// TimeLayout is the timestamp form used in a snapshot's id: sortable, UTC, and
// safe in a path and in a blob name.
const TimeLayout = "20060102T150405Z"

// Manifest is a snapshot's record of itself.
type Manifest struct {
	// SchemaVersion guards against a future pgctl misreading this file.
	SchemaVersion int `json:"schemaVersion"`

	ID          string `json:"id"`
	Environment string `json:"environment"`
	Database    string `json:"database"`

	StartedAt  time.Time `json:"startedAt"`
	FinishedAt time.Time `json:"finishedAt,omitzero"`

	// ServerVersion and PgDumpVersion are both recorded because a restore is
	// only safe when pg_restore is at least as new as the pg_dump that wrote
	// the archive, and because "it worked last week" is usually a version
	// change nobody wrote down.
	ServerVersion int    `json:"serverVersion"`
	PgDumpVersion string `json:"pgDumpVersion"`

	Compression string `json:"compression"`
	Jobs        int    `json:"jobs"`

	ExcludedSchemas []string `json:"excludedSchemas,omitempty"`

	Tables []TableEntry `json:"tables"`

	// ForeignKeys is the source's referential structure at the moment of the
	// dump. A restore plans against the *target's* constraints — those are what
	// break — but keeping the source's makes schema drift between the two
	// detectable before the load rather than during it.
	ForeignKeys []pg.FK `json:"foreignKeys,omitempty"`

	// Bytes is the snapshot's size on disk once written.
	Bytes int64 `json:"bytes"`

	Warnings []string `json:"warnings,omitempty"`
}

// TableEntry is one table's fate in this snapshot.
type TableEntry struct {
	// Name is schema-qualified.
	Name string `json:"name"`

	// Data is how much of the table came along.
	Data config.DataMode `json:"data"`

	// Where is the predicate that selected the rows, for a filtered table.
	Where string `json:"where,omitempty"`

	// Why records the reason a table is not whole, copied from the rule, so
	// that an operator looking at a short table in QAT has the explanation in
	// front of them.
	Why string `json:"why,omitempty"`

	// File is the sidecar's path relative to the snapshot root, for a filtered
	// table. Empty when pg_dump produced the data.
	File string `json:"file,omitempty"`

	// Columns is the column list a filtered table's binary COPY was written
	// with, in order. Binary COPY has no header naming them, so loading the
	// file requires knowing what it holds — and a column added upstream since
	// the dump is exactly when guessing goes wrong.
	Columns []string `json:"columns,omitempty"`

	// SourceBytes and SourceRows are what the table measured on the source,
	// before compression: the numbers a plan reports and progress is estimated
	// against.
	SourceBytes int64 `json:"sourceBytes"`
	SourceRows  int64 `json:"sourceRows"`

	// Rows is the number actually copied, for a filtered table.
	Rows int64 `json:"rows,omitempty"`
}

// NewID builds a snapshot id from its environment, database and instant.
func NewID(env, database string, at time.Time) string {
	return strings.Join([]string{env, database, at.UTC().Format(TimeLayout)}, "/")
}

func (m *Manifest) String() string { return m.ID }

// Path is a snapshot's location under a storage root. The id's slashes are the
// directory structure, so a store lists an environment's snapshots by listing a
// prefix.
func Path(root, id string) string {
	return filepath.Join(root, filepath.FromSlash(id))
}

// Write saves the manifest into a snapshot directory.
func Write(dir string, m *Manifest) error {
	m.SchemaVersion = 1
	sort.Slice(m.Tables, func(i, j int) bool { return m.Tables[i].Name < m.Tables[j].Name })

	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("encode manifest: %w", err)
	}
	data = append(data, '\n')

	// Written to a temporary name and renamed, so a manifest that exists is
	// always a manifest that is complete — a half-written one would describe a
	// snapshot that does not match it.
	tmp := filepath.Join(dir, ManifestName+".tmp")
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	return os.Rename(tmp, filepath.Join(dir, ManifestName))
}

// Read loads the manifest from a snapshot directory.
func Read(dir string) (*Manifest, error) {
	data, err := os.ReadFile(filepath.Join(dir, ManifestName))
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse manifest in %s: %w", dir, err)
	}
	if m.SchemaVersion > 1 {
		return nil, fmt.Errorf("snapshot %s was written by a newer pgctl (manifest version %d)",
			m.ID, m.SchemaVersion)
	}
	return &m, nil
}

// Table returns the entry for a table.
func (m *Manifest) Table(name string) (TableEntry, bool) {
	for _, t := range m.Tables {
		if t.Name == name {
			return t, true
		}
	}
	return TableEntry{}, false
}

// TableNames returns every table in the snapshot, sorted.
func (m *Manifest) TableNames() []string {
	out := make([]string, 0, len(m.Tables))
	for _, t := range m.Tables {
		out = append(out, t.Name)
	}
	sort.Strings(out)
	return out
}

// Filtered returns the tables whose data came from a COPY sidecar rather than
// from pg_dump.
func (m *Manifest) Filtered() []TableEntry {
	var out []TableEntry
	for _, t := range m.Tables {
		if t.Data == config.DataFiltered {
			out = append(out, t)
		}
	}
	return out
}

// Complete reports whether the snapshot finished. An interrupted dump leaves a
// manifest with no FinishedAt, which is what stops it being restored.
func (m *Manifest) Complete() bool { return !m.FinishedAt.IsZero() }

// Age is how old the snapshot is, for the listing.
func (m *Manifest) Age() time.Duration { return time.Since(m.StartedAt) }
