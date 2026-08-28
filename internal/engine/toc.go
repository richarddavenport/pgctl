package engine

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// TOCEntry is one item in a pg_dump archive's table of contents.
type TOCEntry struct {
	// Line is the entry exactly as pg_restore printed it, which is what
	// pg_restore -L reads back.
	Line string
	// Desc is the entry's kind: "TABLE DATA", "SEQUENCE SET", "INDEX", …
	Desc string
	// Schema and Name identify what the entry is about.
	Schema string
	Name   string
}

// Qualified is the entry's schema-qualified subject.
func (t TOCEntry) Qualified() string { return t.Schema + "." + t.Name }

// tocLine matches a data entry. The general form is
//
//	dumpId; catalogOid oid DESC schema name owner
//
// and DESC may contain spaces, so only the kinds pgctl selects on are matched
// rather than attempting to parse every entry pg_dump can emit.
var tocLine = regexp.MustCompile(`^(\d+);\s+\d+\s+\d+\s+(TABLE DATA|SEQUENCE SET)\s+(\S+)\s+(\S+)`)

// ReadTOC lists an archive's contents.
//
// Driving pg_restore by table of contents rather than by `--table`: pg_restore's
// table patterns are not schema-qualified, so restoring claims.policy_claim and
// operations.policy_claim by name is not expressible, and a selection that
// silently matches the wrong schema is the kind of mistake this tool exists to
// make impossible. A TOC entry names both.
func ReadTOC(ctx context.Context, archive string) ([]TOCEntry, error) {
	out, err := exec.CommandContext(ctx, "pg_restore", "--list", archive).Output()
	if err != nil {
		return nil, fmt.Errorf("pg_restore --list %s: %w", archive, err)
	}

	var entries []TOCEntry
	scan := bufio.NewScanner(bytes.NewReader(out))
	scan.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scan.Scan() {
		line := scan.Text()
		m := tocLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		entries = append(entries, TOCEntry{Line: line, Desc: m[2], Schema: m[3], Name: m[4]})
	}
	return entries, scan.Err()
}

// WriteRestoreList writes a pg_restore -L list holding the given entries, and
// returns its path.
func WriteRestoreList(dir string, entries []TOCEntry) (string, error) {
	var b strings.Builder
	b.WriteString(";\n; pgctl: the archive entries selected for this apply\n;\n")
	for _, e := range entries {
		b.WriteString(e.Line)
		b.WriteString("\n")
	}
	path := filepath.Join(dir, "pgctl-restore.list")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return "", fmt.Errorf("write restore list: %w", err)
	}
	return path, nil
}

// SelectData picks the TABLE DATA entries for the given tables, plus the
// SEQUENCE SET entries for the sequences those tables own.
//
// The sequences matter: loading rows without their sequence leaves the target's
// counter behind the data, and the next insert collides on a primary key. That
// failure surfaces in the application, hours later, as a mystery.
func SelectData(entries []TOCEntry, tables, sequences []string) (selected []TOCEntry, missing []string) {
	want := set(tables)
	wantSeq := set(sequences)
	found := map[string]bool{}

	for _, e := range entries {
		switch e.Desc {
		case "TABLE DATA":
			if want[e.Qualified()] {
				selected = append(selected, e)
				found[e.Qualified()] = true
			}
		case "SEQUENCE SET":
			if wantSeq[e.Qualified()] {
				selected = append(selected, e)
			}
		}
	}

	// A table with no TABLE DATA entry is one pg_dump was told to skip, which
	// for pgctl means a rule excluded its data. The caller decides whether that
	// is expected; it must not silently load nothing.
	for _, t := range tables {
		if !found[t] {
			missing = append(missing, t)
		}
	}
	return selected, missing
}

func set(items []string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, i := range items {
		m[i] = true
	}
	return m
}
