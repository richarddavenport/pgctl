// Package engine turns a config and an operator's intent into snapshots and
// applies. It owns every subprocess (sops, pg_dump, pg_restore) and every
// connection.
package engine

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// Secrets is one environment's decrypted secret set.
//
// Values are held in memory for the length of an operation and never written
// anywhere: not to a log line, not into a subprocess's argv (which is world
// readable on Linux), not into an error message. Credentials reach pg_dump
// through its environment and through PGPASSWORD, and reach pgx through a
// connection struct.
type Secrets map[string]string

// LoadSecrets decrypts an environment's sops-encrypted dotenv.
//
// Shelling out to sops rather than linking it: sops is what a person runs by
// hand, what swarmctl runs, and what CI runs, so a value that pgctl cannot read
// is a problem with the operator's age key rather than with pgctl's idea of
// sops. The repo root is where the paths in the config resolve against.
func LoadSecrets(ctx context.Context, root, file string) (Secrets, error) {
	if file == "" {
		return Secrets{}, nil
	}
	path := file
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, file)
	}

	cmd := exec.CommandContext(ctx, "sops", "decrypt", path)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("sops decrypt %s: %s", file, msg)
	}
	return ParseDotenv(out.Bytes()), nil
}

// ParseDotenv reads KEY=value lines. Comments, blanks and `export` prefixes are
// tolerated, and a value may be quoted.
//
// Deliberately not a full dotenv implementation: these files are consumed by
// docker compose's env_file, which is itself this simple, so anything fancier
// would be reading a file differently from the thing that runs in production.
func ParseDotenv(data []byte) Secrets {
	out := Secrets{}
	scan := bufio.NewScanner(bytes.NewReader(data))
	scan.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scan.Scan() {
		line := strings.TrimSpace(scan.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') ||
				(value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}
		out[key] = value
	}
	return out
}

// Get returns a value, and whether it was present and non-empty. An empty
// value in a secrets file means the same thing as a missing one — something
// went wrong upstream — and treating them alike stops pgctl connecting as an
// empty user to an empty host.
func (s Secrets) Get(key string) (string, bool) {
	v, ok := s[key]
	if !ok || v == "" {
		return "", false
	}
	return v, true
}
