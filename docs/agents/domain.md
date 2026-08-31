# Domain Docs

How the engineering skills should consume this repo's domain documentation when exploring the codebase.

## Before exploring, read these

- **`CONTEXT.md`** at the repo root, or
- **`CONTEXT-MAP.md`** at the repo root if it exists — it points at one `CONTEXT.md` per context. Read each one relevant to the topic.
- **`design/decisions.md`** — this repo's decision record. Read the entries that
  touch the area you're about to work in.

  **It is one numbered log, not a file per ADR, and there is no `docs/adr/`.**
  Where a skill says "read the ADRs", read this. Where a skill says "write an
  ADR", append a numbered entry here. The form is deliberate: these decisions
  refer to each other constantly, and a running log is read start-to-finish in a
  way a directory of files is not.

- **`design/maintenance.md`** and **`design/dba-surface.md`** — notebooks rather
  than decisions: ideas captured with what was measured against them, and marked
  where nothing is built. Read them before proposing work in those areas, so a
  proposal that was already considered and set aside says why it is coming back.

If any of these files don't exist, **proceed silently**. Don't flag their absence; don't suggest creating them upfront. The `/domain-modeling` skill (reached via `/grill-with-docs` and `/improve-codebase-architecture`) creates them lazily when terms or decisions actually get resolved.

## File structure

This repo, single-context:

```
/
├── CONTEXT.md          ← does not exist yet; created lazily, see above
├── design/
│   ├── decisions.md    ← the decision record, one numbered log
│   ├── maintenance.md  ← notebook
│   └── dba-surface.md  ← notebook
├── cmd/pgctl/
└── internal/
```

pgctl is a single Go module with no packages published separately, so the
multi-context layout (a root `CONTEXT-MAP.md` pointing at per-context glossaries)
does not apply. If that changes, this file changes with it.

## Use the glossary's vocabulary

When your output names a domain concept (in an issue title, a refactor proposal, a hypothesis, a test name), use the term as defined in `CONTEXT.md`. Don't drift to synonyms the glossary explicitly avoids.

If the concept you need isn't in the glossary yet, that's a signal — either you're inventing language the project doesn't use (reconsider) or there's a real gap (note it for `/domain-modeling`).

## Flag conflicts with a recorded decision

If your output contradicts an entry in `design/decisions.md`, surface it
explicitly rather than silently overriding it:

> _Contradicts decision 17 (pgctl reports; it does not sample) — but worth
> reopening because…_

This matters more here than the usual amount, because several entries were
written specifically to be read by whoever later proposes the thing they argue
against, and each records what would have to change to reverse it. Reversing one
is allowed. Reversing one without noticing is the failure.
