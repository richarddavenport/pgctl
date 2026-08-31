# CLAUDE.md

Claude Code adapter. **The canonical agent context for this repo is
[`AGENTS.md`](./AGENTS.md)** — this file does not duplicate it, it exists so that
Claude Code loads it (Claude reads `CLAUDE.md` by default; the rest of the
tooling reads `AGENTS.md`). Keep real context in `AGENTS.md`, not here.

@AGENTS.md

## Claude Code specifics

- `make install` builds the working tree over the `pgctl` on your PATH. Do that
  rather than reasoning about whether a change took effect.
- `make check` is what to run before pushing: tests, `gofmt`, `go vet`, and
  `golangci-lint`. The lint config is strict and CI enforces it.
- Tests that need a database skip unless given one. `make lab-up` starts a
  disposable PostgreSQL and `make test-all` runs everything against it.
- The `*_probe_test.go` files are not assertions but instruments — they render
  frames or print measurements for a person to look at. `render_probe_test.go`
  and `screenshot_probe_test.go` found three layout bugs that assertions had not.
