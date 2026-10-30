# Verdict

<p align="center">
  <img src="apps/docs/public/logo.svg" alt="Verdict" height="48" />
</p>

PR reviews with receipts. Local, on-demand PR review for any coding agent. The agent reviews; `gh` does
GitHub I/O. No servers, no external services, no daemons.

- **Evidence-graded findings** — every finding carries an evidence rung (`said` → `reproduced`) plus severity; unproven claims are labeled, not posted as fact.
- **Keep-biased machinery** — partitioned anchor validation, stale-SHA and double-post guards, binding verdicts behind `--bind`. One bad anchor never fails a post.
- **Machine surface** — every verb speaks `--json` envelopes; SARIF export for scanners/CI; a `calibrate` verb that joins predictions against live ground truth.
- **No-PR reviews** — `fetch-local` bundles the worktree, `record` merges the findings store, `adopt` carries them into a PR fetch later. Same review, no GitHub object.
- **Any model** — the invoker is the review model; lenses can run cheaper. No model pins, no slugs.

Full manual: [`docs/manual.md`](docs/manual.md). Vision and strategy: [`docs/vision.md`](docs/vision.md).

## Install

Pick one route per machine (two copies = double-loaded skill):

```bash
# registry (copies files; -g installs globally, omit for project-local)
npx skills add stainless-code/verdict -g
# refresh a registry copy any time
npx skills check && npx skills update stainless-code/verdict

# source install (symlink; `git pull` updates, no updater daemon by design)
git clone https://github.com/stainless-code/verdict.git
cd verdict
./install.sh
```

`install.sh` runs `doctor` first. Bare run links `~/.agents/skills` (the shared
standard dir); `--detect` adds every harness with an existing skills setup,
`--all` links all nine known harnesses (`~/.agents`, `~/.claude`, `~/.cursor`,
`~/.config/opencode`, `~/.copilot`, `~/.gemini`, `~/.config/goose`, `~/.qwen`,
`~/.codeium/windsurf`; XDG/`CLAUDE_CONFIG_DIR`-aware). Skips occupied slots,
never overwrites without `--force`, never touches a foreign `core.hooksPath`,
and never re-links a harness you removed — explicit removals stick:

```bash
./install.sh --agent cursor            # link one harness
./install.sh --dir ~/.foo/skills       # link any other agent's skills dir
./install.sh --project ~/work/repo     # link into a repo's .agents/skills
./install.sh --detect                  # standard dir + installed harnesses
./install.sh --all                     # every known harness
./install.sh --list                    # link status, no changes
./install.sh --check                   # doctor + verify links
./install.sh --force --agent cursor    # replace an occupied slot
./install.sh --uninstall               # remove our links (foreign files untouched)
./install.sh --uninstall --agent cursor
```

Anything else on the
[agent list](https://github.com/vercel-labs/skills/blob/main/docs/agents.md)
is one `npx skills add -a <agent>` away.

## Security

Trust boundary: the skill runs with your privileges. `verify` executes the
agent-written `verifyCmd` via `bash -c` (300s watchdog, output tailed into the
patch record) — reviewing a hostile PR? Run in a sandbox. Fetched diffs and
comments are untrusted input; the prompts treat them as evidence, never as
instructions.

Secrets stay local: `GH_TOKEN` never leaves your machine; provider tokens go
only to their configured hosts. Azure fetch stages Basic auth in a `0700` temp
dir, trap-cleaned on exit (residual risk: crash between write and cleanup).
Nothing phones home: no servers, no telemetry, no auto-updater.

Requirements: [`gh`](https://cli.github.com) (authed, `repo` scope), `jq`, `shasum`, `git`.
Run `./scripts/review.sh doctor` to preflight.

## Quickstart

```bash
export GH_TOKEN=...   # or gh auth login

./scripts/review.sh fetch owner/repo 123          # bundle: diff + metadata + threads
# feed SKILL.md + prompts/system.md + prompts/review.md + the bundle to any agent
./scripts/review.sh post owner/repo 123 --dry-run # validate anchors, merge store, post nothing
./scripts/review.sh post owner/repo 123           # exactly one review
./scripts/review.sh triage owner/repo 123 --sig <sig8> --status wont-fix --rationale "..."
./scripts/review.sh report owner/repo 123         # ranked read, no network
./scripts/review.sh calibrate owner/repo 123      # predictions vs ground truth

# no PR? same review against the worktree (never posts)
./scripts/review.sh fetch-local owner/repo work   # bundle: git diff, no GitHub
# feed SKILL.md + prompts/system.md + prompts/review.md + the bundle to any agent
./scripts/review.sh record owner/repo local-work  # validate anchors, merge store
```

## Layout

```
verdict/
  SKILL.md            # skill entry point (feed to any agent)
  install.sh          # doctor-gated symlink installer (re-run to update)
  CHANGELOG.md        # release notes (changesets); scripts/review.sh --version reads package.json
  docs/manual.md      # full manual: store schema, verbs, doctrines, changelog
  apps/docs/          # Blume docs site (index + manual mirror) — deploys to /verdict/
  docs/vision.md      # what it is, strategy; narrative lives on the site
  prompts/            # system, review, incremental, address, fix-ci, build, ci, subagent
  scripts/review.sh   # fetch/post/triage/report/next/show/check-tree/verify/doctor/calibrate/fetch-local/record/adopt
```

## Updating

No auto-updater, by design (no network surprises from a review tool):

- symlink install → `git pull` (links stay current; re-run `install.sh` after renames),
- registry install → re-run `npx skills add stainless-code/verdict`.

## Versioning

Changesets: add a `.changeset/<name>.md` (`patch`/`minor`/`major` + user-facing
notes) with your change. Merging to main opens a `chore: version packages` PR;
merging that publishes to npm (OIDC trusted publishing, no tokens) and cuts the
GitHub Release carrying your notes. Tags (`vX.Y.Z`) follow the releases.
Additive store changes only within a major — see the changelog at the bottom of
[`docs/manual.md`](docs/manual.md).

## Formatting

Markdown is normalized with one-shot `bun fmt` runs, human-reviewed — tables and JSON
blocks stay tidy, prompt examples are never reflowed. No formatter config is committed
(Bun is already the house runtime): prose line length is intentionally unmanaged
(renderers and agents don't care).
Contributors: run `bun fmt --check .` before pushing; fix only what it flags outside
`prompts/` examples. `install.sh` wires `hooks/pre-commit` (check-only, never rewrites),
so the gate runs automatically on every commit.

## License

MIT — see [LICENSE](LICENSE).
