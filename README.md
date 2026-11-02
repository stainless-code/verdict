# Verdict

<p align="center">
  <img src="apps/docs/public/logo.svg" alt="Verdict" height="48" />
</p>

PR reviews with receipts. Local, on-demand PR review for any coding agent. The agent reviews; `gh` does
GitHub I/O. No servers, no external services, no daemons.

- **Evidence-graded findings.** Every finding carries an evidence rung (`said` → `reproduced`) plus severity. The tool labels unproven claims and never posts them as fact.
- **Keep-biased machinery.** Partitioned anchor validation, stale-SHA and double-post guards, binding verdicts behind `--bind`. One bad anchor never fails a post.
- **Machine-readable.** Every verb speaks `--json` envelopes. SARIF export for scanners and CI. A `calibrate` verb joins predictions against live ground truth.
- **No-PR reviews.** `fetch-local` bundles the worktree, `record` merges the findings store, `adopt` carries them into a PR fetch later. Same review, no GitHub object.
- **Any model.** The invoker is the review model. Lenses can run cheaper. No model pins, no slugs.

Full manual: [`docs/manual.md`](docs/manual.md). Vision and strategy: [`docs/vision.md`](docs/vision.md).

Requirements: [`gh`](https://cli.github.com) (authed, `repo` scope), `jq` (>= 1.7), `shasum`, `git`, Go (to build).
Run `verdict doctor` to preflight (`make build` writes `bin/verdict`).

## Install

The default install is one verified command. It puts `verdict` in
`~/.verdict/bin`, checks the release before installing, and never needs root
or a daemon.

```bash
# macOS / Linux (sh, bash, zsh, dash, ash)
curl -fsSL https://github.com/stainless-code/verdict/releases/latest/download/install.sh | sh

# Windows (PowerShell)
irm https://github.com/stainless-code/verdict/releases/latest/download/install.ps1 | iex
```

Pin a version with `VERDICT_VERSION=vX.Y.Z` (Windows: `-Version vX.Y.Z`).
`VERDICT_INSTALL` picks another directory (default `~/.verdict/bin`).
The installer fetches the archive, `checksums.txt`, and the sigstore bundle,
verifies the cosign identity/issuer and the archive SHA-256, extracts only
`verdict`, fsyncs, and atomically renames a staged file over the target. Any
failure leaves the old binary in place. Without cosign it warns loudly and
verifies the checksum only; `VERDICT_REQUIRE_SIGNATURE=1` fails closed. It
never executes the download before verification, and it never evals release
metadata.

PATH: the installer appends one marked line (`# verdict installer`) to your
shell config only when the install dir is missing. `VERDICT_NO_MODIFY_PATH=1`
or `--no-modify-path` skips that; `install.ps1` uses the user PATH and the
same opt-outs (`-NoModifyPath`).

Offline / air-gapped mirrors, and local archives with their sibling checksums:

```bash
VERDICT_DOWNLOAD_URL=file:///srv/verdict/releases sh install.sh
sh install.sh ./verdict_1.2.3_linux_amd64.tar.gz
```

A local archive needs the sibling `checksums.txt` (and, when cosign is
present, `checksums.txt.sigstore.json`). Uninstall removes only
installer-managed files and, with `--remove-path`, its own PATH line:

```bash
sh install.sh --uninstall --remove-path
# Windows
./install.ps1 -Uninstall -RemovePath
```

### Updating

`verdict upgrade` is explicit and channel-aware: it never runs in the
background, never checks at review time, and never prompts. Typing the command
is the consent.

```bash
verdict upgrade --version v1.2.3   # pin (default: latest through the Releases redirect)
verdict upgrade --dry-run          # print the detected channel + planned action, touch nothing
verdict upgrade --json             # one machine envelope
```

| Detected install                                                           | `verdict upgrade` does                                                                                                                                                                                                                                                                                     |
| -------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Installer-managed (`~/.verdict/bin` or `VERDICT_INSTALL`, receipt present) | Verified self-replace: fetch archive + `checksums.txt` + sigstore bundle, verify cosign identity/issuer (or loudly checksum-only), SHA-256, extract only `verdict`, `--version` smoke, atomic replace, roll back on failure. Windows never overwrites the running `.exe`: a helper replaces it after exit. |
| npm / pnpm / bun / yarn global                                             | Prints the exact manager command, then runs it (no prompt) and streams its output: `npm i -g @stainless-code/verdict@latest`, `pnpm add -g …`, `bun add -g …`, `yarn global add …`.                                                                                                                        |
| Homebrew                                                                   | Prints and runs `brew upgrade stainless-code/tap/verdict` (the formula owns version selection).                                                                                                                                                                                                            |
| Project-local `node_modules`                                               | Refuses typed: the upgrade belongs in the project manifest/lockfile (`npm install @stainless-code/verdict@latest`).                                                                                                                                                                                        |
| Skill copy                                                                 | Refuses typed: `npx skills update` (or the skill's `bootstrap`).                                                                                                                                                                                                                                           |
| sudo/admin, unwritable, or unidentifiable                                  | Refuses typed with the reason and the exact installer command; never escalates privileges.                                                                                                                                                                                                                 |

`verdict uninstall` removes only installer-managed files (the binary plus its
`.verdict-installer` receipt); `--remove-path` also removes the marked
`# verdict installer` PATH line. It supports `--dry-run` / `--json` and
refuses npm/brew/project-local/skill copies with the owning removal command.
`VERDICT_INSTALL_METHOD` overrides channel detection for tests.

### CI: setup-verdict action

One `uses:` line installs a pinned, verified `verdict` on Ubuntu, macOS,
and Windows runners. It runs the same `install.sh`/`install.ps1` verifier
as above (checksum plus cosign, fail-closed), never touches shell rc files,
and exposes the binary path:

```yaml
- uses: stainless-code/verdict/setup-verdict@v1 # keep the tag pinned
  with:
    version: v1.2.3 # `latest` also works, but a pin is recommended
- run: ${{ steps.verdict.outputs.path }} --version # or just `verdict`
```

Inputs: `version` (default `latest`; `X.Y.Z` or `vX.Y.Z`) and
`require-signature` (default `'true'`; `'false'` falls back to a
checksum-only install with a warning). Outputs: `path` (absolute path to
the installed binary) and `version` (installed version, leading `v`
stripped). The binary is cached by version + OS + arch (pinned versions
only; `latest` always re-resolves) and a second call with the same version
reuses it without downloading. Set an id on the step
(`id: verdict`) to read `steps.verdict.outputs.path`.

The action lives in-repo at `setup-verdict/` so it versions with the
installers it calls; a dedicated `stainless-code/setup-verdict` repo stays
an option if other projects ever need it standalone.

### Other routes

Pick one route per machine (two copies = double-loaded skill):

```bash
# registry route (npx skills is scoped to the skills dir): install the GLOBAL
# binary once (curl/npm/brew above) and the skill resolves `verdict` on PATH.
# install.sh is NOT shipped in the npm tarball and never runs here.
npx skills add stainless-code/verdict -g
npx skills check    # report outdated, change nothing
npx skills update   # update everything in scope (add -g for global)

# no global binary yet? one explicit, verified bootstrap (never at review
# time) writes <skillsdir>/verdict/bin/verdict; pin it with VERDICT_VERSION.
sh <skillsdir>/verdict/scripts/verdict bootstrap

# source route (symlink into a git checkout; `git pull` updates, no updater
# daemon by design). install-skill.sh runs only from a checkout — it refuses
# anywhere else.
git clone https://github.com/stainless-code/verdict.git
cd verdict
make build
./install-skill.sh
```

`install-skill.sh` runs `doctor` first. A bare run links `~/.agents/skills`,
the shared standard dir. `--detect` adds every agent with an existing skills
setup. `--all` links all nine known agents (`~/.agents`, `~/.claude`,
`~/.cursor`, `~/.config/opencode`, `~/.copilot`, `~/.gemini`,
`~/.config/goose`, `~/.qwen`, `~/.codeium/windsurf`; XDG and
`CLAUDE_CONFIG_DIR` aware). It skips occupied slots, never overwrites without
`--force`, and never touches a foreign `core.hooksPath`. It never re-links an
agent you removed. Explicit removals stick:

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

Anything else on the
[agent list](https://github.com/vercel-labs/skills/blob/main/docs/agents.md)
is one `npx skills add -a <agent>` away.

Operator verbs (offline, same preview-then-confirm idiom): scaffold repo
controls with `verdict init --preset standard --yes`, copy the skill payload
(`SKILL.md` + `prompts/` + `scripts/`) with `verdict install --yes`
(detects `.agents/skills`, `.claude/skills`, `.codex/skills`; only writes
installer-owned `verdict/` dirs), or write the binary's own embedded skill
atomically with `verdict skill install --agent claude` (or `--dir
<skills-dir>`; previews create/update/keep/blocked, refuses a foreign target
unless `--force`, never prompts, supports `--dry-run`/`--json`). The
`install-skill.sh` symlink route is the wide-coverage sibling for git
checkouts only (nine agents, `--detect`/`--all`).

## Security

Trust boundary: the skill runs with your privileges. `verify` executes the
agent-written `verifyCmd` via `bash -c` (300s watchdog, output tailed into the
patch record) — and a pre-exec allowlist gate refuses destructive, networked,
credential-reading, or repo-escaping commands with typed errors before anything
spawns. Reviewing a hostile PR? Run the verify step in a sandbox (recipe in
`docs/manual.md`). Fetched diffs and
comments are untrusted input; the prompts treat them as evidence, never as
instructions.

Secrets stay local: `GH_TOKEN` never leaves your machine. Provider tokens go
only to their configured hosts. Azure fetch stages Basic auth in a `0700` temp
dir, and trap deletes it on exit (residual risk: a crash between write and cleanup).
Nothing phones home: no servers, no telemetry, no auto-updater.

## Quickstart

```bash
export GH_TOKEN=...   # or gh auth login

verdict fetch owner/repo 123          # bundle: diff + metadata + threads
# feed SKILL.md + prompts/system.md + prompts/review.md + the bundle to any agent
verdict post owner/repo 123 --dry-run # validate anchors, merge store, post nothing
verdict post owner/repo 123           # exactly one review
verdict triage owner/repo 123 --sig <sig8> --status wont-fix --rationale "..."
verdict report owner/repo 123         # ranked read, no network
verdict calibrate owner/repo 123      # predictions vs ground truth

# no PR? same review against the worktree (never posts)
verdict fetch-local owner/repo work   # bundle: git diff, no GitHub
# feed SKILL.md + prompts/system.md + prompts/review.md + the bundle to any agent
verdict record owner/repo local-work  # validate anchors, merge store
```

## Layout

```
verdict/
  SKILL.md            # skill entry point (feed to any agent)
  install.sh          # verified binary installer (macOS/Linux release asset)
  install.ps1         # verified binary installer (Windows release asset)
  install-skill.sh    # doctor-gated symlink installer for checkouts (re-run to update)
  CHANGELOG.md        # release notes (changesets); verdict --version reads package.json
  docs/manual.md      # full manual: store schema, verbs, doctrines, changelog
  apps/docs/          # Blume docs site (index + manual mirror, deploys to /verdict/)
  docs/vision.md      # what it is, strategy; narrative lives on the site
  prompts/            # system, review, incremental, address, fix-ci, build, ci, grade, diagrams, subagent
  scripts/verdict     # POSIX skill shim: VERDICT_BIN, bundled bin, PATH, then bootstrap
  scripts/verdict-complete.sh  # bash/zsh completion for verdict
  bin/verdict         # the CLI (make build; gitignored): fetch/post/triage/report/next/show/check-tree/verify/doctor/calibrate/fetch-local/record/adopt/fix/ready/trend/learn/init/install/skill
```

## Updating

No auto-updater, by design (no network surprises from a review tool):

- binary install → re-run the installer (or `verdict upgrade` when it ships),
- symlink install → `git pull` (links stay current; re-run `install-skill.sh`
  after renames),
- registry install → `npx skills update` (or re-run `npx skills add stainless-code/verdict`),
- embedded skill copy (`verdict skill install`) → re-run it after upgrading the binary.

## Versioning

Changesets: add a `.changeset/<name>.md` (`patch`/`minor`/`major` + user-facing
notes) with your change — `patch` by default, `minor` for new verbs or flags,
`major` for envelope/store breaks. Docs-only, eval-only, and comment-only
merges ship none (no backfill of shipped work). Merging to main opens a `chore: version packages` PR;
merging that publishes to npm (OIDC trusted publishing, no tokens) and cuts the
GitHub Release carrying your notes. Tags (`vX.Y.Z`) follow the releases.
Additive store changes only within a major. See the changelog at the bottom of
[`docs/manual.md`](docs/manual.md).

## Release train

One merged Version PR ships everything: `changeset git-tag` tags the bumped
version, GoReleaser builds six static binaries (`darwin`/`linux`/`windows` ×
`amd64`/`arm64`), signs `checksums.txt` (cosign keyless bundle), emits
per-archive SPDX SBOMs, and owns the GitHub Release; then
`scripts/publish-npm.mjs` publishes six `@stainless-code/verdict-<os>-<arch>`
platform packages plus the wrapper (no postinstall — `--ignore-scripts`
installs work), and the same run renders `dist/verdict.rb` for the
`stainless-code/tap` Homebrew tap
(`brew install stainless-code/tap/verdict`).

Artifact names are frozen in [`release/contract.json`](release/contract.json);
`.goreleaser.yml`, the npm scripts, and the formula generator must match it
(the contract tests fail otherwise). `verdict --version` prints the tag in
release binaries (ldflags `main.version`) and the `package.json` version for
source builds.

Dry-run (publishes nothing, pushes nothing, needs no secrets):
`make release-dry-run` — snapshot archives, the npm tree (a fake `npm`
records argv), the formula, `npm pack` content checks, and the contract
tests. CI runs it on every PR. The real chain (`scripts/release.sh`) runs
only from the Release workflow after a Version PR merges; the tap push stays
off until tap auth lands (`TAP_PUSH=1` with `TAP_GITHUB_TOKEN`).

## Formatting

Markdown is normalized with one-shot `bun fmt` runs and human review. Tables and JSON
blocks stay tidy, and prompt examples are never reflowed. We commit no formatter config
(Bun is already the house runtime); we leave prose line length unmanaged
(renderers and agents don't care).
Contributors: run `bun fmt --check .` before pushing; fix only what it flags outside
`prompts/` examples. `install-skill.sh` wires `hooks/pre-commit` (formatting plus
`shellcheck -S warning` over staged shell files; check-only, never rewrites),
so the gates run automatically on every commit.

## License

MIT. See [LICENSE](LICENSE).
