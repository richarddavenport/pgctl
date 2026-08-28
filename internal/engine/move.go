package engine

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/richarddavenport/pgctl/internal/snapshot"
)

// MoveRequest is one environment-to-environment refresh.
type MoveRequest struct {
	From string
	To   string

	Database string
	Set      string
	Tables   []string
	Widen    bool

	// Keep catalogues the snapshot afterwards instead of deleting it, for when
	// a refresh turns out to be worth repeating.
	Keep bool
}

// Move refreshes one environment from another without leaving a snapshot
// behind.
//
// Deliberately not a `pg_dump | pg_restore` pipe. A pipe needs custom format,
// which cannot be dumped in parallel and, streamed, cannot be restored in
// parallel either — so it trades away the largest speed win to avoid touching a
// disk. This takes a parallel snapshot into a temporary directory, applies it in
// parallel, and deletes it. See design/decisions.md #14.
func (e *Engine) Move(ctx context.Context, req MoveRequest, report Reporter) error {
	if req.From == req.To {
		return &RefusalError{fmt.Sprintf("%s is both the source and the target", req.From)}
	}
	target, err := Resolve(ctx, e.cfg, e.root, req.To, req.Database)
	if err != nil {
		return err
	}
	if target.Env.Protected {
		return &RefusalError{fmt.Sprintf(
			"%s is a protected environment and can never be an apply target", target.Env.Name)}
	}

	// The staging directory is named after the snapshot it holds, under a
	// temporary root, so a failed move leaves something identifiable rather
	// than an anonymous directory of gigabytes.
	staging, err := os.MkdirTemp("", "pgctl-move-")
	if err != nil {
		return fmt.Errorf("create staging directory: %w", err)
	}
	at := time.Now()
	id := snapshot.NewID(req.From, req.Database, at)
	dir := snapshot.Path(staging, id)

	keep := req.Keep
	defer func() {
		if keep {
			report.warn(fmt.Sprintf("snapshot kept at %s", dir))
			return
		}
		if err := os.RemoveAll(staging); err != nil {
			report.warn(fmt.Sprintf("could not remove the staging directory %s: %v", staging, err))
		}
	}()

	report.step("move", fmt.Sprintf("%s → %s, staging in %s", req.From, req.To, staging))

	if _, err := e.Dump(ctx, DumpRequest{
		Environment: req.From,
		Database:    req.Database,
		Dir:         dir,
		At:          at,
		// Nothing is uploaded: this snapshot exists for the next few minutes.
		NoPush: true,
	}, report); err != nil {
		return err
	}

	plan, err := e.planFrom(ctx, dir, ApplyRequest{
		Snapshot: id,
		Target:   req.To,
		Set:      req.Set,
		Tables:   req.Tables,
		Widen:    req.Widen,
	}, report)
	if err != nil {
		return err
	}
	fmt.Fprint(os.Stderr, plan.Describe())

	if err := e.Execute(ctx, plan, report); err != nil {
		// A failed apply is the one case where the snapshot is worth keeping
		// without being asked: it took as long to produce as the retry would.
		keep = true
		return err
	}
	return nil
}
