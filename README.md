# Verdict

<p align="center">
  <img src="apps/docs/public/logo.svg" alt="Verdict" height="48" />
</p>

Verdict splits code review into steps your agent runs on your machine: fetch, review, gate, post. It fetches the change, your agent writes the findings, and `verdict post` submits exactly one review. No servers, no accounts, no daemon.

- **Evidence-graded findings.** Every finding carries an evidence rung, from `said` to `reproduced`, plus a severity. Unproven claims are labeled, never posted as fact.
- **Guarded posting.** Anchors are validated before posting. One bad anchor never fails a post. Stale-SHA and duplicate-post guards refuse to post over a moved head or a repeat.
- **Any model, any agent.** The agent you run is the review model. Verdict pins no models.
- **Machine-readable.** Review verbs print one `--json` envelope. `report` also exports SARIF for scanners and CI.
- **No PR needed.** `fetch-local` reviews your working tree and never posts.

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

### 2. Add the skill

The skill is the review prompt. It ships inside the binary.

```bash
verdict skill install --detect
```

`--detect` installs into every agent whose skills directory already exists and
is non-empty. To target one agent, run `verdict skill install --agent claude`. The agent names are in `verdict skill --help`. You can also use the registry: `npx skills add stainless-code/verdict`. Use one route per machine, or the skill loads twice. The registry route installs only the skill, so install the binary first: `install.sh` is not shipped in the npm tarball. After `verdict upgrade`, re-run the install to refresh the copy.

### 3. Review a PR

