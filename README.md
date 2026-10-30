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

Requirements: [`gh`](https://cli.github.com) (authed, `repo` scope), `jq`, `shasum`, `git`.
Run `./scripts/review.sh doctor` to preflight.

## Install

Pick one route per machine (two copies = double-loaded skill):

```bash
# registry (copies files; -g installs globally, omit for project-local)
npx skills add stainless-code/verdict -g
# refresh registry copies any time (filters match skill names, not owner/repo)
npx skills check    # report outdated, change nothing
npx skills update   # update everything in scope (add -g for global)

# source install (symlink; `git pull` updates, no updater daemon by design)
git clone https://github.com/stainless-code/verdict.git
cd verdict
./install.sh
```

`install.sh` runs `doctor` first. A bare run links `~/.agents/skills`, the shared
standard dir. `--detect` adds every agent with an existing skills setup.
`--all` links all nine known agents (`~/.agents`, `~/.claude`, `~/.cursor`,
`~/.config/opencode`, `~/.copilot`, `~/.gemini`, `~/.config/goose`, `~/.qwen`,
`~/.codeium/windsurf`; XDG and `CLAUDE_CONFIG_DIR` aware). Install.sh skips
occupied slots, never overwrites without `--force`, and never touches a foreign
`core.hooksPath`. It never re-links an agent you removed. Explicit removals stick:

```bash
./install.sh --agent cursor            # link one agent
./install.sh --dir ~/.foo/skills       # link any other agent's skills dir
./install.sh --project ~/work/repo     # link into a repo's .agents/skills
./install.sh --detect                  # standard dir plus installed agents
./install.sh --all                     # every known agent
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
patch record). Reviewing a hostile PR? Run in a sandbox. Fetched diffs and
comments are untrusted input; the prompts treat them as evidence, never as
instructions.

Secrets stay local: `GH_TOKEN` never leaves your machine. Provider tokens go
only to their configured hosts. Azure fetch stages Basic auth in a `0700` temp
dir, and trap deletes it on exit (residual risk: a crash between write and cleanup).
Nothing phones home: no servers, no telemetry, no auto-updater.

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
  apps/docs/          # Blume docs site (index + manual mirror, deploys to /verdict/)
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
Additive store changes only within a major. See the changelog at the bottom of
[`docs/manual.md`](docs/manual.md).

## Formatting

Markdown is normalized with one-shot `bun fmt` runs and human review. Tables and JSON
blocks stay tidy, and prompt examples are never reflowed. We commit no formatter config
(Bun is already the house runtime); we leave prose line length unmanaged
(renderers and agents don't care).
Contributors: run `bun fmt --check .` before pushing; fix only what it flags outside
`prompts/` examples. `install.sh` wires `hooks/pre-commit` (check-only, never rewrites),
so the gate runs automatically on every commit.

## License

MIT. See [LICENSE](LICENSE).
