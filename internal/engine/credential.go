package engine

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Where a storage credential comes from, and what pgctl is careful about.
//
// Two routes, per remote. An environment variable, which is the default and
// the one CI uses; or a command that prints the value, for a team whose secrets
// live in an encrypted file. `sops exec-env envs/prd.env pgctl …` needs neither
// and is a perfectly good answer — the reason a command exists beside it is
// that exec-env decrypts the whole file into the environment, and pgctl hands
// its environment to pg_dump, pg_restore and every configured hook. Measured
// against the file this was built for: 239 variables reaching every subprocess
// in order to deliver one key.
//
// Three rules hold, and they are the whole of why this file is not four lines:
//
//   - the value never reaches an error message. A command that fails reports its
//     STDERR and its exit status; its stdout is the secret and is dropped, and
//     so is the command text, which may itself contain the credential.
//   - the value never reaches a subprocess. It is passed to the blob client and
//     nowhere else — no exported variable, no argv.
//   - the command runs once per process. Several remotes can name the same
//     file, a listing opens every remote, and each run of sops is a decryption
//     and possibly a prompt.

// credentialTimeout bounds a credential command.
//
// Generous, because the command may be doing real work: sops with an age key is
// milliseconds, and one backed by a hardware token waits for a person to touch
// it. Bounded all the same — a credential helper that blocks forever turns
// `pgctl ls` into a program that hangs with no output, which is the failure
// mode libpq's password prompt taught this repo to be afraid of.
const credentialTimeout = 60 * time.Second

// credentials caches what the commands printed, keyed by the command itself.
type credentials struct {
	mu   sync.Mutex
	seen map[string]string
}

// credential resolves one credential: the command if there is one, otherwise the
// environment variable.
//
// A command WINS over a set variable, deliberately. Naming a command is a
// statement about where the credential lives; an environment variable that
// happens to be set is often an accident of the shell — and silently preferring
// the accident is how a snapshot ends up in the wrong account.
func (e *Engine) credential(ctx context.Context, what, command, env string) (string, error) {
	if command == "" {
		value := os.Getenv(env)
		if value == "" {
			return "", fmt.Errorf("no %s: set %s, or give the remote a %sCommand",
				what, env, what)
		}
		return value, nil
	}

	e.creds.mu.Lock()
	defer e.creds.mu.Unlock()
	if value, ok := e.creds.seen[command]; ok {
		return value, nil
	}

	value, err := runCredentialCommand(ctx, command, e.root)
	if err != nil {
		return "", fmt.Errorf("reading the %s: %w", what, err)
	}
	if value == "" {
		// An empty answer is a failure, and a quiet one: sops prints nothing to
		// stdout when its extract path matches no key, exits zero, and the
		// snapshot then fails much later against the storage account with an
		// authentication error naming nothing.
		return "", fmt.Errorf("reading the %s: the command printed nothing", what)
	}
	if e.creds.seen == nil {
		e.creds.seen = map[string]string{}
	}
	e.creds.seen[command] = value
	return value, nil
}

// runCredentialCommand runs one command and returns its stdout.
//
// Through a shell, because the useful commands are quoted extract expressions
// that are unreadable as an argv list — and because the alternative is a config
// format that has to explain its own tokenisation. It is a command from the
// project's own committed config, in the same file that already declares the
// hooks pgctl runs around an apply.
//
// dir is the directory the config was loaded from, so a command may name a file
// relative to the config that mentions it — `envs/prd.env` means the same thing
// wherever pgctl is run from. Getting this wrong makes a config work for
// whoever wrote it and fail for everybody else.
func runCredentialCommand(ctx context.Context, command, dir string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, credentialTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = dir
	// Stdin is closed rather than inherited: a helper that decides to prompt
	// should fail, not silently consume the keystrokes of a terminal UI that is
	// mid-frame.
	cmd.Stdin = nil

	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		// STDERR only, and NOT the command itself. Stdout is the secret, and an
		// error that quoted it would put a storage key in a log, a ticket or a
		// screenshot — which is exactly what redact() exists to prevent one
		// level down.
		//
		// The command is left out for the same reason, which is less obvious
		// and was found by the test for the first rule: `keyCommand: printf
		// hunter2` is a config somebody writes while trying this out, and the
		// secret is then IN the command. The caller names the remote and which
		// credential it was reading, which identifies the command without
		// repeating it — and the command is in their config, where they can
		// read it themselves.
		if why := strings.TrimSpace(errb.String()); why != "" {
			return "", fmt.Errorf("%w: %s", err, lastLines(why, 3))
		}
		return "", err
	}
	// Trailing newline trimmed, because every one of these commands prints one
	// and a key with a newline on the end fails authentication for a reason
	// nobody can see.
	return strings.TrimSpace(out.String()), nil
}