You need [`gh`](https://cli.github.com) signed in with `repo` scope, and `git`. Run `verdict doctor` to check.

```bash
verdict fetch owner/repo 123            # writes .verdict/state/123/ (diff, metadata, threads)
# ask your agent to review PR 123; it reads .verdict/state/123/
verdict post owner/repo 123 --dry-run   # validate anchors, post nothing
verdict post owner/repo 123             # post exactly one review
```

No PR yet? Review your working tree instead. This never posts.

```bash
verdict fetch-local owner/repo work     # writes .verdict/state/local-work/
# ask your agent to review local-work
verdict record owner/repo local-work    # validate anchors, save the findings
```

## Install

| You want                   | Run                                                                                                                                               |
| -------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------- |
| Try it once                | `npx @stainless-code/verdict --help` or `bunx @stainless-code/verdict --help`                                                                     |
| Pin a version (npm)        | `npm i -g @stainless-code/verdict@X.Y.Z`                                                                                                          |
| Pin a version (shell)      | `curl -fsSL https://stainless-code.com/verdict/install.sh \| VERDICT_VERSION=vX.Y.Z sh`                                                           |
| Pin a version (PowerShell) | `$env:VERDICT_VERSION = 'vX.Y.Z'; irm https://stainless-code.com/verdict/install.ps1 \| iex`                                                      |
| Update                     | `verdict upgrade` (add `--dry-run` to preview)                                                                                                    |
| Uninstall                  | `verdict uninstall --remove-path`, or the owning manager: `npm uninstall -g @stainless-code/verdict`, `brew uninstall stainless-code/tap/verdict` |

The shell installer puts `verdict` in `~/.verdict/bin`. `VERDICT_INSTALL` picks another directory. It adds that directory to your PATH only if it is missing. `--no-modify-path` skips the edit.

`verdict upgrade` detects how you installed and runs the matching update. It never runs in the background.

If the docs site is down, both installers are also release assets: `https://github.com/stainless-code/verdict/releases/latest/download/install.sh` and `install.ps1`.

### Verify the download

Releases are built from tagged commits, and each release publishes `commit.txt` with the commit its signature was minted for. `checksums.txt` is signed with cosign (keyless), and the installers verify the signature — identity, issuer, and the workflow-SHA pin to that commit — when `cosign` is on your PATH, then the archive SHA-256 always. A release without `commit.txt` is refused rather than installed unpinned. Set `VERDICT_REQUIRE_SIGNATURE=1` to fail instead of falling back to checksum-only. To check by hand, use the `cosign verify-blob` command in [Release train](CONTRIBUTING.md#release-train).

### CI

[`setup-verdict`](setup-verdict/action.yml) installs a pinned, verified binary on Ubuntu, macOS, and Windows runners.

```yaml
- uses: stainless-code/verdict/setup-verdict@vX.Y.Z # pin a release tag or commit
  id: verdict
  with:
    version: vX.Y.Z
- run: ${{ steps.verdict.outputs.path }} --version
```

Inputs are `version` (default `latest`) and `require-signature` (default `true`). Outputs are `path` and `version`. A full workflow is in [`examples/verdict-review.yml`](examples/verdict-review.yml) and the [CI recipe](docs/ci-recipe.md).

## Use it with your agent

- **Skill and CLI.** The skill from step 2 drives the CLI. It works with Claude Code, Cursor, Codex, OpenCode, Copilot, Gemini, Goose, Qwen, Windsurf, and any agent that reads an `agents` skills directory.
- **MCP.** `verdict mcp` serves the review verbs as tools over stdio. `verdict mcp config --agent claude` prints the config entry, and `--write` merges it into the file. It supports `claude`, `cursor`, and `codex`, and is experimental until 1.0. See the [manual](docs/manual.md#mcp-server-stdio-experimental-until-10).
- **Go SDK.** [`pkg/verdict`](pkg/verdict) runs the verbs in-process for Go programs, with typed requests and responses — no `verdict` subprocess, no `--json` parsing.
- **TypeScript client.** `import { createClient } from "@stainless-code/verdict/client"`. It ships in the npm package. See [`clients/ts`](clients/ts/README.md).
- **Other languages.** Every `--json` envelope and store file has a JSON Schema in [`skills/verdict/schemas/`](skills/verdict/schemas). The [Build a client guide](https://stainless-code.com/verdict/build-a-client) covers exit codes and codegen, with a tested [Python example](examples/python-client/).

## Hosts

`fetch` works on all six hosts. Posting works on GitHub only.

| Host      | `--provider` | Fetch uses                                                 | Post | Incremental re-review (`--base-sha`) |
| --------- | ------------ | ---------------------------------------------------------- | ---- | ------------------------------------ |
| GitHub    | `github`     | `gh`                                                       | yes  | yes                                  |
| GitLab    | `gitlab`     | `glab` (or `GITLAB_TOKEN`)                                 | no   | no                                   |
| GitCafe   | `gitcafe`    | `cafe` (or `CAFE_TOKEN`)                                   | no   | no                                   |
| Forgejo   | `forgejo`    | `curl` with `FORGEJO_HOST` and `FORGEJO_TOKEN`             | no   | no                                   |
| Bitbucket | `bitbucket`  | `curl` with `BITBUCKET_TOKEN`                              | no   | no                                   |
| Azure     | `azure`      | `az` (login or `AZURE_DEVOPS_EXT_PAT`, `AZURE_DEVOPS_ORG`) | no   | no                                   |

On the other five hosts, `record`, `report`, `show`, `next`, `triage`, and `threads` work on the fetched bundle. `post`, live `calibrate`, `ready`, and `adopt` refuse with a typed error; `calibrate --probe` runs offline on any bundle. Details and per-host gaps are in the [manual](docs/manual.md#providers-non-github-hosts).

## Security

- **The skill runs with your privileges.** `verify` runs the agent's `verifyCmd` through `bash -c` with a 300 second limit. A pre-exec gate refuses destructive, networked, credential-reading, and repo-escaping commands before anything spawns. The gate is a tripwire, not a sandbox. For a hostile PR, run the verify step in a sandbox: see the [recipe](docs/manual.md#hostile-pr-sandbox-recipe-opt-in-not-enforced).
- **Review controls come from the base commit.** A PR cannot edit the panel, budget, or knowledge-base rules it is judged by. See [Review controls are base-owned](docs/manual.md#review-controls-are-base-owned).
- **Diffs and comments are untrusted input.** The prompts treat them as evidence, never as instructions.
- **Secrets stay local.** `GH_TOKEN` never leaves your machine, and provider tokens go only to their own hosts. Nothing phones home: no telemetry, no background updater.

Report vulnerabilities through [`SECURITY.md`](SECURITY.md).

## Docs

- [Docs site](https://stainless-code.com/verdict): quickstart, how it reviews, comparison, glossary.
- [`docs/manual.md`](docs/manual.md): the operator manual. Every verb, flag, store file, and exit code.
- [`docs/vision.md`](docs/vision.md): what Verdict is and where its craft comes from.
- [`CHANGELOG.md`](CHANGELOG.md): release notes.

## Contributing

Build from source, run the evals, and ship a change: see [`CONTRIBUTING.md`](CONTRIBUTING.md).

## License

MIT. See [LICENSE](LICENSE).
