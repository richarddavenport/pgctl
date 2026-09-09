# Changelog

Every released version, newest first, in the words of somebody deciding whether
to install it. `scripts/changelog.sh` reads the section for a tag and the
release refuses to publish without one — a release whose notes are generated
commit titles is a release nobody reads.

The format is [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the
versions are [semantic](https://semver.org/spec/v2.0.0.html). While the major
is 0, a minor may change behaviour: what that means in practice is written under
each entry rather than assumed.

## [Unreleased]

## [0.2.0]

pgctl can now tell you it is out of date, and fix that itself.

### Added

- **The version is in the bottom right of every screen**, with `→ v0.3.0`
  beside it when a newer release exists and `new` if it appeared while the
  session was open. The corner is where you already look to answer "what am I
  running", so "is that current" is answered in the same place.
- **`U` installs it**, from a screen that says what will happen first. It does
  not restart anything: replacing a running binary is safe, but the running code
  is still the old code, so the screen tells you to quit and start again rather
  than implying the session was upgraded. It is in `ctrl+p` too, in its own
  group — every other letter on that screen acts on your databases and this one
  acts on the tool.
- **`pgctl update`** does the same thing headlessly, through the same code, so
  both refuse in the same places. `--force` is needed to replace a local build
  with a release, because a build from `make install` is usually newer than the
  last release and replacing it silently throws away what you were working on.
- **Homebrew**: `brew install richarddavenport/tap/pgctl`. The formula is
  generated from the published release, so `brew upgrade` and `pgctl update` get
  the identical binary. Updating a brew-installed pgctl in place works but
  leaves brew's records stale until the next `brew upgrade`; pgctl says so and
  then does what you asked.
- **This file.** A release now refuses to publish without a section for its tag,
  so release notes describe what changed rather than listing commit titles.

### Changed

- `install.sh` needs only `curl` — it fetches the release directly and falls
  back to the `gh` CLI only for the one case that requires it. Verified against
  a real release rather than reasoned about.
- Every update is checked against the release's `checksums.txt` before anything
  is replaced, and a mismatch leaves the existing binary untouched. The
  replacement is written beside the target so the final rename is atomic: there
  is no moment at which half a pgctl is on your PATH.
- The update check needs no GitHub credential and fails silently. It is a
  courtesy notice; an error about it would be noise in front of your actual work.

### Note for anyone on 0.1.0

0.1.0 has no `update` command, so this one release has to be installed the way
you installed that one — `install.sh`, or `brew install`. From 0.2.0 onward,
`pgctl update` and `U` are enough.

## [0.1.0]

The first release. pgctl moves PostgreSQL data between environments: nightly
snapshots, whole-database refreshes, and migrations of individual tables or
table sets that respect the foreign keys between them.

### What it does

- **Snapshots are catalogued artifacts.** One press of `n` — or one `pgctl
  snapshot` — is a **run**: every database of a connection at one instant,
  addressed as `prd/20260909T153059Z`. Each carries a manifest recording where
  it came from, when, the server version, and every table's fate.
- **A snapshot is also a per-table backup.** Dumps are `pg_dump` directory
  format, one file per table, so a table can be cherry-picked out of last
  night's full snapshot. There is no separate per-table backup to remember.
- **A table selection is refused unless it is referentially closed.** pgctl
  reads the target's foreign keys, computes the closure of what you asked for,
  and says what is missing rather than letting the load fail halfway. `--widen`
  accepts the closure.
- **Load order comes from the graph.** The selection is condensed into strongly
  connected components and ordered parents-first, so a schema with foreign-key
  rings still gets a complete plan.
- **Tables too large to move whole are filtered.** `pg_dump` has no row
  predicate, so a table with a `where:` rule is produced by binary `COPY`
  alongside the archive and recorded in the same manifest.
- **Storage is a list of named destinations** — a local directory plus any
  number of Azure Blob remotes, each with its own retention policy. There is no
  default: `--to-storage` is required once a remote is declared.
- **Two front ends over one engine.** A terminal interface built on
  [tuikit](https://github.com/richarddavenport/tuikit), and `pgctl ls / plan /
  apply / move / prune` for scripts. Both render the same refusals, because a
  safety rule that lives in a view is one the other front end does not have.

### Safety

- `protected` connections are never an apply target, with no override.
- `guarded` connections need their name typed in full.
- An apply plans first, and shows the plan before touching anything.
- A snapshot that did not finish cannot be applied, and one missing a table the
  target has is refused rather than loaded partially.

### Installing

```sh
curl -fsSL https://raw.githubusercontent.com/richarddavenport/pgctl/master/install.sh | bash
```

Needs PostgreSQL 16 or newer, server and client tools both — below 16 there is
no `zstd` compression, and pgctl's speed starts there. Nothing else: connections
are libpq DSNs and credentials come from `~/.pgpass` or `~/.pg_service.conf`.

Once installed, `pgctl update` fetches the next release and `U` in the
interface does the same thing.

[Unreleased]: https://github.com/richarddavenport/pgctl/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/richarddavenport/pgctl/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/richarddavenport/pgctl/releases/tag/v0.1.0
