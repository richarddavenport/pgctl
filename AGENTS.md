# AGENTS.md

Canonical entry point for AI agents working on pgctl. Vendor adapters
(`CLAUDE.md`) point back here; they are not the source of truth.

pgctl moves PostgreSQL data between environments: nightly snapshots,
whole-database refreshes, and migrations of individual tables or table sets that
respect the foreign keys between them.

## Required reading

- [`README.md`](./README.md) — what the tool does and how it is configured.
- [`design/decisions.md`](./design/decisions.md) — **read before changing how
  anything behaves.** Seventeen numbered decisions, each with the reasoning that
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

## Working principles

- **PostgreSQL's own mechanisms first.** A connection is a libpq DSN; credentials
  come from `~/.pgpass` and `~/.pg_service.conf`; databases are discovered from
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
