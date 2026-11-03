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
3. **Fetch** — `verdict fetch <repo> <pr>` builds `.review/<pr>/` (PR record,
   `full.diff`, `toc.txt`, `roles.txt`).
4. **Agent review** — any agent reads `full.diff` via `toc.txt` (plus
   `PR.json`, `roles.txt`, `SKILL.md` + `prompts/`) and writes exactly one
   `.review/<pr>/review.json` (`schema_version: 1`, `body`, `approved`,
   `comments[]`, optional `resolved[]`/`triage[]`).
5. **Post** — `verdict post <repo> <pr>` validates anchors, merges
   `findings.json`, and submits exactly one review.
6. **Merge gate** — `verdict report <repo> <pr> --check critical
--completeness` exits 0 only when no open `critical` findings stand AND
   the fetched HEAD is the posted HEAD. Require this job in branch
   protection: the readiness gate saves tokens before the review, the
   findings gate blocks the merge after it.

## Merge gating

Branch protection should require the recipe's job (`review` in the
example). The gate composes two machine exits, both documented in the
operator manual:

- `ready --check`: 0 reviewable, 1 blockers-or-wait, 2 usage, 3 tool error.
- `report --check <tier> [--completeness]`: 0 no open findings at-or-above
  the tier (and HEAD-reviewed with `--completeness`), 1 findings or stale
  HEAD, 2 usage, 3 corrupt store. Only `open` findings count; advisory
  `report` without `--check` never gates.

Triage or fix, then re-fetch and re-run: there is no `--force` — fetch +
triage + re-check is the recovery.

## Permissions and secrets

| Need                                     | Value                                     |
| ---------------------------------------- | ----------------------------------------- |
| `contents: read`, `pull-requests: write` | fetch/post/record plus the review comment |
| `GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}`  | GitHub reads and the posted review        |
| A model key (e.g. `ANTHROPIC_API_KEY`)   | your agent's key, in your repo's secrets  |

`fetch-depth: 0` on checkout keeps the PR base commit in the clone: verdict
reads the review controls (`.review/kb.jsonl`, `panel`, `budget`, overrides,
...) from that commit with `git show <base>:<path>`, so a PR cannot edit the
rules it is judged by. A shallow checkout without the base refuses
`controls-unavailable` (verdict never fetches it for you); a targeted
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
(`report --check critical --completeness` in `.git/hooks/pre-push`) and the
readiness pre-check. See the operator manual's hook recipe and the
readiness/report gate sections.
