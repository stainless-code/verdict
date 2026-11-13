# First-party CI automation recipe

A supported, copy-paste recipe that runs verdict in your own CI with your
own runner and key: fetch → agent review → post → the findings merge gate.
No hosted service, no new secrets leaving your machines — the diff goes only
to the model provider whose key you already hold. The worked example lives
at [examples/verdict-review.yml](../examples/verdict-review.yml); copy it
into your repo as `.github/workflows/verdict-review.yml`.

## The pipeline

1. **Install** — the `setup-verdict` action installs a pinned, signature-
   verified binary and adds it to `PATH` (set `vars.VERDICT_VERSION` to a release containing `run --agent`).
2. **Readiness pre-check** — `verdict ready <repo> <pr> --check` fails fast
   when the PR is not reviewable (failing CI, open threads, draft), saving
   agent spend before the expensive steps.
3. **Fetch** — `verdict fetch <repo> <pr>` builds `.verdict/state/<pr>/` (PR record,
   `full.diff`, `toc.txt`, `roles.txt`).
4. **Agent review** — `verdict run <repo> <pr> --agent "$VERDICT_AGENT"`
   spawns your agent once against the fetched bundle. The Action's `agent` input chooses the
   agent: it sets `VERDICT_AGENT` in the step's `env:` (one of `claude`, `codex`,
   `cursor`, `opencode` — the shipped `run --agent` table) and the `--agent`
   argument that reads it. Install that CLI on the runner in an earlier step;
   the recipe never downloads it. The agent reads `full.diff` via `toc.txt`
   (plus `PR.json`, `roles.txt`, `SKILL.md` + `prompts/`) and writes exactly
   one `.verdict/state/<pr>/review.json` (`schema_version: 1`, `body`,
   `approved`, `comments[]`, optional `resolved[]`/`triage[]`). The step fails
   unless that file is fresh: `run` refuses `no-review-artifact` when the agent
   wrote nothing and `stale-review-artifact` when it left an earlier run's
   file, so a silent agent cannot reach `post`.
5. **Post** — `verdict post <repo> <pr>` validates anchors, merges
   `findings.json`, and submits exactly one review.
6. **Merge gate** — one composed exit code:
   `verdict gate <repo> <pr> --max-severity critical --require-head --live auto`
   exits 0 only when no open `critical` findings stand, the fetched HEAD is
   the posted HEAD, the stored diff-coverage receipt for that HEAD attests the
   whole diff, and no live thread stands open that the store does not. One
   gate step instead of chained checks. Require this job in branch
   protection: the readiness gate saves tokens before the review, the merged
   gate blocks the merge after it.

## Merge gating

Branch protection should require the recipe's job (`review` in the
example). The gate composes two machine exits, both documented in the
operator manual:

- `ready --check`: 0 reviewable, 1 blockers-or-wait, 2 usage, 3 tool error.
- `gate --max-severity <tier> [--require-head]`, with the `--live` and
  `--coverage` postures (defaults `auto` and `require`):
  0 every requested dimension passed (a skip is neutral), 1 a dimension
  failed or read unknown, 2 usage, 3 tool error. Its dimensions are the
  findings tier, the HEAD freshness check, the stored diff-coverage receipt,
  and the live-thread recheck — 0 no open findings at-or-above the tier (and
  HEAD-reviewed with `--require-head`, coverage attested for the same HEAD),
  1 findings, stale HEAD, a partial attestation, or an outnumbering
  live-thread set. The coverage dimension is `require` by default because the
  recipe posts before it gates: the receipt is written by then, but its
  attestation is whatever the review wrote. A review that skipped files, or
  wrote no `coverage[]` at all, fails here instead of merging — so the review
  step must produce that attestation (or every `chunks[]` entry on a chunked
  bundle). Pass `--coverage warn` as the migration while it does not: the
  dimension then reports a partial or missing attestation without failing.
  Only `open` findings count, and the advisory `verdict report` step never
  gates.

Triage or fix, then re-fetch and re-run: there is no `--force` — fetch +
triage + re-check is the recovery.

## Required versus advisory legs

A job either blocks a merge or it does not — never both, and never by
accident. Declare the split once: `ci-complete` in this repository's own
workflow carries `REQUIRED_JOBS` and `ADVISORY_JOBS`, and its `needs` list
must name exactly the union, so a new job cannot stay silently optional
(`internal/archtest` fails the build on drift). A required lane must be
literally `success`; a skip or a cancel fails it, because a skipped job
reports success to branch protection. An advisory lane only has to report a
result: it shows up red on the PR without blocking anyone. `ADVISORY_JOBS`
carries the lanes the operator has decided are not merge-blocking — `fuzz`
and `bench` today — and every other job is required, because a green,
deterministic leg belongs there whatever platform it runs on.

## Permissions and secrets

| Need                                     | Value                                     |
| ---------------------------------------- | ----------------------------------------- |
| `contents: read`, `pull-requests: write` | fetch/post plus the posted review comment |
| `GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}`  | GitHub reads and the posted review        |
| A model key (e.g. `ANTHROPIC_API_KEY`)   | your agent's key, in your repo's secrets  |

The agent step is the only place the recipe touches a model key. The key
comes from your repo's secrets into that step's environment
(`ANTHROPIC_API_KEY` above — use whatever variable your agent reads), and
`verdict run` makes no network call of its own and never proxies your key: it
spawns the agent and checks the artifact, while the model traffic is the
agent's, on your runner, to the provider whose key you already hold. No
verdict-hosted service sees the diff or the key.

`fetch-depth: 0` on checkout keeps the PR base commit in the clone: verdict
reads the review controls (`.verdict/config.json`, `.verdict/kb.jsonl`,
`.verdict/learnings.md`, `.verdict/overrides/*`, ...) from that commit with `git show <base>:<path>`, so a PR cannot edit the
rules it is judged by. When the checkout cannot supply the base (a shallow
checkout, or no checkout at all) a GitHub `fetch` reads the same base commit
through the contents API instead (`gh api repos/<owner>/<repo>/contents/<path>?ref=<base>`,
so `contents: read` is enough); other providers refuse `controls-unavailable`
(verdict never fetches the base for you). A targeted
`git fetch --depth=1 origin <base-sha>` step is the minimal alternative.
`persist-credentials: false` on checkout keeps the git credential helper
empty; `gh` authenticates from `GH_TOKEN` only. `GH_TOKEN` is scoped to the
steps that call `gh`, so the agent step never inherits the write token.

Fork PRs have no secrets. Without a guard, the shipped job fails at the agent
step: `ANTHROPIC_API_KEY` is empty, `verdict run` reports `agent-missing` or a
non-zero agent exit, and the job goes red instead of silently skipping review.
That is the intended default, because a skipped review reads as a pass. If you
would rather skip than fail, gate the agent and `post` steps on
`github.event.pull_request.head.repo.full_name == github.repository` and run
only `fetch`, `report`, and `gate` on forks.

## Pin policy

Every `uses:` in the example pins the full 40-hex commit SHA, never a
moving tag; the trailing `# vX` comment names the release the SHA resolves
to so bumps stay reviewable. Re-pin `setup-verdict` to the newest release
commit periodically. The eval suite grades this file: unpinned actions, a
placeholder agent step, and missing gate steps fail the build.

## Local counterpart

The same two gates run locally before CI burns: the pre-push probe
(`gate --max-severity critical --require-head` in `.git/hooks/pre-push`) and the
readiness pre-check. See the operator manual's hook recipe and the
readiness/report gate sections.
