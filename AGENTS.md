# AGENTS.md

Canonical entry point for AI agents working on pgctl. Vendor adapters
(`CLAUDE.md`) point back here; they are not the source of truth.

pgctl moves PostgreSQL data between environments: nightly snapshots,
whole-database refreshes, and migrations of individual tables or table sets that
respect the foreign keys between them.

## Required reading

- [`README.md`](./README.md) — what the tool does and how it is configured.
- [`design/decisions.md`](./design/decisions.md) — **read before changing how
  anything behaves.** Nineteen numbered decisions, each with the reasoning that
  produced it. A change that contradicts one is not forbidden, but it has to say
  so rather than quietly reverse it.

## Deeper context

Read these when the work touches the area:

- [`design/maintenance.md`](./design/maintenance.md) — PostgreSQL maintenance:
  what pgctl already does after a restore, what it could report, what it should
  stay out of. A notebook, not a plan.
- [`design/dba-surface.md`](./design/dba-surface.md) — the enumeration of
  database administration work, fifteen domains, grounded in measurements.
  Nothing there is built except where it says so.
- [`docs/nightly.md`](./docs/nightly.md) — the scheduled snapshot: what it needs
  and where it should run.

## Building it

pgctl's interface is built on [tuikit](https://github.com/richarddavenport/tuikit)
— decision 18. tuikit is private and untagged, so `go.mod` resolves it through
`replace github.com/richarddavenport/tuikit => ../tuikit`: **you need a tuikit
checkout beside this one**, or nothing builds. CI checks out both.

Three things will fail your change that did not exist before:

- `guard.Engine` in `internal/{engine,pg,config,store,snapshot}` — those
  packages may not import a colour, a width, a key or a UI framework.
- `guard.Tokens`, `guard.Glyphs` and `guard.Chrome` in `internal/tui` — a colour
  that is not a role in `theme.go`, or a non-ASCII character not in its glyph
  set, is a test failure.
- the goldens in `internal/tui/testdata` — 19 states at two terminal sizes, 38 frames. A
  layout change is an ordinary test failure; run
  `go test ./internal/tui -update-goldens` when it is intended, and **read the
  diff**. `TestEveryFrameFitsItsTerminal` is the one that stops a frame drawing
  off the side of the screen.

To look at a screen rather than assert on it:

```sh
PGCTL_FRAMES=/tmp/pgctl-frames go test ./internal/tui -run CaptureFrames
tuikit frames /tmp/pgctl-frames -out /tmp/frames.html -title pgctl
```

## When comp cannot do something

You will hit this. The rule is **not** to work around it quietly.

If you are about to write `c.Text`, `c.Fill`, `c.Set`, or arithmetic on a Rect,
stop: the canvas exists so a tool never does that. Work around it if you must —
you have a thing to ship — but read `../tuikit/AGENTS.md` § "When comp cannot do
something" and file the report **in the same commit**, or it will not be
reported:

```sh
gh issue create -R richarddavenport/tuikit --template from-a-tool.md
```

Include **the code you wrote by hand**, not a description of it. A component
gets built when two tools have hand-rolled the same thing, and "the same" is a
judgement nobody can make from prose. A guess at the API is welcome, and a wrong
guess is useful.

## Working principles

- **PostgreSQL's own mechanisms first.** A connection is a libpq DSN; credentials
  come from `~/.pgpass` and a libpq service file (which pgctl keeps at
  `~/.config/pgctl/pg_service.conf` via `PGSERVICEFILE` — decision 8b);
  databases are discovered from
  the server. Every time pgctl has invented a parallel scheme for something
  PostgreSQL already has, it has been deleted again — see decisions 8 and 8a.
- **Refusals are the engine's, not the front end's.** The TUI and the CLI render
  the same decision. A safety rule that lives in a view is a safety rule the
  other front end does not have.
- **Comments carry the reasoning, not the mechanics.** The code says what it
  does. A comment is for why it does it that way and what happens if it does
  not — several in here name the specific failure that motivated them.
- **Verify against a real database.** Most of the important bugs in this tool
  were found by running it, not by reading it: a hang inside libpq's GSSAPI
  negotiation, a 33-rows-per-second load, an archive that could not be restored.
  There are probe tests (`*_probe_test.go`) for exactly this.

## Agent skills

Per-repo configuration for the installed engineering skills (`to-issues`,
`to-prd`, `triage`, `qa`, `to-spec`, `wayfinder`).

### Issue tracker

GitHub Issues on `richarddavenport/pgctl`, via the `gh` CLI. See
[`docs/agents/issue-tracker.md`](./docs/agents/issue-tracker.md).

### Triage labels

The five canonical roles (`needs-triage`, `needs-info`, `ready-for-agent`,
`ready-for-human`, `wontfix`), each label string equal to its name. See
[`docs/agents/triage-labels.md`](./docs/agents/triage-labels.md).

### Domain docs

Single-context. Decisions live as one numbered log in
[`design/decisions.md`](./design/decisions.md) rather than as a file per ADR —
there is no `docs/adr/`. See
[`docs/agents/domain.md`](./docs/agents/domain.md).
