# Verdict

<p align="center">
  <img src="apps/docs/public/logo.svg" alt="Verdict" height="48" />
</p>

Verdict splits code review into steps your agent runs on your machine: fetch, review, gate, post. It fetches the change, your agent writes the findings, and `verdict post` submits exactly one review. No remote servers, no accounts, no daemon; `verdict serve` is an opt-in, read-only surface, loopback by default.

- **Evidence-graded findings.** Every finding carries an evidence rung, from `said` to `reproduced`, plus a severity. Unproven claims are labeled, never posted as fact.
- **A fix prompt, not just a complaint.** Every open, verified, unrefuted `critical` or `important` finding with a structured brief renders a collapsed `Prompt for AI agent` block in the posted body, computed from settled finding data — no extra model call.
- **The topology, computed.** `post` draws the change's file topology from the bundle's own file list, hunk counts, and roles: a Mermaid block in the review body, never an agent drawing.
- **The PR against its intent.** `fetch` pulls a linked GitHub issue into the bundle as a spec source, so the review checks the change against the intent the pull request claims.
- **Guarded posting.** Anchors are validated before posting. One bad anchor never fails a post. Stale-SHA and duplicate-post guards refuse to post over a moved head or a repeat.
- **Any model, any agent.** The agent you run is the review model; verdict pins no models. `verdict run --agent <name>` launches two isolated whole-diff finders with opposite-finder cross-checks across 46 built-in agent CLIs — `--finders 1` restores the single-finder workflow, `--rounds N` caps repairs per finder, `--budget auto` sizes the depth from the diff — and `settings.json` adds your own. No daemon, queue, scheduler, webhook, or model selection.
- **Try it with no setup.** `verdict demo` reviews a public pull request URL or your own working tree with zero configuration, writes nothing in your repository, and cannot post; `fetch-local` bundles your working tree and never posts either.
- **CI in one command.** `verdict init --ci github|forgejo|gitlab` writes the every-PR workflow: `fetch`, `run --agent`, `post`, advisory `report`, composed `gate` — with `ready --check` first on GitHub and GitLab.
- **Machine-readable.** Review verbs print one `--json` envelope. `report` also exports SARIF for scanners and CI.

## Who it's for

- **Solo developer** — Run `fetch-local` on your working tree or branch for an evidence-graded pre-PR pass with no remote account or subscription.
- **OSS maintainer** — Triage outside pull requests with `fetch` and `verify --sandbox` so untrusted diffs stay contained while review controls remain base-owned.
- **Startup team** — Add a deterministic `report --check` merge gate on your own CI runner with no per-seat or per-review meter.
- **Agent-heavy workflow** — Put a bounded self-review loop and `--json` envelopes between fast agent edits and `post` so unproven findings never ship as fact.
- **Regulated environment** — Keep tokens, diffs, and receipts local on your machine or runner, run a local model when code cannot leave the host, and keep append-only findings history.
- **DevEx / platform team** — Standardize review policy with pinned lens packs, typed `.verdict/config.json`, SARIF exports, and `report --cost` rollups across repositories.

