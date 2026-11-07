# First-party CI automation recipe

A supported, copy-paste recipe that runs verdict in your own CI with your
own runner and key: fetch → agent review → post → the findings merge gate.
No hosted service, no new secrets leaving your machines — the diff goes only
to the model provider whose key you already hold. The worked example lives
at [examples/verdict-review.yml](../examples/verdict-review.yml); copy it
into your repo as `.github/workflows/verdict-review.yml`.

## The pipeline

1. **Install** — the `setup-verdict` action installs a pinned, signature-
   verified binary and adds it to `PATH` (`version: latest` in the example;
   pin a released version for a frozen gate).
2. **Readiness pre-check** — `verdict ready <repo> <pr> --check` fails fast
   when the PR is not reviewable (failing CI, open threads, draft), saving
   agent spend before the expensive steps.
3. **Fetch** — `verdict fetch <repo> <pr>` builds `.verdict/state/<pr>/` (PR record,
   `full.diff`, `toc.txt`, `roles.txt`).
4. **Agent review** — any agent reads `full.diff` via `toc.txt` (plus
   `PR.json`, `roles.txt`, `SKILL.md` + `prompts/`) and writes exactly one
   `.verdict/state/<pr>/review.json` (`schema_version: 1`, `body`, `approved`,
   `comments[]`, optional `resolved[]`/`triage[]`).
5. **Post** — `verdict post <repo> <pr>` validates anchors, merges
   `findings.json`, and submits exactly one review.
6. **Merge gate** — one composed exit code:
   `verdict gate <repo> <pr> --max-severity critical --require-head --live auto`
   exits 0 only when no open `critical` findings stand, the fetched HEAD is
   the posted HEAD, and no live thread stands open that the store does not.
   One gate step instead of three chained checks. Require this job in branch
   protection: the readiness gate saves tokens before the review, the merged
   gate blocks the merge after it.

## Merge gating

Branch protection should require the recipe's job (`review` in the
example). The gate composes two machine exits, both documented in the
operator manual:

- `ready --check`: 0 reviewable, 1 blockers-or-wait, 2 usage, 3 tool error.
- `gate --max-severity <tier> [--require-head] [--live auto|strict|skip]`:
  0 every requested dimension passed (a skip is neutral), 1 a dimension
  failed or read unknown, 2 usage, 3 tool error. It composes the same three
  dimensions as `report --check <tier> [--completeness]` plus the live-thread
  recheck — 0 no open findings at-or-above the tier (and HEAD-reviewed with
  `--require-head`), 1 findings, stale HEAD, or an outnumbering live-thread
  set. Only `open` findings count; `report` without a gate flag never gates,
  and `report --check --completeness` stays the narrower two-dimension form.

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
may be empty — that is the current state — and the gate then simply has no
second tier. Keep a lane required unless the operator has decided that its
failures are not merge-blocking; a green, deterministic leg belongs on the
required side whatever platform it runs on.

## Permissions and secrets

| Need                                     | Value                                     |
| ---------------------------------------- | ----------------------------------------- |
| `contents: read`, `pull-requests: write` | fetch/post/record plus the review comment |
| `GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}`  | GitHub reads and the posted review        |
| A model key (e.g. `ANTHROPIC_API_KEY`)   | your agent's key, in your repo's secrets  |

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
empty; `gh` authenticates from `GH_TOKEN` only. Fork PRs have no secrets:
run the fetch + advisory steps there, and keep `post` on same-repo runs.

## Pin policy

Every `uses:` in the example pins the full 40-hex commit SHA, never a
moving tag; the trailing `# vX` comment names the release the SHA resolves
to so bumps stay reviewable. Re-pin `setup-verdict` to the newest release
commit periodically. The eval suite grades this file: unpinned actions and
missing gate steps fail the build.

## Local counterpart

The same two gates run locally before CI burns: the pre-push probe
(`gate --max-severity critical --require-head` in `.git/hooks/pre-push`) and the
readiness pre-check. See the operator manual's hook recipe and the
readiness/report gate sections.
