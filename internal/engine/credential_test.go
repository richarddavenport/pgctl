package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/richarddavenport/pgctl/internal/config"
)

// credEngine is an engine rooted in a directory, which is what a credential
// command's relative paths resolve against.
//
// The root comes from cfg.Source — the path the config was loaded FROM — so the
// fixture has to set it, exactly as loading a file does. Leaving it empty roots
// the engine at "." and the command then runs in the test binary's directory,
// which is where this test first went wrong.
func credEngine(t *testing.T, dir string) *Engine {
	t.Helper()
	cfg, err := config.Parse(nil, dir)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	cfg.Source = filepath.Join(dir, "pgctl.yaml")
	return New(cfg)
}

// A command's output is the credential, and a trailing newline is not part of
// it.
//
// Every one of these commands prints a newline, and a storage key with one on
// the end fails authentication for a reason nobody can see from the error.
func TestACredentialCommandsOutputIsTrimmed(t *testing.T) {
	e := credEngine(t, t.TempDir())
	got, err := e.credential(context.Background(), "key", "printf 'sekrit\\n'", "")
	if err != nil {
		t.Fatalf("credential: %v", err)
	}
	if got != "sekrit" {
		t.Errorf("credential = %q, want it trimmed", got)
	}
}

// A command runs relative to the config's directory.
//
// `keyCommand: sops -d … envs/prd.env` has to mean the same thing wherever
// pgctl is run from, or the config works for whoever wrote it and fails for
// everybody else.
func TestACredentialCommandRunsBesideTheConfig(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("from-the-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	e := credEngine(t, dir)
	got, err := e.credential(context.Background(), "key", "cat secret.txt", "")
	if err != nil {
		t.Fatalf("credential: %v", err)
	}
	if got != "from-the-file" {
		t.Errorf("credential = %q; the command did not run beside the config", got)
	}
}

// A command WINS over a set environment variable.
//
// Naming a command is a statement about where the credential lives; a variable
// that happens to be set is often an accident of the shell, and silently
// preferring the accident is how a snapshot ends up in the wrong account.
func TestACommandBeatsAnEnvironmentVariable(t *testing.T) {
	t.Setenv("PGCTL_TEST_KEY", "from-the-environment")
	e := credEngine(t, t.TempDir())

	got, err := e.credential(context.Background(), "key", "printf from-the-command", "PGCTL_TEST_KEY")
	if err != nil {
		t.Fatalf("credential: %v", err)
	}
	if got != "from-the-command" {
		t.Errorf("credential = %q, want the command's answer", got)
	}

	// And with no command, the variable is used.
	if got, err = e.credential(context.Background(), "key", "", "PGCTL_TEST_KEY"); err != nil {
		t.Fatalf("credential: %v", err)
	}
	if got != "from-the-environment" {
		t.Errorf("credential = %q, want the variable", got)
	}
}

// The command runs ONCE, however many times a credential is asked for.
//
// Several remotes can name one file, and a listing opens every remote — each
// run of sops is a decryption and possibly a prompt for a hardware token.
func TestACredentialCommandRunsOncePerProcess(t *testing.T) {
	dir := t.TempDir()
	counter := filepath.Join(dir, "runs")
	e := credEngine(t, dir)
	command := "printf x >> " + counter + "; printf the-key"

	for i := 0; i < 3; i++ {
		got, err := e.credential(context.Background(), "key", command, "")
		if err != nil {
			t.Fatalf("credential: %v", err)
		}
		if got != "the-key" {
			t.Fatalf("credential = %q", got)
		}
	}

	runs, err := os.ReadFile(counter)
	if err != nil {
		t.Fatalf("read the counter: %v", err)
	}
	if len(runs) != 1 {
		t.Errorf("the command ran %d times, want once", len(runs))
	}
}

// A failing command reports its STDERR and never its stdout.
//
// Stdout is the secret. An error that quoted it would put a storage key in a
// log, a ticket or a screenshot — which is the whole thing redact() exists to
// prevent one level down, and this is the same hazard one level up.
func TestAFailingCredentialCommandNeverQuotesItsOutput(t *testing.T) {
	e := credEngine(t, t.TempDir())
	_, err := e.credential(context.Background(), "key",
		"printf 'THE-SECRET'; printf 'no matching creation rule\\n' >&2; exit 3", "")
	if err == nil {
		t.Fatal("a failing command produced a credential")
	}
	if strings.Contains(err.Error(), "THE-SECRET") {
		t.Errorf("the error quotes the secret: %v", err)
	}
	// And it says enough to fix: what ran, that it failed, and why.
	for _, want := range []string{"key", "no matching creation rule", "exit status 3"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not mention %q: %v", want, err)
		}
	}
}

// A command that succeeds and prints nothing is a failure, and says so here.
//
// sops prints nothing to stdout when its extract path matches no key, and exits
// zero. Without this the snapshot fails minutes later against the storage
// account, with an authentication error that names nothing.
func TestASilentCredentialCommandIsAnError(t *testing.T) {
	e := credEngine(t, t.TempDir())
	_, err := e.credential(context.Background(), "account", "true", "")
	if err == nil {
		t.Fatal("an empty credential was accepted")
	}
	if !strings.Contains(err.Error(), "printed nothing") {
		t.Errorf("err = %v, want it to name the empty output", err)
	}
}

// With neither route configured the error names both of them.
func TestAMissingCredentialNamesBothRoutes(t *testing.T) {
	e := credEngine(t, t.TempDir())
	_, err := e.credential(context.Background(), "key", "", "PGCTL_TEST_UNSET_KEY")
	if err == nil {
		t.Fatal("a missing credential was accepted")
	}
	for _, want := range []string{"PGCTL_TEST_UNSET_KEY", "keyCommand"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not offer %q: %v", want, err)
		}
	}
}
