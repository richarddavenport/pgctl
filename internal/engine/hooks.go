package engine

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/richarddavenport/pgctl/internal/config"
)

// runHooks executes a hook phase. fatal decides whether a failure stops the
// operation: a preApply failure does, a postApply failure does not, because by
// then the data has landed and abandoning the run leaves the target worse.
func (e *Engine) runHooks(ctx context.Context, phase string, hooks []config.Hook,
	env map[string]string, fatal bool, report Reporter) error {

	for _, h := range hooks {
		name := h.Name
		if name == "" {
			name = h.Run
		}
		report.step(phase, name)

		timeout := h.Timeout
		if timeout == 0 {
			timeout = 10 * time.Minute
		}
		hookCtx, cancel := context.WithTimeout(ctx, timeout)
		err := runShell(hookCtx, h.Run, env)
		cancel()

		if err == nil {
			continue
		}
		if fatal {
			return fmt.Errorf("%s hook %q: %w", phase, name, err)
		}
		report.warn(fmt.Sprintf("%s hook %q failed: %v", phase, name, err))
	}
	return nil
}

// runShell runs a hook through the shell, so that a project can write a
// pipeline or a compound command in one config line.
func runShell(ctx context.Context, script string, env map[string]string) error {
	cmd := exec.CommandContext(ctx, "sh", "-c", script)
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var errb bytes.Buffer
	cmd.Stderr = &errb
	cmd.Stdout = &errb
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, lastLines(errb.String(), 5))
	}
	return nil
}

// hookEnv is what a hook is told about the operation around it. Nothing secret
// goes in: a hook that needs credentials should decrypt them the same way pgctl
// does, rather than receive them through an environment a `ps` can read.
func hookEnv(connection, database, snapshotID string) map[string]string {
	return map[string]string{
		"PGCTL_CONNECTION": connection,
		"PGCTL_DATABASE":   database,
		"PGCTL_SNAPSHOT":   snapshotID,
	}
}