**Measured:** In the [2026-11-20 results snapshot](bench/quality/results/2026-11-20/README.md) (`verdict backtest fixtures`, 16 scored runs across `fx1` and `fx2` on two drivers), the shipped flow recorded **0/16 false greens** and **0/46 hallucinated quotes** among verified gating findings — see the [ROI & benchmarks page](https://stainless-code.com/verdict/roi) for recall by fixture and the cost calculator.

## Quickstart

### 1. Install

Pick the route that matches your tools.

```bash
npm i -g @stainless-code/verdict
```

<details>
<summary>Other package managers and OS installers</summary>

```bash
pnpm add -g @stainless-code/verdict
bun add -g @stainless-code/verdict
yarn global add @stainless-code/verdict
brew install stainless-code/tap/verdict
```

pnpm's release-age gate (`minimumReleaseAge`) can serve the previous release for
a day after a new one ships, so pin the exact version or add
`--config.minimumReleaseAge=0` when you need the newest.

macOS and Linux:

```bash
curl -fsSL https://stainless-code.com/verdict/install.sh | sh
```

Windows (PowerShell):

```powershell
irm https://stainless-code.com/verdict/install.ps1 | iex
```

</details>

Check it with `verdict --version`.

### 2. Try it on a public pull request (no setup)

```bash
verdict demo https://github.com/owner/repo/pull/123
```

One review, zero configuration: `demo` reads the pull request over the public
API when `gh` is not signed in, launches the first coding-agent CLI it finds on
`PATH` with a single finder, records the review, and prints it with the change
topology. Every artifact lives in one throwaway temp directory, so nothing is
written in your repository, and posting is structurally out of reach. Inside a
git repository the same command with no argument reviews your own uncommitted
changes. `--agent <name>` picks the agent, `--keep` keeps the bundle, and
`--json` prints one envelope.

### 3. Add the skill

The installed skill is a small Bootstrap skill: it names when to use Verdict and how to reach the binary. The binary serves the full Procedure through `verdict skill`, so the instructions always match the version that checks your work.

```bash
verdict skill install --detect
```

`--detect` installs into every agent whose skills directory already exists and
is non-empty. To target one agent, run `verdict skill install --agent claude`. The agent names are in `verdict skill --help`. The shim resolves `VERDICT_BIN`, then the skill's own `bin/verdict`, then `verdict` on `PATH`; it never fetches during a review. You can also use the registry: `npx skills add stainless-code/verdict`. Use one route per machine, or the skill loads twice. The registry route installs only the skill, so install the binary first: `install.sh` is not shipped in the npm tarball. After `verdict upgrade`, re-run the install to refresh the copy.

### 4. Review a PR

You need [`gh`](https://cli.github.com) signed in with `repo` scope, and `git`. Run `verdict doctor` to check.

```bash
verdict fetch owner/repo 123                # writes .verdict/state/123/ (diff, metadata, threads)
verdict run owner/repo 123 --agent claude   # launch your agent on the bundle; it writes review.json
verdict post owner/repo 123 --dry-run       # validate anchors, post nothing
verdict post owner/repo 123                 # post exactly one review
```

`run --agent` launches any of 46 built-in agent CLIs — `claude`, `codex`, `cursor`,
`opencode`, and 42 more, listed in `verdict run --help` — plus the names you define
under `agents` in `settings.json`. `--budget auto` classifies the diff first and
launches one finder on a `light` tier, two with cross-checks on `standard` or `deep`.
`--print` renders the exact command to run yourself, and everything after `--` is
passed to the agent unchanged (`verdict run owner/repo 123 --agent claude -- --model opus`).
The agent authenticates with your key on your machine; verdict makes no network
calls itself and posts nothing.

No PR yet? Review your working tree instead. This never posts.

```bash
verdict fetch-local owner/repo work                # writes .verdict/state/local-work/
verdict run owner/repo local-work --agent claude   # launch your agent on the local bundle
verdict record owner/repo local-work               # validate anchors, save the findings
```

## Install

| You want                   | Run                                                                                                                                               |
| -------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------- |
| Try it once                | `npx @stainless-code/verdict demo <pr-url>` or `bunx @stainless-code/verdict demo <pr-url>`                                                       |
| Pin a version (npm)        | `npm i -g @stainless-code/verdict@X.Y.Z`                                                                                                          |
| Pin a version (shell)      | `curl -fsSL https://stainless-code.com/verdict/install.sh \| VERDICT_VERSION=vX.Y.Z sh`                                                           |
| Pin a version (PowerShell) | `$env:VERDICT_VERSION = 'vX.Y.Z'; irm https://stainless-code.com/verdict/install.ps1 \| iex`                                                      |
| Update                     | `verdict upgrade` (add `--dry-run` to preview)                                                                                                    |
| Uninstall                  | `verdict uninstall --remove-path`, or the owning manager: `npm uninstall -g @stainless-code/verdict`, `brew uninstall stainless-code/tap/verdict` |

The shell installer puts `verdict` in `~/.verdict/bin`. `VERDICT_INSTALL` picks another directory. It adds that directory to your PATH only if it is missing. `--no-modify-path` skips the edit.

`verdict upgrade` detects how you installed and runs the matching update. It never runs in the background.

If the docs site is down, both installers are also release assets: `https://github.com/stainless-code/verdict/releases/latest/download/install.sh` and `install.ps1`.

### Verify the download

Releases are built from tagged commits, and each release publishes `commit.txt` with the commit its signature was minted for. `checksums.txt` is signed with cosign (keyless), and the installers verify the signature — identity, issuer, push trigger, and the workflow-SHA pin to that commit — when `cosign` is on your PATH, then the archive SHA-256 always. A release without `commit.txt` is refused rather than installed unpinned. Set `VERDICT_REQUIRE_SIGNATURE=1` to fail instead of falling back to checksum-only. To check by hand, use the `cosign verify-blob` command in [Release train](CONTRIBUTING.md#release-train).

## Use it with your agent

- **Skill and CLI.** The skill from step 3 drives the CLI. It works with Claude Code, Cursor, Codex, OpenCode, Copilot, Gemini, Goose, Qwen, Windsurf, and any agent that reads an `agents` skills directory.
- **MCP.** `verdict mcp` serves the review verbs as tools over stdio. `verdict mcp config --agent claude` prints the config entry, and `--write` merges it into the file. It supports `claude`, `cursor`, and `codex`, and is experimental until 1.0. See the [manual](docs/manual.md#mcp-server-stdio-experimental-until-10).
- **Go SDK.** [`pkg/verdict`](pkg/verdict) runs the verbs in-process for Go programs, with typed requests and responses — no `verdict` subprocess, no `--json` parsing.
- **TypeScript client.** `import { createClient } from "@stainless-code/verdict/client"`. It ships in the npm package. See [`clients/ts`](clients/ts/README.md).
- **Other languages.** Every `--json` envelope and store file has a JSON Schema in [`schemas/`](schemas). The [Build a client guide](https://stainless-code.com/verdict/build-a-client) covers exit codes and codegen, with a tested [Python example](examples/python-client/).

## Hosts

`fetch` works on all six hosts. `post` submits to GitHub, GitLab, and Forgejo.

| Host      | `--provider` | Fetch uses                                                 | Post | Incremental re-review (`--base-sha`) |
| --------- | ------------ | ---------------------------------------------------------- | ---- | ------------------------------------ |
| GitHub    | `github`     | `gh`                                                       | yes  | yes                                  |
| GitLab    | `gitlab`     | `glab` (or `GITLAB_TOKEN`)                                 | yes  | yes                                  |
| GitCafe   | `gitcafe`    | `cafe` (or `CAFE_TOKEN`)                                   | no   | no                                   |
| Forgejo   | `forgejo`    | `curl` with `FORGEJO_HOST` and `FORGEJO_TOKEN`             | yes  | no                                   |
| Bitbucket | `bitbucket`  | `curl` with `BITBUCKET_TOKEN`                              | no   | no                                   |
| Azure     | `azure`      | `az` (login or `AZURE_DEVOPS_EXT_PAT`, `AZURE_DEVOPS_ORG`) | no   | no                                   |

A GitLab post is not a GitHub post: GitLab has no `REQUEST_CHANGES` review event, so a `--bind --request-changes` run posts its discussions and summary as a comment, and `post.json` records the withheld capability in `withheld[]` (`request_changes`, `no-request-changes-event`). An approval is a real MR approval. The four hosts without a compare adapter accept `--base-sha` and write an `incremental.diff.note` instead of a delta.

Forgejo takes one atomic review over its REST API through `curl`, so it carries both binding verdicts like GitHub. Its comments anchor on file line numbers, the approval event is spelled `APPROVED`, and a comment the host cannot place — no numeric line, a deleted file — is withheld with a warning rather than sent as an unanchored comment.

On the other three hosts, `record`, `report`, `show`, `next`, `triage`, and `threads` work on the fetched bundle. `post`, live `calibrate`, `ready`, and `adopt` refuse with a typed error; `calibrate --probe` runs offline on any bundle. `adopt` reaches any host verdict can post to (GitHub, GitLab, Forgejo), while `ready` and live `calibrate` run on GitHub and GitLab. Details and per-host gaps are in the [manual](docs/manual.md#providers-non-github-hosts).

## Security

- **The skill runs with your privileges.** `verify` runs the agent's `verifyCmd` through `bash -c` with a 300 second limit. A pre-exec gate refuses destructive, networked, credential-reading, and repo-escaping commands before anything spawns, and refuses a command word built from a variable (`E=curl; $E -o ./f http://host/x`) as `verify-indirection`. The gate is a tripwire, not a sandbox. For a hostile PR, `verify --sandbox` enforces the sandbox: `bubblewrap` on Linux (mounts, own PID namespace, no network) and `sandbox-exec` on macOS (no network); elsewhere follow the [recipe](docs/manual.md#hostile-pr-sandbox-recipe).
- **Review controls come from the base commit.** A PR cannot edit the panel, budget, or knowledge-base rules it is judged by. See [Review controls are base-owned](docs/manual.md#review-controls-are-base-owned).
- **Diffs and comments are untrusted input.** The prompts treat them as evidence, never as instructions.
- **Secrets stay local.** `GH_TOKEN` never leaves your machine, and provider tokens go only to their own hosts. Nothing phones home: no telemetry, no background updater.

Report vulnerabilities through [`SECURITY.md`](SECURITY.md). The assurance case —
threat model, base-owned controls, evidence gating, sandbox, and supply chain,
boundary by boundary — is [`ASSURANCE_CASE.md`](ASSURANCE_CASE.md), and
[`docs/openssf-best-practices.md`](docs/openssf-best-practices.md) audits the
project against the OpenSSF Best Practices criteria and records what is still
outstanding.

## CI

One offline command writes the every-PR workflow for your host:

```bash
verdict init --ci github            # .github/workflows/verdict-review.yml
verdict init --ci forgejo           # .forgejo/workflows/verdict-review.yml
verdict init --ci gitlab            # .gitlab/ci/verdict-review.yml
```

Every file runs the same review pipeline — `fetch`, `run --agent`, `post`, the
advisory `report`, then `gate` — and the GitHub and GitLab files put `ready --check`
first; Forgejo has no live readiness read yet. Post runs before the gate because
the gate's freshness and coverage dimensions read the receipt post writes. `--agent` takes any of
the 46 built-in launchable agents — `init --ci` resolves against the catalog, not your
`settings.json` — `--key-secret` names the repository secret, and `--print` prints
the file and writes nothing. GitLab gets a merge-request pipeline beside a minimal
root `.gitlab-ci.yml` include; an existing root pipeline is never touched.

### `setup-verdict`

[`setup-verdict`](setup-verdict/action.yml) installs a pinned, verified binary on Ubuntu, macOS, and Windows runners.

```yaml
- uses: stainless-code/verdict/setup-verdict@vX.Y.Z # pin a release tag or commit
  id: verdict
  with:
    version: vX.Y.Z
- run: ${{ steps.verdict.outputs.path }} --version
```

Inputs are `version` (default `latest`) and `require-signature` (default `true`). Outputs are `path` and `version`. A full workflow is in [`examples/verdict-review.yml`](examples/verdict-review.yml) and the [CI recipe](docs/ci-recipe.md).

### The runner is your CI

The first-party [GitHub Action](action.yml) runs the review and merge gate
on your runner with the keys you pass. Copy [the workflow](examples/verdict-review.yml),
install your agent CLI before the Action, and keep checkout `fetch-depth: 0` and
`persist-credentials: false`. Give the job `contents: read` and
`pull-requests: write`, then require its `review + merge gate` check in branch protection.

Pass `repository`, `pull-request`, and `github-token` explicitly. Choose `agent`
and pass `anthropic-api-key` or `openai-api-key` from your secrets. That four-name
ceiling — `claude`, `codex`, `cursor`, `opencode` — belongs to the composite
Action's own input validation; a workflow from `init --ci` takes any built-in
`run --agent` name. Inputs are strings — Actions composite
metadata does not support input `type` declarations. Empty key inputs overwrite
ambient provider key variables; the agent step clears `GH_TOKEN` and `GITHUB_TOKEN`.
Use a clean CI runner without preconfigured agent login credentials. Verdict
never proxies your key; the agent contacts its model provider directly.

`version` is required and accepts only pinned stable releases. Set the repository
variable `VERDICT_VERSION` to a release containing `run --agent` before enabling
the workflow — the `v0.7.0` tag is the first release with that command. The Action
reuses `setup-verdict` for checksum and signature verification and exports the
installed `version`. Pin the Action to a full commit SHA, as the example does.
The Action owns fetch → ready → run → post → report → gate; `run` makes no
network call of its own and refuses missing or stale `review.json`. A successful
review is posted to GitHub before the merge gate runs.

## Docs

- [Docs site](https://stainless-code.com/verdict): quickstart, how it reviews, comparison, ROI, glossary.
- [`docs/manual.md`](docs/manual.md): the operator manual. Every verb, flag, store file, and exit code.
- [`docs/troubleshooting.md`](docs/troubleshooting.md): every refusal code, its cause, and its fix.
- [`docs/vision.md`](docs/vision.md): what Verdict is and where its craft comes from.
- [`CHANGELOG.md`](CHANGELOG.md): release notes.

## Contributing

Build from source, run the evals, and ship a change: see [`CONTRIBUTING.md`](CONTRIBUTING.md).
Packs, offline eval fixtures, and host adapters have their own copy-pasteable
guide: [`docs/contributing-packs.md`](docs/contributing-packs.md).

## License

MIT. See [LICENSE](LICENSE).
