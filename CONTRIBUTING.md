# Contributing to Verdict

Verdict is a single Go binary (`go.mod`, no third-party runtime
dependencies) plus a TypeScript client in `clients/ts`, npm packaging
in `scripts/`, and GitHub workflows (`ci.yml`, `release.yml`,
`deploy-docs.yml`, `setup-verdict.yml`).

## Development setup

Prerequisites: Go (see `go.mod`), `gh` (authed, `repo` scope), `git`,
and `bun` (frozen-lockfile installs for the root and `apps/docs`, same
as CI's `.github/actions/setup`). The eval harness also needs `jq`
(>= 1.7) and `shasum`; `install.sh` needs `shasum` or `sha256sum`.

```bash
bun install --frozen-lockfile
(cd apps/docs && bun install --frozen-lockfile)
make build          # writes bin/verdict, the CLI under test
make hooks          # wires hooks/pre-commit and hooks/pre-push into git
```

`hooks/pre-commit` gates staged formatting, shell, Go, workflow, and module
files — check-only, never rewrites. `hooks/pre-push` runs the Go bar CI runs
(`build`, `vet`, `test`, `make cross`, pinned `golangci-lint`) over the pushed
range and skips it for a docs-only range; `SKIP_PRE_PUSH=1 git push` is the
escape hatch.

Run `verdict doctor` to preflight the harness.

### Run your build as the skill

To use a checkout as your own skill install, symlink its `skills/verdict/`
directory (never the whole checkout). `git pull` then updates the skill, and
there is no updater daemon. `install-skill.sh` runs only
from a checkout and refuses anywhere else.

```bash
git clone https://github.com/stainless-code/verdict.git
cd verdict
make build
./install-skill.sh
```

`install-skill.sh` runs `doctor` first, wires `hooks/pre-commit`, and links
`skills/verdict` into the shared `~/.agents/skills` directory. An older link to
the whole checkout is migrated. It skips occupied slots, never overwrites
without `--force`, never touches a foreign `core.hooksPath`, and never re-links
an agent you removed.

```bash
./install-skill.sh --agent cursor            # link one agent
./install-skill.sh --dir ~/.foo/skills       # link any other agent's skills dir
./install-skill.sh --project ~/work/repo     # link into a repo's .agents/skills
./install-skill.sh --detect                  # standard dir plus installed agents
./install-skill.sh --all                     # every known agent
./install-skill.sh --list                    # link status, no changes
./install-skill.sh --check                   # doctor + verify links
./install-skill.sh --force --agent cursor    # replace an occupied slot
./install-skill.sh --uninstall               # remove our links (foreign files untouched)
./install-skill.sh --uninstall --agent cursor
```

`--all` links these locations (XDG and `CLAUDE_CONFIG_DIR` aware): `~/.agents`,
`~/.claude`, `~/.cursor`, `~/.config/opencode`, `~/.copilot`, `~/.gemini`,
`~/.config/goose`, `~/.qwen`, `~/.codeium/windsurf`. Re-run the script after a
rename in the tree. The binary installs its own embedded skill copy with
`verdict skill install`, which is the route for users who do not clone.

## Layout

```
verdict/
  skills/verdict/     # the skill payload, exactly what `verdict skill files` lists:
    SKILL.md          #   skill entry point (feed to any agent)
    prompts/          #   system, review, incremental, address, fix-ci, build, ci, grade, diagrams, subagent
    schemas/          #   JSON Schemas for the envelope and store files
    scripts/verdict   #   POSIX skill shim: VERDICT_BIN, bundled bin, PATH, then bootstrap
  install.sh          # verified binary installer (macOS/Linux release asset)
  install.ps1         # verified binary installer (Windows release asset)
  install-skill.sh    # doctor-gated symlink installer for checkouts
  CHANGELOG.md        # release notes (changesets); verdict --version reads package.json
  cmd/verdict/        # the CLI entry point (usage.go holds the verb synopsis)
  internal/           # verb packages, MCP server, installers, arch tests
  pkg/verdict/        # public Go SDK
  clients/ts/         # TypeScript client (shipped as @stainless-code/verdict/client)
  scripts/verdict-complete.sh  # bash/zsh completion for verdict
  setup-verdict/      # GitHub Action that installs a pinned, verified binary
  release/            # contract.json: frozen artifact names
  docs/manual.md      # full manual: store schema, verbs, doctrines, changelog
  docs/vision.md      # what it is and where its craft comes from
  apps/docs/          # Blume docs site (deploys to /verdict/)
  evals/              # hermetic eval suite (make evals)
  bin/verdict         # the built CLI (make build; gitignored)
```

## The merge bar

Every PR must clear all of these; CI enforces each one:

1. **Evals green.** `make evals` builds `bin/verdict` and runs the
   full hermetic suite against it. While iterating, run only the
   affected cases (`./evals/one.sh <case>`).
2. **Go green.** `make test-race` (`-race -shuffle=on -count=1`; CI also
   gates `go vet ./...`, `go mod verify`, `gofmt -l .` empty, and
   `govulncheck` via `make ci-go`). `make cross` builds and vets every
   `GOOS/GOARCH` pair `release/contract.json` ships, so a windows/arm64-only
   break fails here instead of in the release job. The nightly schedule also
   runs `make fuzz`; no pull request pays for it.
3. **Linter-clean.** `golangci-lint run ./...` with the repo's
   `.golangci.yml` (CI runs the pinned action; zero issues, no new
   `//nolint`).
4. **Formatted.** `bun fmt --check .` before pushing; fix only what it
   flags, and never reflow `skills/verdict/prompts/` examples.
5. **A changeset when the repo's practice requires one.** See
   [Versioning](#versioning).

A PR that touches only `apps/docs/`, `.changeset/`, `CHANGELOG.md`, or
`.github/ISSUE_TEMPLATE/` runs the format, docs-site, and audit lanes; the
fast path is decided from the diff against the PR base, and every other PR —
plus every push to `main` — runs the whole bar above. The macOS legs of the
Go, eval, and installer lanes are the exception, running only on pushes to
`main`, the nightly schedule, and `workflow_dispatch`, because GitHub caps
concurrent macOS jobs per org. They are required on the nightly run and
advisory on a `main` push or a manual dispatch, where GitHub sometimes cancels
a macOS leg it never assigned a runner; the nightly is where a real macOS
break turns the build red.

Two rules no ladder rung covers live in
[`docs/ci-and-tests.md`](docs/ci-and-tests.md): the commit-message skip-token
ban and test determinism (poll with `testenv.Until`; a timed-out test has a
race to fix, never a timeout to raise).

## Golden tests

Golden files under `testdata/` are the contract: if your change
alters golden-covered output, regenerate with `go test ./<pkg>/ -update`
(`internal/golden` and `internal/core/verify` register the flag) and
**review the diff** before committing. A regenerated golden you
cannot explain line-by-line is a red flag, not a green gate. Never
commit a golden update alongside unrelated changes.

## Store schemas

The Go store types are the source of truth and `make schemas` generates the
published schemas from them. A slice that changes what the script reads or
writes updates the matching schema plus the schema-contract fixtures in the
same commit, so `evals/cases/schema-contract.sh` proves the new shape instead
of a review finding it later.

## Versioning

Add `.changeset/<name>.md` (`patch`/`minor`/`major` + user-facing notes)
with any user-facing change. Use `patch` by default and `minor` for new verbs
or flags. While the package is on `0.x`, use `minor` for breaking envelope,
store, or API changes too — v1 requires an explicit operator decision. From
`1.0` onward, use `major` for breaking changes. Docs-only, eval-only, and
comment-only merges ship none, and shipped work is never backfilled.

Merging to `main` opens a `chore: version packages` PR. Merging that PR
publishes to npm (OIDC trusted publishing, no tokens) and cuts the GitHub
Release with your notes. Tags (`vX.Y.Z`) follow the releases. Store changes
within a major are additive only from `1.0` onward. The changelog is at the bottom of
[`docs/manual.md`](docs/manual.md).

## Release train

One merged Version PR ships everything. `changeset git-tag` tags the bumped
version. GoReleaser builds six static binaries (`darwin`/`linux`/`windows` ×
`amd64`/`arm64`), signs `checksums.txt` (cosign keyless bundle), emits
per-archive SPDX SBOMs, and owns the GitHub Release. Its goreleaser stage
also uploads `commit.txt` — the tag's commit SHA — and refuses to report the
release complete until GitHub serves it. The release body is the
`CHANGELOG.md` section for the tagged version — `scripts/release-notes.mjs`
prints it without the changeset commit ids and `release.sh` passes it as
`--release-notes`, then reads the published body back and rewrites it with
`gh release edit` unless it already matches — so a release reads as the curated
changeset entry, not a commit list, and an empty body fails the run instead of
shipping. Then `scripts/publish-npm.mjs` publishes six
`@stainless-code/verdict-<os>-<arch>` platform packages plus the wrapper (no
postinstall, so `--ignore-scripts` installs work). The same run renders
`dist/verdict.rb` for the `stainless-code/tap` Homebrew tap.

The wrapper also carries the TypeScript client as its `./client` subpath
export, so one install brings both. `scripts/release.sh` builds and checks
that client before it tags (a published version cannot gain it later) and
publishes with `--require-client`, which refuses a wrapper without it.

Artifact names are frozen in [`release/contract.json`](release/contract.json).
`.goreleaser.yml`, the npm scripts, the formula generator, and the archive
allowlist both installers carry must match it; the contract tests fail
otherwise. `verdict --version` prints the tag in release binaries (ldflags
`main.version`) and the `package.json` version for source builds.

To verify a release by hand, download `checksums.txt`,
`checksums.txt.sigstore.json`, and `commit.txt` from the GitHub Release, then:

```bash
cosign verify-blob --bundle checksums.txt.sigstore.json \
  --certificate-identity-regexp '^https://github\.com/stainless-code/verdict/\.github/workflows/release\.yml@refs/heads/main$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-github-workflow-trigger push \
  --certificate-github-workflow-sha "$(cat commit.txt)" \
  checksums.txt
```

The certificate identity is the `main` push that signed the release, not the
tag: the Release workflow triggers on `on: push: branches: [main]`, so GitHub
mints `.../release.yml@refs/heads/main`. The certificate's workflow-SHA claim
is the commit the tag points at, and `commit.txt` publishes that commit so
verification pins to this build instead of any run of the same workflow. The
trigger pin (`--certificate-github-workflow-trigger push`) rules out a
`workflow_dispatch` run at that same commit, which would otherwise mint the
same identity and the same SHA claim.

Then compare the archive's SHA-256 with its line in `checksums.txt`.

The release job runs this same verification against the assets it published
(`Verify the published checksums signature` in
[`.github/workflows/release.yml`](.github/workflows/release.yml)), with the
identity and issuer as literals: a release nobody can verify this way fails the
run instead of shipping.

Dry-run (publishes nothing, pushes nothing, needs no secrets):
`make release-dry-run`. It builds snapshot archives and the npm tree (a fake
`npm` records argv), renders the formula and grades it with `brew style` and
`brew audit --strict` in a throwaway local tap (`make formula-audit`), checks
the `npm pack` contents, and runs the contract tests. CI runs it on every PR.
The real chain (`scripts/release.sh`) runs only from the Release workflow
after a Version PR merges, and audits the formula again before the tap push.
The tap push stays off until tap auth lands (`TAP_PUSH=1` with
`TAP_GITHUB_TOKEN`).

`make deadcode` ratchets against unreachable functions
(`deadcode -test ./...`). Entries the tool cannot resolve, such as the
`pkg/verdict` SDK that other repositories dial, live in
[`scripts/deadcode-baseline.txt`](scripts/deadcode-baseline.txt). It runs in
`make ci-go`.

## Formatting

Markdown is normalized with one-shot `bun fmt` runs and human review. Tables
and JSON blocks stay tidy, and prompt examples are never reflowed. We commit
no formatter config (Bun is already the house runtime) and leave prose line
length unmanaged. Run `bun fmt --check .` before pushing, and fix only what it
flags outside `skills/verdict/prompts/` examples. `install-skill.sh` wires `hooks/pre-commit`
(formatting plus `shellcheck -S warning` over staged shell files; check-only,
never rewrites), so the gates run on every commit.

## Commit style

One focused commit per change: `<area>: <imperative summary>` (≤72
chars), blank line, 1–3 lines saying what changed and why. Areas
match the tree (`core`, `mcp`, `pkg`, `client`, `skill`, `upgrade`,
`action`, `ci`, `docs`, `evals`). No trailers, no merge commits on
feature branches, never touch `main` directly, and never a CI-skip directive
in the message ([`docs/ci-and-tests.md`](docs/ci-and-tests.md)).

## Issues and pull requests

Use the issue templates (bug reports name the verb and paste the
`--json` envelope; features name the surface they touch) and fill in
the PR template checklist. Verbs are: `fetch`, `post`, `triage`,
`report`, `next`, `show`, `verify`, `doctor`, `calibrate`,
`fetch-local`, `run`, `record`, `panel`, `adopt`, `fix`, `check-tree`, `ready`,
`trend`, `index`, `backtest`, `learn`, `pack`, `init`, `threads`, `skill`, `upgrade`, `uninstall`,
`serve`
— plus the hidden `mcp` stdio transport. If your change adds or
renames one, the templates, the manual, and `cmd/verdict/usage.go`
must agree; [`docs/adding-a-verb.md`](docs/adding-a-verb.md) is the
step-by-step list of every file a new verb touches.

Security bugs follow `SECURITY.md`, never the public templates.
