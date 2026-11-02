# Contributing to Verdict

Verdict is a single Go binary (`go.mod`, no third-party runtime
dependencies) plus a TypeScript client in `clients/ts`, npm packaging
in `scripts/`, and GitHub workflows (`ci.yml`, `release.yml`,
`deploy-docs.yml`, `setup-verdict.yml`).

## Development setup

Prerequisites: Go (see `go.mod`), `gh` (authed, `repo` scope),
`jq` (>= 1.7), `shasum`, `git`, and `bun` (frozen-lockfile installs
for the root and `apps/docs`, same as CI's `.github/actions/setup`).

```bash
bun install --frozen-lockfile
(cd apps/docs && bun install --frozen-lockfile)
make build          # writes bin/verdict, the CLI under test
```

Run `verdict doctor` to preflight the harness.

## The merge bar

Every PR must clear all of these; CI enforces each one:

1. **Evals green.** `make evals` builds `bin/verdict` and runs the
   full hermetic suite against it. While iterating, run only the
   affected cases (`./evals/one.sh <case>`).
2. **Go green.** `go test -race ./...` (CI also gates `go vet ./...`,
   `go mod verify`, `gofmt -l .` empty, and `govulncheck` via
   `make ci-go`).
3. **Linter-clean.** `golangci-lint run ./...` with the repo's
   `.golangci.yml` (CI runs the pinned action; zero issues, no new
   `//nolint`).
4. **Formatted.** `bun fmt --check .` before pushing; fix only what it
   flags, and never reflow `prompts/` examples.
5. **A changeset when the repo's practice requires one.** Add
   `.changeset/<name>.md` (`patch`/`minor`/`major` + user-facing
   notes) for any user-facing change — `patch` by default, `minor`
   for new verbs or flags, `major` for envelope/store breaks.
   Docs-only, eval-only, and comment-only merges ship none.

## Golden tests

Golden files under `testdata/` are the contract: if your change
alters golden-covered output, regenerate with `go test ./... -update`
and **review the diff** before committing. A regenerated golden you
cannot explain line-by-line is a red flag, not a green gate. Never
commit a golden update alongside unrelated changes.

## Commit style

One focused commit per change: `<area>: <imperative summary>` (≤72
chars), blank line, 1–3 lines saying what changed and why. Areas
match the tree (`core`, `mcp`, `pkg`, `client`, `skill`, `upgrade`,
`action`, `ci`, `docs`, `evals`). No trailers, no merge commits on
feature branches, never touch `main` directly.

## Issues and pull requests

Use the issue templates (bug reports name the verb and paste the
`--json` envelope; features name the surface they touch) and fill in
the PR template checklist. Verbs are: `fetch`, `post`, `triage`,
`report`, `next`, `show`, `verify`, `doctor`, `calibrate`,
`fetch-local`, `record`, `adopt`, `fix`, `check-tree`, `ready`,
`trend`, `learn`, `init`, `install`, `skill`, `upgrade`, `uninstall`
— plus the hidden `mcp` stdio transport. If your change adds or
renames one, the templates, the manual, and `cmd/verdict/usage.go`
must agree.

Security bugs follow `SECURITY.md`, never the public templates.
