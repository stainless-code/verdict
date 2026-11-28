# @stainless-code/verdict

## 0.8.0

### Minor Changes

- 4308eea: **Breaking:** verify receipts now bind the content a command ran on, not HEAD plus a clean working tree.

  - **The content.** The paths in your index (tracked and `git add -N` files) with their working-tree bytes, cleaned the way `git add` would commit them. Uncommitted edits count; untracked and ignored files do not. Git's built-in line-ending rules (`core.autocrlf`, `core.eol`, the `text`/`eol`/`binary` attributes) apply, so a CRLF checkout of an LF commit, Git for Windows' default, names that commit's tree.
  - **The snapshot.** `verdict verify` copies that content into a private directory outside your checkout, names it as a git tree, re-reads it to prove it, and runs the command there, never in your checkout. The snapshot holds the committed form: LF where the commit has LF, exact bytes for `binary` and `-text` files, and `ident` or `working-tree-encoding` paths as git's own checkout writes them. It is a clean, CI-like checkout: ignored dependencies and local config are absent unless listed in `verify.share`, and submodules are empty, so install dependencies in the command or share them. `verify` warns naming what it left out. A path that resolves outside the snapshot (an absolute path, or a symlink in the tree) reaches the real filesystem, read-only under `--sandbox`. The snapshot is removed afterwards, and a later run sweeps any that a killed run left.
  - **No checkout code runs.** Every git call runs with hooks and fsmonitor off and every filter driver (clean, smudge and process, git-lfs included) switched off. A smudged Git LFS file therefore names different content from the commit that stores its pointer: `verify` warns naming LFS and the paths, and `verify --group` refuses `head-mismatch` naming LFS.
  - **What `record` and `post` compare.** The receipt's `tree` against the graded content: for a local bundle, the `content_tree` that `fetch-local` now records in `source.json` (the bundle's diff is read from that tree); for a PR bundle, the head commit's tree. A `--staged` bundle records no tree, because `verify` never runs the index.
  - **The local fix loop needs no commit:** patch, re-run `fetch-local`, run `verdict verify`, then `record`.
  - **Removed.** The in-place fallback: a checkout `verify` cannot snapshot refuses `verify-failed`, as does `verify` outside a git checkout. Receipts carry `tree` instead of `head` (`validation-<group>.json` replaces `execution_head`) and are `schema_version` `"2"`. v1 receipts are not read, so re-run `verdict verify` after upgrading. `verify --group` requires the snapshot to equal the reviewed commit's tree. A receipt that says `passed` but fails its own evidence reads as `deep validation contradictory`. A `verification.json` claim is `{sig, verdict, verifyCmd}`; `resultHead` is no longer read.

- cfb1651: `verdict demo` now accepts `--timeout <secs>` in both spellings, defaulting to the same 300 s the `run` verb gives a launch. The `agent-timeout` refusal and its `try:` line tell the operator to raise `--timeout`, but `demo` hard-coded the budget and took no flag, so the advice named something the verb did not have. The value rides to the run service's existing validator, so a non-positive or non-numeric value refuses `bad-timeout` the same way `run --timeout` does.
- ecc6b89: New `verdict drain` answers the review threads waiting on a reply. It refreshes the threads, plans the ones that need a reply, and pins the plan with a digest. `drain --agent <name>` launches one agent through the same launcher as `run` and checks its `drain.replies.json` against the plan.
- 6923d0e: `drain --post` answers the waiting review threads from a validated `drain.replies.json`. GitHub and GitLab post one marked reply per draft and append `replies.json` under a lock after each write; `withdraw` also resolves its thread. Secret-shaped bodies refuse before any write, stale threads are skipped, and a failed write records `failed[]` and refuses `drain-incomplete` so the same command can finish the remaining replies.
- 599bd7e: **Breaking:** `REVIEW_PUBLISH_ATTRIBUTION=true|false` is no longer accepted (it warned and mapped to `product`/`off`), and a boolean `publish.attribution` in the settings file now refuses with the generic string-type message. Migrate to `off|product|full`.
- 1e609cf: **Breaking:** `verdict ingest --sarif <file|->`, the older spelling of `--from sarif <file|->`, is removed and now fails as an unknown flag. The MCP `ingest` tool and the Go SDK's `IngestRequest.From` are now required; both used to treat an empty value as `sarif`, and an empty one now refuses `missing-sarif`. `bench/runner` drops `-runs`, the older spelling of `-repeats`. Migration: write `ingest --from sarif <file|->`, pass `from: "sarif"` to MCP `ingest` and `From: verdict.SourceSARIF` to the SDK's `Ingest`, and use `-repeats` with the bench runner.
- 6b51123: **Breaking:** verdict no longer reads bundles written by older versions; it refuses them with the new `bundle-outdated` code, whose message names the fix, instead of degrading.

  - A `run.json` without `requires_evidence` and `inputs_sha256` (written before the integrity binding) refuses on every read: `post`, `record`, `report`, `ready`, `triage`, `verify --group`, `config --explain`, and `run`. It was previously enforced as unverified policy with a "pre-integrity bundle" warning.
  - `config --preview` on a bundle directory without `run.json` refuses instead of guessing the file list from `.difffiles`. With no bundle directory at all it still previews.
  - A `findings.json` whose `verification` is the bare array of panel results (0.7.x) refuses instead of having its panel read.
  - A corpus bundle without `corpus_run.json` refuses in `calibrate` and `trend` instead of scoring as an independent k=1 sample. `bench/runner`'s loop mode now writes `corpus_run.json` for its final bundle too.

  Migration: re-fetch bundles (`verdict fetch`, `verdict fetch-local`) and re-run reviews and benchmarks created before 0.8.0.

- 6ad7f55: **Behavior change:** `gate --max-severity <tier>` now fails when an open finding at the tier lacks a confirmed cross-check, whether the two-finder execution is live, finished or gone; the evidence dimension names the count. Before, a finding the panel left pending passed once the execution's artifacts were gone.

  A re-fetched bundle keeps the requirement: a `record.json` or `post.json` receipt for the current head that names a finder summary decides it, even when one finder failed, and it outranks a configured `finders: 1` default. A receipt bound to an earlier head no longer makes a clean review of the new head insufficient. `run --finders 1` now always records `finders: 1` in `run.json`, so a single-finder capture gates as the manual finding it is.

- 3e2278e: **Breaking:** `gate` is the only gating verb. `report` is the advisory read: `--check`, `--completeness`, `--max-risk` and `--live` are unknown flags on it (usage exit 2), and `report --json` drops `data.gate` and `data.completeness`. The Go SDK's `ReportRequest`, the MCP `report` tool and the TypeScript client drop the same fields. `gate` gains `--max-risk low|medium|high|critical` and `--category <cat>`, and its `head` dimension names the cause (`missing-post`, `corrupt-post`, `stale-head`) and the re-fetch command.

  | Before                                      | Now                                           |
  | ------------------------------------------- | --------------------------------------------- |
  | `report --check <tier>`                     | `gate --max-severity <tier>`                  |
  | `report --check <tier> --category <cat>`    | `gate --max-severity <tier> --category <cat>` |
  | `report --completeness`                     | `gate --require-head`                         |
  | `report --max-risk <level>`                 | `gate --max-risk <level> --live skip`         |
  | `report --live <auto\|strict\|skip>`        | `gate --live <auto\|strict\|skip>`            |
  | `report --json` → `data.gate.passed`        | `gate --json` → `data.passed`                 |
  | `report --json` → `data.gate.dimensions[]`  | `gate --json` → `data.dimensions[]`           |
  | `report --json` → `data.completeness`       | `gate --json` → the `head` dimension          |
  | Go SDK `ReportRequest{Check, MaxRisk, ...}` | `GateRequest{MaxSeverity, MaxRisk, ...}`      |

  `gate` always grades the stored coverage receipt, which `report` never did, so a review without attested coverage fails `gate` where `report --check` passed: add `--coverage warn` until it attests. `gate` prints one `gate: <dimension>` line each instead of `check:` lines. Workflows from `verdict init --ci` already use `gate`.

- 096437b: Every git call verdict makes now runs no code the reviewed checkout chooses; the one exception is your own clean and process filters, which still run when verdict reads or stages your own working tree. Each call names its repository with `-C`, drops inherited `GIT_DIR`, `GIT_INDEX_FILE` and git's other location variables (plus `GIT_EXEC_PATH` and `GIT_CONFIG_PARAMETERS`), and runs with hooks and fsmonitor off; every diff, log and show adds `--no-ext-diff --no-textconv`. Before, an in-tree `core.hooksPath` such as `.husky/`, an fsmonitor, a textconv or external diff driver, or a git-lfs smudge could run during `fetch-local`, `fix --test --commit` or a `backtest` replay.

  **Breaking:** `fix --test --commit` no longer runs commit hooks, neither `.git/hooks` nor a hooks path the tree sets. The commit carries reviewed content, so run your hooks yourself before you push. Your clean and process filters still run for that commit, so under a git-lfs pattern it holds the LFS pointer.

  `fetch-local --worktree` and the `backtest` replay checkout now switch off every filter driver, so each file lands as its blob bytes. Content naming for `verify` and `fetch-local` runs no filter at all (see the content-bound receipts entry).

- 7425418: **Breaking:** every `gh` and `glab` call for a target resolved from a URL or the `origin` remote now names that target's host (`gh api --hostname`, `gh pr … --repo <host>/<owner>/<repo>`, `glab api --hostname`, `glab mr … -R https://<host>/<path>`). The one exception is a GitLab host on a non-default port, which glab cannot pin: it runs unpinned only when the environment provably names that exact host and port (see the ported-GitLab entry). `GH_HOST`, `GITLAB_HOST` or the CLI's default host can no longer send a fetch, reply or review to another host's pull request with the same number, and `post` always posts to the host its bundle came from.

  When a call cannot be pinned, verdict refuses `host-mismatch` (exit 1) naming both hosts. For example: a Forgejo target on another host than `FORGEJO_HOST`; GitHub on a non-default port; a ported GitLab host the environment does not select; a GitLab target while `GITLAB_API_HOST` or `CI_SERVER_FQDN` names another server; or a bundle fetched from another host than the target. Two hosts that differ only by port are different hosts. If `post` refuses `host-mismatch`, re-fetch the target or pass a target on the bundle's host. A typed `<owner/repo> <pr>` names no host, so its reads still follow the host CLI's default. See `docs/manual.md` § Targets.

- 9cf0969: `verdict run --agent claude` now writes its review without a manual permission flag. Every claude launch carries one grant scoped to that launch's own bundle directory (`--allowedTools=Edit(./.verdict/state/<bundle>/**)`), so it can write `review.json` there. The grant is an added allow rule: it removes nothing your own Claude settings allow. The grant is built from the resolved directory and never widens past it: a symlinked `.verdict`, `.verdict/state` or bundle directory is refused (`bad-config`), and a path the rule syntax cannot carry launches without the grant and with a warning. A bundle holding a symlink, a non-regular entry, or (on unix) a hard-linked file anywhere refuses the new `artifact-path-unsafe` before any agent starts, since the grant covers the whole bundle; a checked-in `review.json -> ../src/main.go` or `cost.json` link cannot aim verdict's grant at source, and the artifact is read back without following links. Your own permission flag after `--` (`--permission-mode`, `--dangerously-skip-permissions`, `--allowedTools`, `--add-dir`, `--tools`) drops the grant. The Bash/PowerShell denial on review launches is now unconditional: only `--allow-shell` removes it, whatever you pass after `--` (claude's `--tools`, pi's `--tools`/`--no-tools`/`--tools +name` included). The flag the denial itself uses — pi's `--exclude-tools`/`-xt`, bob's `--disable-tool-groups` — is verdict's on review launches: passing it after `--` in any spelling refuses `bad-flags`. The grant is defense in depth: the boundary is the disposable review snapshot every review launch runs in. The `no-review-artifact` hint no longer suggests the broader `--permission-mode acceptEdits` for claude, and `init --ci` workflows name the grant in their comment.

  `verdict loop` now works with stock agent setups. Its fix spawn carries the agent's documented edit grant (claude `--permission-mode acceptEdits` with Bash and PowerShell still denied, codex `--sandbox workspace-write`, none for other agents). `verdict loop … -- <agent args>` passes arguments to every spawn after that grant; since they also reach the review of untrusted content, they are an allowlist: only the agent's model flag with one value, spaced (`-- --model opus`, or a documented alias such as codex's `-m`); anything else, the `=` form and an empty argument included, refuses `bad-flags`. `--print` shows each grant in the exact argv. Claude's review carries its scoped write grant and codex's carries `--sandbox workspace-write` inside the review snapshot. **Breaking (pre-1.0):** loop's verify step, which runs the fix agent's own command, is now sandboxed by default (as `verify --sandbox`); a host without the sandbox refuses `sandbox-unavailable`. Pass `--sandbox-network-only` or the new `--no-sandbox` (which warns) to opt out; `--sandbox` still works and is now a confirmation. New `verdict loop --max-severity critical|important|informational` (default `important`) has `gate`'s meaning, so an informational finding no longer holds the loop; `--max-severity informational` restores the strict gate.

  The review handoff (`procedure.md` and each finder's procedure) now asks the agent for the `coverage[]` attestation the default `gate` requires (`[]` when nothing changed; `chunks[]` on a chunked bundle), and the two-finder union keeps an empty attestation. Before, every `run --agent` review — including the workflows `init --ci` generates — failed the default gate on coverage, and `verdict loop` on a clean tree could never pass. Loop's stop line also names a refusal's code once instead of twice.

  `verdict run --require-coverage` carries `record --require-coverage` into the launch: a review missing its coverage attestation gets a repair round naming the new `coverage-incomplete` problem, and a run that ends with only that gap refuses `coverage-incomplete` as record would. On a chunked bundle a `chunks[]` that names no chunk (absent, `false`, or empty) is the gap, for `record --require-coverage` and `post --require-coverage` too. With two finders the union's attestation is what counts: it is checked before any cross-check runs, and `--resume` seals the flag. The loop's review step passes it, so a review that omits `coverage[]` is repaired instead of stopping the loop at record.

- e937ed8: verdict now measures agent spend from the agent CLI's own output instead of the agent's self-report. Each spawn that writes a spend receipt (the self-review loop's review and fix, and each two-finder `run` launch) asks for structured output and reads the cost from it:

  - `claude`: `--output-format json`, priced from `total_cost_usd`.
  - `opencode`: `--format json`, the sum of each step's `cost`.
  - `codex`: `--json`, the last turn's tokens priced from a dated, cited price table, for the model Codex itself would pick (`--model`, `-c`, the profile, `config.toml`). Conflicting sources, an unreadable model, or a local or non-OpenAI provider leave the spawn unpriced.

  An unpriced spawn falls back to the agent's self-report, then to the median of past measured spawns of the same agent and model (`projected`), and only then counts as unknown. A cost ceiling still stops on unknown spend. Each receipt records `priced_by`, `agent` and `model`, and a two-finder receipt is settled to the measured total, so `loop --max-cost-usd` and the monthly ceiling count real spend. A truncated or malformed stream measures nothing, and an older CLI that rejects the output flag is retried once without it.

- 18bd91f: **Breaking:** `verdict mcp`, `verdict serve` and `verdict lsp` share one write-posture rule, and `--read-only` is the only switch.

  - **stdio (`mcp`, `lsp`) has CLI parity.** Local writes (triage, KB promotion, sandboxed `verify`, `record`, `adopt`, `fix`) and host writes (`post`, resolving threads) are on by default. `--read-only` removes every write. `lsp` has no write path.
  - **`serve` writes only on loopback.** Local writes are on by default behind its token, session-cookie, CSRF, Host and Origin checks. Host posting needs `--allow-post`, with its preview, nonce and digest flow. `--read-only` removes every write and refuses `--allow-post`.
  - **A non-loopback `--host` serves read-only,** warns why, and refuses `--allow-post`. No flag turns remote writes on, because the token would cross plain HTTP.
  - A wildcard bind no longer allowlists its own spelling (`0.0.0.0`, `::`) as a Host or Origin, and prints a start URL on `127.0.0.1` or `::1`.

  The removed flags are unknown-flag usage errors. `mcp config` no longer treats a `["mcp", "--operator"]` entry as its own (`mcp-config-foreign`): edit or delete it, then re-run `--write`. An agent's MCP server that must not call `triage` should start with `--read-only`.

  | Before                                | Now                                                |
  | ------------------------------------- | -------------------------------------------------- |
  | `mcp --operator`                      | `mcp` (add `--read-only` to remove every write)    |
  | `mcp config --operator`               | `mcp config`                                       |
  | `serve --interactive`                 | `serve`                                            |
  | `serve --interactive --allow-post`    | `serve --allow-post`                               |
  | `serve` (read-only)                   | `serve --read-only`                                |
  | `serve --interactive --host <lan-ip>` | read-only, no replacement; bind loopback to triage |

- c8453b7: `report --policy <file>` replays a stored bundle under an alternative policy: it derives the gate and approval twice — once under the captured policy, once under the candidate — from the same findings, checks, and receipts, and prints both decisions plus the dimensions that changed. The candidate is validated with the config schema and resolver. The replay lists every dimension the candidate added, removed, or moved, writes nothing, calls no host, exits 0 whatever the candidate decides, and therefore refuses the gate flags rather than swallow their exit code.
- 3172f63: `verdict pr-wait` (and `ready --wait-for build`) now work on GitLab merge requests, using the same `glab` reads as `ready`: state, head, the head pipeline as the check rollup, and approvals and discussions for `review-changed`. A GitLab wait counts the head pipeline only when it ran for the current head (merged-results and merge-train pipelines included), and a `locked` merge request reads as open, not closed.

  One per-host table now drives `ready`, `pr-wait`, the advertised `--provider` list and the refusal. **Behavior change:** `pr-wait --provider forgejo`, which never worked, now refuses `provider-unimplemented` up front, naming the supported hosts (github, gitlab), instead of failing inside the first poll.

- b898d7f: **Security:** every review launch now also runs in the host's OS sandbox (STA-538). `verdict run --agent` finders, cross-checks, repair rounds and drain, and `verdict loop`'s review step, run under `sandbox-exec` with a generated profile on macOS and under `bwrap` on Linux, with the network open so the agent reaches its model API. A review agent can write only its launch's snapshot directory (its working copy and private `TMPDIR`), its own cited data paths (`claude` alone: its debug log, its login's token refresh, its CLI cache, the macOS login keychain, and its launch's own project directory; `opencode`, `codex` and `pi` get throwaway homes instead, below; each relocation variable honored), and the devices a process needs — never its own config or code (`~/.claude.json`, claude's settings, hooks and plugins, `~/.config/opencode` and opencode's installed packages, pi's settings and extensions), no other dotfile in `HOME`, nothing else under `~/.config`, never your checkout, the execution's shared snapshot, or another launch's snapshot directory (which macOS also hides from its reads) — and cannot read the credential table `verify --sandbox` hides, which now also covers glab's `~/.config/glab-cli` and `GLAB_CONFIG_DIR` (for `verify --sandbox` too). Before, an agent tool that took an absolute path could write anywhere you can and read your credentials into review text. `codex` keeps its own `--sandbox workspace-write` sandbox instead, because a nested Seatbelt profile fails on macOS; it confines its writes, but its reads are not confined, and `run` warns so. With no sandbox (Windows, Linux without bubblewrap, a probe that fails), `claude` and `codex` still run with a warning that their reads are not confined, and every other agent refuses the new `review-sandbox-unavailable` code before it starts; `--allow-unsandboxed-review` on `run` and `loop` runs every review launch without the sandbox, warned: without it only `claude` (while your own Claude settings allow no write tool, which the opt-out does not check) and `codex` are kept in their copy by their own tools, and the opt-out accepts that any other agent can write outside its copy (a sibling launch's copy, your checkout, your files) and read your credentials. A row with no state paths can write only its snapshot and `TMPDIR`, and `run` warns so; an agent you define in prefs declares its own writable data paths as `agents.<name>.state_locations`. `run --json` reports the posture in `data.sandbox` (`--print` adds the wrapper argv with a `{snapshot}` placeholder), and `loop --print` carries it on each review step in `plan[].sandbox`. A sandboxed launch runs the agent binary verdict resolved by its absolute path. The loop's fix spawn is never sandboxed: editing your checkout is its job. The sandbox also closes every unix socket (an ssh agent, docker, systemd, tmux; on macOS the resolver's and syslog's stay open), hides the host temp roots (and `/run` on Linux), strips `SSH_AUTH_SOCK`, `DBUS_SESSION_BUS_ADDRESS`, `GPG_AGENT_INFO`, `TMUX`, `NVIM`, `XDG_RUNTIME_DIR`, `DISPLAY`, `WAYLAND_DISPLAY` and `XAUTHORITY`, and on macOS denies every Mach service lookup but an allowlist (directory lookups, trustd, SystemConfiguration, and the keychain brokers only for claude, whose login they hold) — so no preference write, pasteboard, LaunchServices registration, login item, window server or Shortcuts; a TLS client that loads its roots from the keychain gets `SSL_CERT_FILE=/etc/ssl/cert.pem` unless you set one. On Linux `/var/tmp` is hidden too; the network namespace stays the host's, so abstract-namespace unix sockets (a `dbus-launch` session bus) and sockets under `HOME` outside the credential table stay reachable. Each writable data directory is pinned so it cannot be swapped for a link, a state path that is or passes through a link refuses, and the state is re-checked before every launch. writable agent state is now only what nothing trusts later: opencode runs with its data and state homes in its launch's wrapper, seeded with copies of its logins, and its real ones read-only (its shadow git repositories would otherwise carry a planted `core.fsmonitor` into your next session); claude may write only its launch's own `~/.claude/projects/<slug>`, made before and removed after, with its memory read-only, and its sessions, todos, file history, backups and caches are read-only; codex and pi review launches — sandboxed or not — run with a throwaway `CODEX_HOME` and `PI_CODING_AGENT_DIR` in their private `TMPDIR`, seeded with no-follow copies of their login and model configuration (pi's settings cut to its model and network keys, so no extension, package, skill, or command setting loads), and all of `~/.codex` and `~/.pi` is read-only (pi's `auth.json` runs `!command` values; codex's cache, scratch and `tmp/arg0*` shims are trusted by later sessions); they also carry `--ephemeral` and `--no-session`; no row but claude writes anything under your real `HOME`; a relocation variable naming `HOME` itself refuses; every read-only path is denied writes explicitly. The credential table now also hides the agent logins (`~/.codex/auth.json`, opencode's and pi's `auth.json`, `~/.claude/.credentials.json`, `~/.config/github-copilot`, `~/.gemini/oauth_creds.json`, `~/.config/git/credentials`, opencode's `mcp-auth.json`), the X11 cookie and the 1Password, Bitwarden and Secretive ssh-agent socket directories, each agent keeping its own, for `verify --sandbox` too. A relocation root that is a link, and a state path inside a credential directory, refuse. A policy that cannot be built refuses `review-sandbox-unavailable` instead of falling back unsandboxed; claude review launches pin `--permission-mode default`, and any write-tool allow rule in your own Claude settings voids claude's no-sandbox fallback; codex's `-s<mode>`, `-c`/`--config`, `-p`/`--profile`, `--enable`/`--disable`, `--remote`, `--trust` and `-o` count as deciding its sandbox, codex's own sandbox gets `-c sandbox_workspace_write.writable_roots=[]`, and a codex wrapped in verdict's macOS sandbox is told to pass `--sandbox danger-full-access`. pi review launches carry `--no-approve`, so the reviewed tree's project-local pi files never load. `backtest` replays are now sandboxed like every other review: every commander verdict runs is, unless a test double says it simulates its processes. Review launches point claude's own scratch directory (`CLAUDE_CODE_TMPDIR`) at their private `TMPDIR`. A review launch can no longer push input into your terminal (`TIOCSTI`): it starts in a session of its own with no controlling terminal, and the macOS profile denies `/dev/tty` and `/dev/ttys*`. codex's throwaway `CODEX_HOME` gets only the model, provider, and connection keys of your `config.toml`, so your MCP servers, `notify` and hook commands, profiles, and sandbox settings never start or apply in a review; `--ignore-user-config` after `--` is verdict's on a review launch. Review sandbox traversal checks recognize both Windows separators, so a relative path written with `/` on Windows is checked the same as one written with `\`.
- 012b06f: **Security:** every review launch now runs in a disposable copy of the reviewed tree, never in your checkout (STA-537). `verdict run --agent` finders, cross-checks, repair rounds and drain, and `verdict loop`'s review step, each start in their own copy of the content-bound snapshot `verify` takes, with a copy of the bundle at its usual `.verdict/state/<bundle>/` path. When the agent exits, verdict copies back only the artifacts that launch is expected to write (`review.json` and, beside a bundle-level review, `cost.json`, `chunks/chunk-*.cost.json`, `validation.json`, `verification.json`; a finder's or cross-check's own artifact; `drain.replies.json`), each a lone regular file of at most 32 MiB, written atomically with the agent's own timestamp. Anything else the agent wrote inside the copy's wrapper is discarded and named in a warning. A write beyond the wrapper (via `..` or an absolute path) is not discarded by the copy: the OS review sandbox (STA-538) blocks it on macOS and Linux, and `claude` (without `--allow-shell`) and `codex` stay in their copy through their own tools; nothing blocks it for another agent on Windows, which has no OS sandbox verdict can apply, or under `--allow-unsandboxed-review`, and the sandbox's own residuals are tracked in STA-539 (abstract-namespace unix sockets on Linux) and STA-540 (loopback services and the other residuals of its final review). The copy holds nothing that leads back to your repository: the snapshot drops its worktree pointer and registration as soon as it is made, each copy's `.git` is an empty repository of its own, and no symlink, `.git`, or `.verdict/state` of the tree's (in any case) is copied. Outside a git checkout the copy holds the bundle alone. Each launch gets a private `TMPDIR`; the agent's environment drops the variables that name your directories (`OLDPWD`, `INIT_CWD`, `GIT_DIR` and git's other location and `GIT_CONFIG_*` variables, and the like). A checkout inside the temp directory keeps its snapshots under the user cache directory instead (`XDG_CACHE_HOME`), which must lie outside both the checkout and the temp directory, refusing otherwise. Copy-back pins both directories and reads each artifact as the very file the walk saw — never through a link, never waiting on a FIFO, refusing a hard link — and a chunk's cost record comes back only for a chunk the manifest lists (1 MiB cap). The shared snapshot the copies are made from is symlink-free, owner-only read-only, and fingerprinted (path, mode, size, modification time, plus inode and change time on unix): a launch that changed it refuses every later copy; on Windows a same-size rewrite that restores the modification time is not detected. Review agents still read the reviewed tree and use the network. Every `claude` review launch, `--allow-shell` or not, adds `--setting-sources=user` and `--strict-mcp-config`, so the reviewed tree's own `.claude/settings*.json` (hooks, allow rules) and `.mcp.json` never load; after `--`, claude's `--settings`/`--setting-sources`/`--plugin-dir`/`--mcp-config`/`--strict-mcp-config` and codex's `-C`/`--cd`/`--worktree`/`--approve-for-me`/`--dangerously-bypass-hook-trust` now refuse `bad-flags` on a review launch unless `--allow-shell` is set. `codex` review launches add `--sandbox workspace-write -c sandbox_workspace_write.exclude_slash_tmp=true`, scoped to the snapshot, so codex writes `review.json` itself instead of relying on stdout salvage (`--print` renders the command without it, and your own `--sandbox` after `--` drops it). The loop's fix phase still edits your checkout. A snapshot that cannot be made refuses `agent-spawn-failed` before any agent starts.
- 9c68ea2: `run --agent` now supports fourteen verified official agent CLIs, including `agy`, `amp`, `copilot`, `crush`, `droid`, `gemini`, `goose`, `kiro`, `pi`, and `qwen`, with prompt flags placed immediately before the directive for agents that take it as a flag value. Operators can define more agents under the new `agents` key of their own settings file; the shape is prefs-only, closed, and refuses name collisions and invalid binaries as `bad-settings`.
- ea18e58: Two-finder `run` now repairs a cross check instead of failing it blind: the cross directive names the accepted quote sides (`RIGHT` for the new side of the diff, `LEFT` for a deleted line), and one repair round carries every problem the checker found — the row set, each row's fields, and each quote against the diff, with JSONPath, code, and fix — so an agent that wrote `side: "new"` fixes it on the next round. A cross quote's `side` is now required and refused when absent or empty by the cross check and by every reader of `verification/verdicts.json`; pre-1.0, a receipt that relied on the old empty-side default no longer verifies, so re-run the execution. An execution left incomplete names each failed phase and, when `--resume` can continue it, that recovery. `gate` over an execution that stopped short fails in every mode, reads `findings` as `unknown` instead of "no open findings", and names `run --resume`, then `record`. A `review-insufficient` refusal that lists no evidence problems no longer points at gaps "named above", and the SDK's `RefusalError` now carries `Problems`. An execution that stopped short in a way `--resume` refuses reads `unknown` as well, and its recovery is a fresh `run`, naming the gap. `run`'s `re-enter:` line quotes every operand, keeps the flag names after `--` but redacts every value (model and budget values included), scrubs credentials, adds a shell comment asking for redacted values back, carries `--resume` before `--` when the refusal says to resume, and drops it after `resume-drift` or `cannot-resume`.
- 6578b8b: New in the repository (not the binary): `contrib/scanner-evidence/`, a pilot adapter you run yourself to add Opengrep candidates to a bundle. `python3 contrib/scanner-evidence/scan.py <bundle> <checkout> <head-sha>` scans a clean checkout at the bundle's head with the shipped starter rules only (Opengrep 2.0.0, no registry rules, metrics and version checks off), and writes `x-scanner.json` and `x-scanner.md` into the bundle. Hand the Markdown to the review, or pass it as `--context`. A match is a candidate, never `reproduced`, and a clean scan never means safe. A missing or wrong-version tool, a dirty tree, a scanner error, a limit hit or an unscanned changed file writes `status: incomplete` and exits 1. Evidence records rule ids, locations and hunk association, never source text. Opengrep is LGPL-2.1, installed separately and never bundled.
- cbeb135: New `verdict dashboard`: a read-only terminal view of your reviews, over the same query layer `serve` uses. **Overview** (`1`) shows portfolio totals, the served roots and every bundle that could not be read; **Queue** (`2`) shows one row per bundle with its counts, model and head. A withheld total shows `—`, never a smaller number. The live view needs a terminal (else it refuses `dashboard-no-tty`, or `dashboard-no-vt` on a Windows console without VT support). `q` or `Esc` quits, Ctrl-C exits 3, and every handled exit restores the screen; after a SIGKILL, run `reset`. `--once` prints one plain-text frame, and `--root` reads every repository below a directory. `NO_COLOR` and `--color never` drop color, and `TERM=dumb` switches to ASCII. Text from a PR has its control characters folded to spaces, so it cannot move the cursor or write the clipboard.
- 6829b98: `verdict dashboard` gains five more tabs: **Detail** (`3`: one bundle's gate, counts and findings with severity, status, rigor and quote check), **Diff** (`4`: lanes in risk order with anchored findings marked; `u` toggles side by side), **Compare** (`5`: findings against a baseline picked with `b`), **Cost** (`6`) and **Precision** (`7`). The view refreshes as bundles change (`--interval 2s`, `--sweep 60s`, `--no-watch` to stop), without blocking keys. Keys: `j`/`k`, `g`/`G`, Ctrl-D/Ctrl-U, `Enter`, `Esc`, `/` to search, `r` to refresh, `y` to copy the finding's fix prompt through OSC 52, and `?` for help. `dashboard <owner/repo>` narrows to one repository, `--check` sets the gate tier, and `--once --tab <name>` prints any tab as text.
- 15621f5: `verdict mcp` adds six read-only dashboard tools over the shared query layer (`query-bundles`, `query-findings`, `query-finding`, `query-copy`, `query-cost`, `query-health`) and `verdict://` resources: static reads (`verdict://roots`, `verdict://bundles`, `verdict://health`, `verdict://cost`) and per-bundle templates, each returned as JSON. They stay inside the served root and write, spawn and journal nothing.
- 72106b4: New `verdict lsp` (experimental): a stdio language server (LSP 3.17) that shows a review bundle's open findings as editor diagnostics in any LSP client. Diagnostics update on open, save, a bundle change (`record`, `fetch-local`, `triage`) and the `verdict.refresh` command, never while you type. `critical`, `important`, `informational` and untagged map to Error, Warning, Information and Hint; addressed, triaged and policy-withheld findings never show. It serves the editor's workspace folders, or each `--root` you pass (every root must be inside a git checkout). `--bundle` pins a bundle; by default it uses the newest `local-` bundle, then the newest PR bundle. `--no-watch` turns the watcher off. It makes no model calls and reads no workspace file.
- 221b68b: `fix --test <sig>` promotes a reproduced finding's test into your checkout: it copies the bundle's retained reproduction to `.verdict/tests/<sig>-<hash>.<ext>` and points the receipt command at it. `--commit` (with `--test`) commits only that file as `test: regression for <sig>`, leaving other staged work staged. A target holding different bytes refuses `test-conflict`, a finding with no retained reproduction refuses `no-reproduction`, and a symlinked `.verdict` or `.verdict/tests` refuses. `tests.draft` in `.verdict/config.json` decides whether `record` keeps the reproduction: `on-reproduce` (the default) or `off`.
- 3a8ebe9: Spec checks now tell a missing requirement from a violated one. A `spec_checks[]` row may be `"status": "unmet"`, with a `note` saying what is missing and no `evidence`. `unmet` withholds approval (`unmet spec check: <item>`) and fails the `spec` gate dimension.

  **Behavior change:** a spec source that enumerates nothing now owes exactly one check, the goal it states (its first heading or prose line). Before, a prose-only issue passed every gate while asking for nothing. A change the spec does not ask for is reported as a finding with `category: "accidental-commit"`.

  `n/a` and `unmet` rows carry no evidence (otherwise `spec-evidence-mismatch`) and need a one-line `note`. A missing note is `spec-note-missing` on `unmet` and the `spec-na-unjustified` warning on `n/a` (an error under `--strict`). The `Spec checks` block adds the `unmet` tally. The schema change is additive.

- 84b12ab: An incremental (`fetch --base-sha`/`--since`) delta review is now graded against the full hunks plus the prior round's recorded attestation in `prior-coverage.json`: unchanged ranges stay credited, so the agent attests only the delta, ranges the prior never covered remain gaps, and an absent or unreadable prior credits nothing — grading fails closed against the full hunks. Lines the delta re-modifies lose their prior credit and must be re-attested. Fetching without `--base-sha`/`--since` clears any stale `incremental.diff`. Full-diff rounds are unchanged. `record` persists the attested union on every successful non-chunked run with at least one attested line so the next round can credit it.
- d93e4bf: `verdict serve` adds a diff view with word-level highlights, grouped by change lane. `GET /v1/roots/{root}/bundles/{bundle}/diff` serves the bundle's diff as side-by-side and unified tables with changed words highlighted, unchanged runs collapsed, findings anchored on their exact line and side, and lanes in risk order (`schema`, `source`, `config`, `tests`, `docs`, `generated`, `lockfiles`, `renamed`, `whitespace`). The UI gains a Diff tab, and the static `report --html` export includes the diff.
- 0029a18: `verify --sandbox` on macOS is now a real boundary, not only a network block. The Seatbelt profile denies every Mach service except the read-only user lookup, which closes the keychain, `defaults`, the pasteboard, `open`, AppleEvents and launchd, and the command can no longer signal processes it did not start. It may write only inside the reviewed root, a private per-run `TMPDIR`, and `/dev/null`, `/dev/tty` and `/dev/fd/*`, so it cannot plant a LaunchAgent or a shell rc file. Credential paths and the shared temp roots are unreadable. `GOCACHE` and `XDG_CACHE_HOME` point inside the private `TMPDIR`, so `go build` and `go test` keep working. **Breaking:** other caches under `HOME` (npm, pip, cargo) now fail to write; point them at `$TMPDIR` in the command. The rest of the filesystem stays readable: Seatbelt is a policy, not a mount namespace.

  One credential table now serves Linux and macOS. It applies under the child's `HOME`, verdict's own `HOME` and the account's home, adds `~/.terraform.d`, cargo credentials, `~/.vault-token` and `~/Library/Keychains`, and follows the variables that move a store (`GH_CONFIG_DIR`, `XDG_CONFIG_HOME/gh`, `AWS_SHARED_CREDENTIALS_FILE`, `AWS_CONFIG_FILE`, `DOCKER_CONFIG`, `KUBECONFIG`, `CLOUDSDK_CONFIG`). A reviewed root inside a credential path refuses `sandbox-unavailable`.

  On Linux without bubblewrap, `verify --sandbox-network-only` (MCP `sandboxNetworkOnly`, Go `VerifyRequest.SandboxNetworkOnly`) isolates only the network, through `unshare`. The filesystem, `/proc` and unix sockets such as `docker.sock` stay reachable, every run warns, and the run records `sandboxMechanism: "unshare-net"`, never `sandboxed`. `--sandbox` still refuses rather than downgrade.

  `--sandbox` now protects the repository's git control files on both platforms, because git later runs them outside any sandbox. `.git`, the git directory a `.git` file names, and a linked worktree's common directory are read-only, and on macOS no new `.git` can be created below the root. `git status` and `git diff` keep working; `git commit`, `git stash` and other repository writes fail inside the sandbox. **Breaking (Linux):** bubblewrap can pin only paths that exist, so `--sandbox` refuses `sandbox-unavailable` for a root whose repository it cannot pin read-only: no `.git` (a subdirectory, a tarball, a bare clone), a symlinked or unresolvable `.git`, or a pointer that passes through a symlink inside the root. Run `verify` from the top of a working checkout.

  `sandboxed: true` now means the full sandbox (`bwrap` or `sandbox-exec`) and nothing weaker. `sandboxMechanism` names the wrapper in `patch.json`, `--json` and the Go `VerifyResponse`, and the group receipt records it as `sandbox_mechanism`.

- 27b662c: `fix --recipe <name>` now selects the findings a named recipe owns. `record` stores each finding's optional `lens` provenance, and the batch reads the recipe's lens set from the bundle's base snapshot, selecting only findings whose `lens` matches. A recipe that declares no lenses reports the empty scope instead of widening to every finding.
- b7cdbf3: Retain bounded, redacted reproduction tests and rerun commands in review bundles. Report and show name the artifact, serve offers a download, and unsupported reproduced claims warn.
- bc6f739: A bad `--context` path now refuses `context-invalid` with a path-specific hint instead of `fetch-failed`.

  **Breaking changes**

  A bad `--context` input — missing, symlinked, too deep or too large, or the wrong type — now refuses `context-invalid` instead of `fetch-failed`. Scripts that matched `fetch-failed` for these cases must match `context-invalid`. Bundle-write failures still refuse `fetch-failed`. The refusal names the path as typed and, for a relative path, its absolute resolution, and its hint tells you to check each `--context` path.

- 98d8bec: `run --agent` now offers 32 more cited headless agents, including Cline and Junie after CLI-reference checks. Rows whose binary shares a name with another tool run a harmless identity probe before launch; a non-zero exit, a five-second timeout, or a missing match refuses the new `agent-mismatch` code before any agent starts, and `launcher.json` records `probe: {args, ok}` for a passing check. Operator agents can set `probe_args` and `probe_match` under settings.json `agents`, with `probe_match` required when `probe_args` is present.
- 770b32f: `drain --post --dry-run` runs every post gate and live read, then writes `drain.dryrun.json` with the exact marked replies and typed skips a real post would produce. It makes zero host writes and leaves `replies.json`, `drain.json`, and `threads.json` byte-identical; `--agent --post --dry-run` plans, drafts, then previews. The flag requires `--post` and secret-shaped bodies still refuse.
- 5712cec: **Breaking:** the installed skill is now a small bootstrap skill that only says when to use verdict and how to reach the binary. The binary serves the full Procedure, so the instructions always match the version that checks the work; before, an older installed copy could disagree with the binary grading it.

  Reinstall the skill on every machine. For each installed agent, run `verdict skill status --agent <x>`, delete the old `verdict` skill directory at the path it prints after `->`, then run `verdict skill install --agent <x>` (or `--detect`). `verdict skill uninstall` removes only the new files, so it would leave the old ones behind. For a registry install, delete the old `verdict` skill directory and re-run `npx skills add stainless-code/verdict`.

- 66342c5: Every fetched bundle now carries `procedure.md`, the review Procedure assembled by the binary from its embedded payload. Drain plans carry `drain.procedure.md` for reply drafting. The `run`, stage, and drain directives name those files as authoritative, so an installed skill that is older can no longer displace them. The fetch `next:` hints and `--show-prompts` output point at the bundle Procedure, and provenance receipts record its sha256 as a bundle input. `PromptsHash` now reads the embedded payload, so an installed user gets the real prompt-set hash without a `procedure/` checkout.
- 85b2e97: MCP hosts can now load the Procedure straight from the binary. `prompts/list` and `prompts/get` serve all eight task prompts (`address-reviews`, `build`, `ci`, `fix-ci`, `grade`, `incremental-review`, `review`, `validate`), and `resources/list` and `resources/read` serve every Procedure file at `verdict://skill/<path>`, identical to `verdict skill show`. Prompts and resources are read-only and available in every mode, `--read-only` included. `initialize` points hosts at the `review` prompt or `verdict://skill/PROCEDURE.md`.
- 31efb1b: **Breaking:** the published JSON schemas moved from `skills/verdict/schemas/` to `/schemas/`, and every `$id` changed with them. Update `$ref` and `$id` URLs to the new path.
- 9de3880: verdict now keeps an append-only journal per bundle under `<state>/verdict/journal/` (`$XDG_STATE_HOME`, else `~/.local/state`, or `%LOCALAPPDATA%` on Windows), outside your repository. Every bundle-writing CLI and MCP call records a start and end line: the repository, redacted argv, before and after bundle digests, the exact paths written or removed with their digests, the exit and refusal codes, and the `next` hint. The journal is capped at 1 MiB and trimmed by whole lines. The new read-only `verdict journal <owner/repo> <pr|local-name> [--json]` verb (also in MCP, the Go SDK and the TypeScript client) prints the folded history.
- 938bc6d: **Breaking:** `review.json` is a findings-only handoff. Each finding carries `path`, an integer `line`, `severity`, `claim`, the quoted code (`quote`) and the finder's `confidence`; optional `spec_checks[]` and `receipts[]` carry the evidence verdict checks. `approved`, `body`, `comments[]`, `sections[]` and a top-level `confidence` are refused as unknown fields. The document keeps `"schema_version": "1"`, with no shim.

  To rewrite a stored review: move each `comments[].body` to `findings[].claim`, add `quote`, `severity` and `confidence`, make `line` an integer, move the old `body` to `summary` (at most 2,000 characters), and remove `comments[]`, `body`, `approved`, `sections[]` and the top-level `confidence`. The claim arrays (`resolved`, `triage`, `coverage`, `chunks`, `suppressed`, `risk`, `security_summary`) are unchanged. The `basis-replaced` problem code is gone, and the dry run's `data.check` reports `findings` and `anchors_moved_to_body` instead of `comments` and `sections`.

  Approval is no longer an agent claim. By default, any open, visible finding at `important` or above whose quote is not `unverified`, or any violated spec check, withholds approval; a repository's `block_clauses` refine that. `record` decides once and stores the decision in `record.json` as `approval`. `post` publishes that decision and refuses `record-stale` if the review, diff or head moved since.

- 1dfcc29: `backtest fixtures` grades converted fixture recordings against hand-known defects in a new fixture corpus. It publishes recall, bonus-union, false-green, hallucination (with explicit `unverifiable` counts) and `review-insufficient` metrics, reproduces seven hand scores as calibration, and writes one report under `.verdict/backtest/fixtures/report.json`. The mode never launches an agent or calls a host.
- 4ab8691: The review handoff is now lean and layered. `<bundle>/procedure.md` carries a short core (role, method, the findings-only contract and a capability map), and sections for chunks, prior threads, knowledge-base rules, lenses and handed context appear only when the bundle has them. The checklist and reference material go to `<bundle>/refs/` for the agent to read on demand, and `verdict skill show` keeps the full workflow. `--show-prompts` prints the handoff and names the refs.

  The agent is told to write `review.json` first and update it as it goes. After a clean exit with no review file, `run` salvages the last fenced `json` block on stdout that holds a `findings` array, records `salvaged: true` in `launcher.json`, and warns. A salvaged or written review that fails the checker refuses `bad-review-input` and feeds the next round. **Behavior change:** a review with no findings must carry a non-empty `summary`. `prompts_hash` now covers every file that feeds the handoff.

- b6cf021: verdict now renders the review body itself, from the structured findings, bundle metadata, evidence receipts, spec checks and approval decision. Agents no longer follow body-formatting rules.

  The body opens with a verdict lead (`✅ No new issues found.`, `ℹ️ Informational findings only.`, `⚠️ Issues found`, or `⛔ Evidence checks insufficient`) that `summary` cannot override. A `Decision: withheld — <reason>` line follows when approval is withheld, then a labeled `Basis:` line matching `verdict report`, and `Safety:` and `Risk:` lines when set. `summary` renders as one sanitized plain-text paragraph. Findings are ranked and show severity, rigor and category, `[path:line]` permalinks pinned to the reviewed commit on GitHub, a label on unverified quotes, and the `brief` collapsed under `Technical details`. Unanchored findings render once under `Unanchored observations`, and withheld findings are left out. Clean reviews add `What we checked`, insufficient ones `Evidence gaps`, and graded specs `Spec checks`. A review metadata comment is always appended. Behavioral mermaid diagrams are retired from agent reviews; structural diagrams in `report --overview` are unchanged.

  **Breaking:** the `degenerate-body` refusal is removed, since verdict owns the body end to end. When `post` splits a body over 65,536 bytes, each comment carries its own `[ev-N]` link definitions.

- 8b159e9: `fetch` and `fetch-local` accept a repeatable `--spec <file>` to hand specification documents into the bundle, separate from `--context` reference material. Validation matches `--context`. Spec sources share the `context/` directory and `context.txt`, `context.json` marks each entry `"role": "spec"` or `"role": "context"`, and `toc.txt` marks spec rows `[spec]`. Combined spec sources are capped at 150 checklist items; more refuses `spec-too-large` before any bundle file is written.
- 5e1ab56: verdict now checks the evidence a review states before deciding. Each finding's `quote` must match the diff at its `path:line`, a review whose findings are all below the approval tier must receipt every changed hunk, and `--spec` sources get a per-item checklist. The result is stored in `record.json` and read by every verb: `record --dry-run` names each gap, `run` repairs gaps across rounds and refuses `review-insufficient` if any remain, `gate` fails an always-on `evidence` dimension, and `post` publishes an insufficient review with that lead and never approves.

  **Breaking:** a review that states no evidence is insufficient, not approved.

  - Quote the exact line at each finding's `path:line`. A multi-line quote adds `start_line` and one quoted line per line in the range.
  - A review with no gating finding must carry `receipts[]`: one `{"hunk": "<the hunks.tsv row>", "quote": "<a line from that hunk>"}` per changed hunk. `record --dry-run` names every gap.
  - `run --rounds` defaults to 2, so an invalid review is re-prompted once. Pass `--rounds 1` for the old behavior.
  - `gate` grades a seventh dimension, `evidence`: a bundle with no evidence receipt for its head fails as `unknown`.
  - A `--spec` item with no check, or backed only by test output (logs, goldens, snapshots), withholds approval.

- 20f6fb0: **Breaking:** cross-check (panel) receipts are now version 2. verdict assigns each checker its identity, and a receipt binds the execution, head, diff, frozen review and dispatch plan. Every confirming or refuting quote must pass the same quote check as a review finding. Approval, `post`, `report`, `gate` and `ready` require every assigned cross-check, and a missing or disputed check on a blocking finding withholds approval as insufficient. Manual panel decisions are unchanged when no two-finder execution was captured. Version 1 panel receipts are refused: re-run `verdict panel` to write v2 receipts.
- efc4342: Two-finder executions now render which finders completed, the cross-check counts, each finding's origin and checker standing, and quoted refutation evidence. `record`, `post`, `report` and their MCP tools recompute cross-check eligibility from the bound artifacts, so a missing or forged stored stamp cannot replace them.

  **Breaking (SDK wire):** the report cost token fields (`input_tokens`, `output_tokens`, `total_tokens`, `cached_input_tokens`, and those under `by_tier`) can be `null`, meaning unknown usage, never a measured zero. The generated TypeScript types are nullable; clients must handle `null`. A rollup that could not price a launched phase reports `est_usd: null` instead of a partial total.

- 11ea710: **Breaking:** `run --agent` now defaults to two isolated whole-diff finders, each followed by a cross-check from the other. This roughly doubles agent spend per run. Use `--finders 1`, or set the global `defaults.finders: 1` (personal fallback `review.finders`), to keep the single-finder procedure; scoped finder counts are refused. `--print` and `--stage` require one finder and say so when they refuse. `drain` stays single-agent and `backtest` replays use one finder. A re-run moves the previous execution's artifacts to `x-superseded/<id>` before the new launch.
- 31f4df8: `cost.json` now attributes usage per phase, naming its execution, finder and launch. USD and token counts may be unknown, and a rollup never counts a root total beside its phase costs. A two-finder run authorizes its spend before any launch and records the topology and projection in the budget ledger. Unknown required prices refuse `budget-unknown` under the require-known policy.
- b40de1d: Remove the inactive behavioral-diagram controls: CLI flags `--diagram-behavioral`, `--no-diagram-behavioral`, `--diagram-expanded`, and `--no-diagram-expanded`; personal settings keys `publish.diagrams.behavioral` and `publish.diagrams.expanded`; and environment variables `REVIEW_PUBLISH_DIAGRAMS_BEHAVIORAL` and `REVIEW_PUBLISH_DIAGRAMS_EXPANDED`. Removed flags now produce the normal unknown-flag usage error, and removed settings keys produce `bad-settings`.

  Structural diagrams in `report --overview`, including `publish.diagrams.structural` and its CLI and environment controls, remain available.

- e70c53e: Large PRs now carry the computed change topology in the posted review body, not only in `report --output`. `record` and `post` render the same collapsed `Change topology (computed from the diff)` block when a PR touches more than 8 files or 24 KB, so the map a hosted reviewer sees is the one verdict computed, and the agent never draws one. `--no-overview`, `--no-diagram-structural`, `REVIEW_PUBLISH_DIAGRAMS_STRUCTURAL=false` or `publish.diagrams.structural: false` remove it. The block is capped (60 nodes, 60 edges, 12,000 bytes) with a note naming what was left out, and a body split never cuts it in half. Small PRs are unchanged.
- 06bef77: A review may carry one structured behavioral diagram: `review.json`'s optional `diagram`, with `type` (`sequence`, `state` or `flow`), an optional `title`, and `nodes` and `edges` whose `evidence` quotes a diff line the way a spec check does. verdict checks every quote against the diff and draws the picture itself; raw mermaid stays retired. The check proves only that each node and edge cites a real new-side diff line. The relation an edge draws is still the agent's claim. Unanchored elements are dropped, and the block is omitted with a note when fewer than half anchor. Labels keep only letters, digits and simple punctuation, and node ids are generated, so an agent cannot inject mermaid syntax. Caps: 24 nodes, 40 edges, 8,000 bytes per block. The block renders collapsed after the change topology, and its outcome is reported as `evidence.diagram` in `record.json` and `post.json` and as `data.body.diagram` in `post --dry-run`. A malformed diagram is a `diagram-invalid` warning, not a refused review, unless `--strict` or `strict_input` promotes warnings to errors.

  Opt in with `publish.diagrams.anchored`, `REVIEW_PUBLISH_DIAGRAMS_ANCHORED=true` or `--diagram-anchored`. It is off by default, and off changes nothing.

- 627f3fb: `verdict init --ci github` writes `.github/workflows/verdict-review.yml`: one workflow that reviews every pull request with your own agent and model key, with no hosted service. Its steps are pinned to full commit SHAs: checkout, `setup-verdict`, `verdict skill install`, `fetch`, `ready --check`, `run --agent`, `post`, an advisory `report`, then `gate --max-severity critical --require-head --live auto`. `post` runs before `gate` because the gate reads the receipt `post` writes. The job runs on `pull_request`, never `pull_request_target`, and skips fork pull requests, which get no secrets. Secrets arrive only through step `env:`, and the agent step clears `GH_TOKEN` and `GITHUB_TOKEN`. The workflow installs verdict but not the agent CLI: install it on the runner before the `Agent review` step, which carries the install command as a comment.

  `--agent <name>` takes any `run --agent` name (default `claude`), `--key-secret <NAME>` renames the repository secret, and `--print` renders the file without writing it. The overwrite guard covers the workflow, and the JSON envelope gains `ci`, `workflow_path`, `workflow_status` and `workflow`.

- d87b8fa: `fetch` now brings a PR's linked issue into the bundle as a spec source. On GitHub, a closing keyword in the title or body (`Fixes #12`, `Closes acme/app#7`) reads the linked issues, and each lands in `context.json` with `role: spec`, its URL as `source`, and its title and body as the document.

  `--issue <ref>` (repeatable, `fetch` only) adds an issue explicitly: `owner/repo#12` or an issue URL through `gh`, or a bare `#12` on GitHub. Issues in other trackers (Linear, Jira, …) go in through `--spec <file>`.

  An unreadable issue is a non-fatal warning (`issue-unavailable`, `issue-truncated`, `issue-over-cap`), never a silent skip or a failed fetch; a malformed operand refuses `issue-invalid`. Issues count toward the spec caps: an explicit `--issue` over the cap refuses `spec-too-large`, while a linked issue over it is demoted to `role: context`, so a PR body cannot break a fetch.

- 72450b3: `post` now submits to Bitbucket Cloud: each inline finding lands as an anchored
  comment (`inline: {path, to}` for a new-side line, `{path, from}` for an
  old-side line), the summary comment carries the review body, and a binding
  approval lands last via `POST .../pullrequests/<n>/approve`. Like GitLab,
  Bitbucket has no request-changes review event, so a bound request-changes
  posts the comments and warns that the verdict did not bind. Anchors keep the
  same honesty as every host — a finding with no placeable anchor is withheld
  and counted, never mis-anchored — and the run keeps the duplicate-post,
  stale-head, and SHA-bound receipt guards every other host carries.
- df4ea09: `post` now submits to Azure DevOps. Each finding becomes a review thread anchored on its file line (new or deleted side), and one general thread carries the review body. Every call is an `az rest` call after `az login`. No token reaches an argv or an error, and request bodies go through private `0600` files. `AZURE_DEVOPS_ORG` must be `dev.azure.com` or `{org}.visualstudio.com`, or the post refuses before `az` runs. Azure has no review event, so a verdict never approves or requests changes there: the threads and body post with a warning and a `withheld` entry in `post.json`. A finding without a placeable anchor is withheld and counted, `post` refuses when the head moved since the fetch, and a re-run updates its own summary thread instead of adding another.
- b9d633d: Org-wide rule packs: `packs.org_pack` in the base-owned `.verdict/config.json` pins one central rules `repo@ref` (`ref` a full commit SHA or a semver tag, never a branch) that every repository inherits, read from the trusted base so a PR cannot change or drop it. `verdict pack sync` vendors the pack into `.verdict/packs/` and hash-locks it in `.verdict/verdict.lock`. Reviews never fetch: every review path verifies the vendored tree against the lock and refuses `pack-lock-missing` or `pack-lock-drift`. `verdict report` and the provenance receipt show the pack version as `org_pack`.
- f7df884: New per-repository monthly spend ceiling: `defaults.monthly_budget_usd` in `.verdict/config.json` and `spend.monthly_budget_usd` in personal settings, the stricter one winning. `run` refuses `spend-capped` (or `budget-unknown` when `require_known_estimate` sees unknown cost) before launching any agent when month-to-date spend plus the run's projected cost would exceed it. **Behavior change:** `--consent-over-budget` now covers only the current invocation. `report --cost` adds a month-to-date block (`mtd` in JSON) when a ceiling is set.
- 1264075: `verdict init --ci forgejo` writes `.forgejo/workflows/verdict-review.yml`, the same every-PR pipeline as `--ci github` on verdict's native Forgejo provider. Host and token come from the runner (`FORGEJO_HOST: ${{ github.server_url }}`, `FORGEJO_TOKEN: ${{ github.token }}`), never a repository secret, and the agent step clears `GH_TOKEN`, `GITHUB_TOKEN` and `FORGEJO_TOKEN`. The Forgejo workflow drops the `permissions:` blocks, which Forgejo lacks, and the `ready --check` step, which Forgejo bundles do not support; the gate still blocks. The install step uses the full `https://github.com/stainless-code/verdict/setup-verdict@<pin>` reference, because a short reference resolves through a mirror that lacks it. The manual gains a _Forgejo Actions_ section.
- ed83902: `verdict init --ci gitlab` writes `.gitlab/ci/verdict-review.yml`, a one-job merge-request pipeline that installs verdict, fetches with `--provider gitlab`, checks readiness, reviews with your agent, posts, reports and gates. With no root pipeline it also writes a minimal `.gitlab-ci.yml`; an existing one is never modified, even with `--force`, and the include line to add is printed instead. `--print` renders both files without touching the tree.
- d444e11: Verified `critical` and `important` findings with a structured brief now render a collapsed `Prompt for AI agent` block in the review body, right after `Technical details`. It is a fenced `text` block a developer copies in one click: the severity, the `path:line`, the claim, the brief's sites, the required outcome, the suggested approach and the open questions. It is rendered from the settled finding, with no model call, and appears in the body only; inline comments are unchanged.

  A finding gets the block only if it is open, visible, verified, not refuted by a cross-check, `critical` or `important`, and has a brief outcome. Every value is escaped, so a hostile brief cannot forge markup, and a body split keeps each `<details>` block whole.

  Turn it off with `publish.fix_prompt: false`, `REVIEW_PUBLISH_FIX_PROMPT=false` or `--no-fix-prompt`; off, the body is unchanged.

- 0ad5fb0: `verdict demo [<public-github-pr-url>] [--agent <name>] [--keep] [--json]` runs one review with no setup and no risk. With a URL it reads a public pull request, through `gh` when it is signed in to github.com and the anonymous REST API otherwise. With no argument it reviews your working tree. It uses the first installed agent CLI on `PATH`, or `--agent`, and refuses `agent-missing` with an install hint when there is none. It runs one finder and prints the review exactly as `post` would publish it. Everything lands in a temporary `verdict-demo-*` directory, removed on exit unless `--keep`: nothing is written to your repository, and nothing is ever posted.
- b6970e1: `verdict run --budget auto|light|standard|deep` and the repository defaults `defaults.budget` and `defaults.sensitive_paths` let a run pick its review depth from the diff before any agent launches. The choice is a deterministic function of the bundle, with no model or network, and the tier and the signals behind it are recorded in `launcher.json` and `data.budget`.

  `light` runs one finder; `standard` and `deep` run two finders with cross-checks, and `deep` is not lowered by a repository-wide `defaults.finders: 1`. An explicit `--finders N` always wins. The default sensitive globs (`**/auth/**`, `**/security/**`, `**/migrations/**`, `**/billing/**`, `.github/workflows/**`) escalate a diff to `deep`; `defaults.sensitive_paths` adds to them and cannot remove any. Both keys are global only: a scope that sets either refuses `bad-scope`. `--budget` means review depth, not spend.

- 214a92e: **Breaking:** `fetch` and `fetch-local` now always withhold built-in secret paths: `.env` and its variants, private keys (`*.pem`, `*.key`, `id_*`, `*.ppk`, `*.p12`, `*.pfx`, `*.jks`, `*.keystore`), credential files (`credentials.json`, `.netrc`, `.npmrc`, `.pypirc`, `.envrc`, `.pgpass`, `.htpasswd`, `kubeconfig`) and Terraform state. The rule is `default:secret` (reason `secret-builtin`). It runs before every other rule, checks both sides of a rename, and ignores case. No `.verdict/ignore` negation, `--ignore` or `--no-default-excludes` brings a path back, so a PR that changes only such files now reviews nothing for them. Templates (`.env.example`, `.env.sample`, `.env.template`, `.env.dist`, `.env.defaults`) stay reviewable.

  `fetch --preview` and `fetch-local --preview` print the exact reviewed files, every withheld section with its rule, the byte totals and the review budget tier, then stop without writing a bundle or starting an agent. `--json` carries the same as `data.preview`.

- dbee824: The built-in layer now ships eleven curated language and ecosystem packs beside the core pack: `verdict/go`, `verdict/ts-js`, `verdict/python`, `verdict/rust`, `verdict/java`, `verdict/sql`, `verdict/terraform`, `verdict/github-workflows`, `verdict/dockerfile`, `verdict/shell`, and a cross-language `verdict/security`. Each carries a stack lens scoped to its file globs and a few high-precision rules for real bug classes, plus an eval case that `pack test <name>` grades offline. `lenses.txt` now carries only the lenses for the bundle's changed files.

  A repository can tune the built-in layer from its trusted-base `.verdict/config.json`: `packs.merge_builtin` turns it off, and `packs.disable_packs`, `packs.disable_lenses` and `packs.disable_rules` drop built-in ids. The setting is read at the merge base with the default branch, so a branch cannot disable the rules that review it.

  `verdict pack check <path> [--json]` shows how one path resolves: the packs in precedence order, the lenses offered, and the matching rules with the pack that won each. It writes nothing and makes no host calls.

- 63c67b2: `run` now launches discovery with the shell denied. Every finder, cross-check, repair round and drain launch passes the agent's own documented tool restriction, so an agent cannot spend its budget on builds and tests that `verify --sandbox` runs better; file reads and writing the review files stay allowed. Three agents document such a flag: `claude` gets `--disallowedTools=Bash,PowerShell`, `bob` gets `--disable-tool-groups execute,mcp`, and `pi` gets `--exclude-tools bash,powershell`. Other agents launch unchanged. **Breaking:** pass `--allow-shell` if your agent needs to run builds during review. No flag you pass after `--` drops the restriction, the flag it uses (pi's `--exclude-tools`, bob's `--disable-tool-groups`) refuses `bad-flags` there, and `--resume` refuses `resume-drift` if the restriction changed.

  Chunked fetches now keep related files together. Files are ordered by package directory, then implementation beside its test (`foo.go` and `foo_test.go`), then path, before they are packed. Per-group runs never mix groups in one chunk, and chunks are cut from the privacy-filtered diff, so a withheld section never reaches `chunk-NN.diff`.

- 7df4c5c: `verdict run --resume` continues the two-finder execution recorded in the bundle's `launcher.json` instead of starting over. Before launching anything, it checks a fingerprint over the diff, inputs, context, prompt set, head, agent definition, finder topology and review depth, and verifies every bound finder and cross-check artifact. It reuses each phase whose artifact still checks out and re-runs only the incomplete ones. Any drift or damage refuses `resume-drift` (or the spend refusal) and leaves the interrupted record unchanged, so the resume can be retried. A bundle with nothing to continue refuses `cannot-resume`, and no refusal launches an agent. `data.resume` names the reused and executed phases.
- 5deb28c: `verdict ingest --from sarif <file|->` imports a SARIF 2.1.0 document from any analyzer (Semgrep, CodeQL, golangci-lint, Ruff, Trivy, or `verdict report --sarif`) under the 8 MiB cap. The source format is required: use `--from sarif <file|->`. Repeating `--from` is a usage error, and any other `--from` format refuses `bad-from`.

  Each result is graded against the bundle's diff, not the scanner's word. The diff line becomes the finding's `quote`, an anchored result grades `rigor: "pointed"`, and a snippet that disagrees with the line is skipped. A result with no location, a bad line, a path outside the repository or the diff, or an ambiguous path is listed in `data.skipped[]` with its reason, never dropped silently. Absolute, drive-letter, traversal and host-bearing `file://` URIs are refused. Results dedupe by finding signature and by tool fingerprint, and `report --sarif` now keeps `tool`, `rule_id` and `fingerprint` under `properties`, so its export re-ingests as the same findings.

- 4c18cf5: `verdict report <repo> <pr|slug> --compare <baseline>` compares finding signatures with a prior bundle (a directory, or a PR or `local-<name>` in the same `.verdict/state`) in four buckets: `new`, `persisting`, `resolved` and `not reviewed`. Persisting rows show severity and status moves. A finding counts as resolved only when attested coverage of its hunk shows it gone or the PR's diff no longer touches it; an incremental delta, an excluded file, an unattested hunk or a missing snapshot reads `not reviewed` with its reason, so an absent finding never reads as a false fix. A bad baseline refuses with a typed code (`missing-compare-bundle`, `unsafe-compare-bundle`, `corrupt-compare-bundle`, `unrecorded-compare-bundle`, `bad-compare`). `--json` adds `data.compare`, and the Go SDK, the MCP `report` tool and the TypeScript client carry the flag. The comparison is advisory and changes no exit code.
- e6b22b1: `verdict init --ci github --comment-trigger` adds a `/verdict` comment re-run. The workflow also runs on `issue_comment`, but only for a pull-request comment from an `OWNER`, `MEMBER` or `COLLABORATOR` that is exactly `/verdict` or starts with `/verdict `. Such a comment replaces the review in flight for that pull request; other comments cancel nothing. It never adds `pull_request_target`, never checks out the pull request head, and never puts the comment body into a script. The flag is GitHub-only.

  Bare `verdict init` on a terminal now asks three questions: the preset, the CI workflow (defaulted from the `origin` host) and the agent (defaulted to the first installed one). Enter keeps each default, and the run writes the same files the explicit flags would. Any selection flag, `--json`, or redirected input or output keeps it non-interactive. The new `init` flags reach the Go SDK's `InitRequest`.

- b1e4513: New `verdict acp`: a lightweight ACP v1 stdio server for editors, read-only, with no model calls and no daemon. `initialize` negotiates protocol version 1, and four methods carry the reads: `verdict/bundles/list`, `verdict/findings/list` (with evidence rungs), `verdict/findings/get`, and `verdict/findings/copy`, which returns the finding's `Prompt for AI agent` and a runnable `verdict triage` command. Policy-withheld findings are never listed or copied. Flags: `--root`, `--check`, `--max-bundles` and `--json`. Bad frames, unknown methods and bad parameters return JSON-RPC errors naming the refusal code.
- 1757748: `verdict fetch-local --scan <path>` reviews files instead of a change. A file operand selects that file, a directory walks every regular file below it, and the bundle carries them as added-file diffs, so a clean tree still yields a reviewable bundle. Paths must stay inside the repository. `.git` and `.verdict` are never read at any depth (case variants included), symlinks are neither followed nor selected, binary files contribute no content, and a path with a tab or newline is skipped with a warning.

  It refuses `bad-scan` for a path outside the root, a symlink, a managed directory, an unreadable file, or a scan over the bundle cap, and `bad-flags` when combined with a diff source, `--worktree` or `-- paths`. Secret, ignore, privacy and size exclusions apply as on a diff run, and `--scan <path> --preview` writes nothing. The result is an ordinary local bundle (`source.json` `kind: "scan"`) that `record`, `report`, `gate` and `serve` read unchanged, and it gates on the coverage it recorded. The flag reaches the Go SDK (`FetchLocalRequest.Scan`) and the MCP `fetch-local` tool (`flags.scan`).

- 404eeff: `verdict serve` adds a read-only compare view: `GET /v1/roots/{root}/bundles/{bundle}/compare?baseline={bundle}` serves the same comparison as `report --compare --json`, and a missing, malformed or out-of-root baseline refuses (`bad-baseline`, `missing-compare-bundle`). The detail page gains a **Compare** tab with a baseline picker, and each finding offers **Copy prompt** and **Copy triage command** buttons; the copied triage command runs as pasted. Static exports omit the Compare tab.
- b1c24bc: `verdict serve` adds local triage and knowledge-base promotion (`POST /v1/triage`, `POST /v1/kb/promote`) through the same services the CLI uses, with inline controls in the embedded UI. A write needs the per-session `X-Verdict-CSRF` token from `/v1/health`, an allowlisted `Origin`, and a strict JSON body. Static exports show no write controls. The write-posture entry says when `serve` writes.
- 93588cf: `verdict serve` adds operator jobs: `POST /v1/jobs/verify` runs a sandboxed `verify --sandbox` over a bundle, and `POST /v1/jobs/post-preview` runs a dry-run `post`, with `GET /v1/jobs/{id}` and `POST /v1/jobs/{id}/cancel`. Jobs hold a per-bundle lock, run with bounded concurrency, and stream their state on `/v1/events`. The Detail view gains `Reproduce in sandbox` and `Preview PR post` buttons when the server allows local writes.
- 55e9178: `verdict serve --allow-post` adds confirmed host posting. The Detail view's `Submit Review` button (PR bundles only) previews the post, shows every comment it will send and the preview's warnings, and posts only from the confirmation dialog. On a writable server without `--allow-post` the route refuses `403 post-disabled`; a read-only server does not register it, so a POST there gets `405`.

  `POST /v1/post/submit` spends a single-use preview nonce, re-renders the review under the bundle lock, and refuses `409 stale-preview` unless it matches the preview. The post is pinned to a digest of the exact payloads the host receives, so a later change refuses `409 post-content-changed` before any host write. Every guard of the CLI `post` holds, and a served post authenticates and renders exactly as the CLI's does. A preview over 256 KiB of text mints no nonce, so Confirm stays disabled; check it with `verdict post <repo> <pr> --dry-run` and post from the CLI. The submit is bounded to five minutes (`post-timeout`), and Ctrl-C stops an in-flight host call. The `--json` start envelope reports `read_only` and `allow_post`.

- 607f269: The built-in packs are now version-aware and fact-checked, and a new `verdict/react` pack joins them. Every rule has a `When to flag` section, including the version or runtime condition that decides it, and a `Do NOT flag` section naming safe idioms and linter duplicates.

  Coverage grows for Go (loop-variable capture before Go 1.22, timers, context leaks, `rows.Err`, `%w`, slice aliasing, mutexes), TS/JS, Python, Rust, Java, SQL, Terraform, GitHub Actions permissions, Dockerfiles and shell, plus four security rules: timing-safe comparison, CSPRNG, JWT validation and path traversal. `verdict/react` applies only to `*.tsx` and `*.jsx` and covers effect cleanup, stale closures, index keys, derived state, conditional hooks, raw HTML and the server/client boundary. Every curated pack ships a positive eval case and a safe negative fixture, which `pack test <name>` checks offline.

- 915a564: Every verb that takes `<owner/repo> <pr>` now also takes a pull or merge request URL in its place (GitHub, GitLab including nested groups, Forgejo/Codeberg, Bitbucket, Azure DevOps; a self-hosted GitLab or Forgejo only when `GITLAB_HOST` / `FORGEJO_HOST` already maps it). The URL implies `--provider`, and an explicit conflicting `--provider` refuses `bad-target-url`, as do hostile or ambiguous URLs. With no positionals, the repository is read from the `origin` remote and the pull request from the one recorded `.verdict/state` bundle whose head ref is the current branch, offline; an ambiguous or missing match refuses `target-unresolved` with the exact arguments to pass. A derived target is echoed on stderr in human output. `fetch-local` keeps its own syntax. See `docs/manual.md` § Targets.
- a93beec: **Breaking:** a verb with no resolvable target now exits 2, the usage-error class, with the typed `target-unresolved` refusal naming the arguments to pass, followed by the verb's usage line (under `--json`, a full refusal envelope). Scripts that treated exit 1 as "no target" must accept 2. The TypeScript client throws `VerdictRefusalError` for it, and the Python example raises `VerdictRefusal`.

  Verbs that already talk to the host (`fetch`, `pr-wait`, `ready`, `threads`, `threads resolve`, `drain --post`) now ask the host CLI for the branch's open pull request when no recorded bundle names it. The answer must pass the same checks as a pasted URL, and the pull request's head must live in the `origin` repository, so a fork's PR with the same branch name never counts. The chosen target is echoed. Offline verbs never reach the host. See `docs/manual.md` § Targets.

- abb3389: `ready --filter` now gates the review on the closed trigger predicates in the trusted base configuration: matching PRs run and record the decision, non-matching PRs refuse `filtered-out` before any host call, and unusable policy or facts refuse `trigger-failed`.

  Fetch now requests and records PR labels in `PR.json` for every host, so the `labels` trigger predicate can read them from the bundle.

- 312477c: `verdict loop <owner/repo> --agent <name>` is a built-in agent self-review loop, replacing the Python recipe. Each round fetches your working tree into a fresh local bundle, runs a review-only agent pass, records it with `--require-coverage`, and gates open findings at `important` or above by default. Use `--max-severity informational` to gate every open finding. A failed gate starts a fix phase: the agent fixes the open findings, and `verdict verify` runs any command it declared in `patch.json`. The next round reviews the result from scratch.

  The loop stops on `pass`, `max-rounds` (`--max-rounds`, default 3), `timeout` (`--timeout`, default `600s`), `cost-budget` (`--max-cost-usd` reached, unmeasurable spend while it is set, or the monthly ceiling), `no-progress`, `agent-failed`, or another refusal. Every spawn leaves a spend receipt that the monthly total counts. `verify` runs sandboxed by default (`--sandbox-network-only` or `--no-sandbox` opt out), `--fix-prompt <file>` replaces the fix instructions, and `--print` shows every planned command without running it. `round-N.json` and `summary.json` land in `--output`, and `--json` prints the summary.

  **Breaking:** `docs/recipes/self-review.py` is removed; use `verdict loop`.

- 4165486: **Breaking:** a `VERIFIED` claim in `verification.json` no longer marks a finding `addressed` on its own. `record` and `post` also require the bundle's `verify-receipt.json`, which only `verdict verify` writes. A claim applies only when the receipt shows exit 0, no timeout, the claim's exact `verifyCmd`, the claim's finding when the receipt names one, and a tree equal to the graded content's (see the content-bound receipts entry). A hand-written claim or a pass written into `patch.json` is not enough: run `verdict verify` on the content you grade. Otherwise the finding keeps its status, the claim is logged as a `revalidation` event, and the run warns `VERIFIED claim for <sig> not applied (<reason>)`. `gate`, `post --approve`, MCP and `serve` all read the settled status. `NOT VERIFIED` still reopens a finding without a receipt.

  A `triage[]` claim of `addressed` now refuses the review (`bad-review-input` from `record`, `bad-triage-status` from `post`) instead of being ignored with a warning.

  `verdict verify` refuses a symlinked bundle directory, writes only through the directory it pinned before the run, and deletes the previous receipt before the command starts.

- ed1e9ae: `verify` can now reuse installed dependencies instead of installing them on every run. List gitignored directories in `.verdict/config.json` as `"verify": {"share": ["node_modules"]}`, and each one appears in the clean snapshot at the same path. A PR bundle reads the list at its base commit; a local bundle reads it at its mode base (`HEAD` for `fetch-local`).

  - **Read-only only under `--sandbox`.** On Linux, bwrap binds the checked directory read-only from an open handle, so renaming it or swapping in a link afterwards changes nothing. On macOS, Seatbelt denies writes to its real path. Under `--sandbox-network-only` or with no sandbox, the snapshot links to the directory and writes reach your checkout.
  - **Refused entries.** An entry that is tracked, not gitignored, uses `..`, is absolute, passes through a symlink, or sits under `.git` or `.verdict` refuses `bad-config`, and nothing runs. A directory the checkout lacks is skipped with a warning.
  - **Recorded and shown.** The verify receipt, the group receipt and the `--json` envelope list every shared path in `shared`. `verify` prints the shared directories and whether they were read-only, and `gate`'s evidence detail notes a VERIFIED claim that rests on such a receipt, without failing it.
  - **Proposed by `init`.** `verdict init` adds `node_modules` when it finds `package-lock.json`, `pnpm-lock.yaml` or `yarn.lock`, and `.venv` for `uv.lock` or `poetry.lock`, but only when git already ignores the directory and tracks nothing in it.

  Sharing trades the clean-snapshot guarantee for speed: a stub planted in a shared directory could make a command pass. Leave the list empty to keep the snapshot clean.

### Patch Changes

- 6578b8b: The `stainless-code/verdict` GitHub Action now pins its `setup-verdict` step to the v0.7.0 release commit, so Action users install through the hardened installers instead of the pre-release revision it pinned before.
- 4b05cd3: `verdict mcp` shuts down cleanly even when a read is blocked on its input. `verdict serve` ends open event streams when its listener fails. The CLI reads its environment only through the source it was given, so host variables no longer leak into a run that was given its own.

  `report --validation` without `--json` now prints a validation table: the mode, the file hash, the counts, and one row per judged finding with its verdict, severity move and reason. With `--json` it uses the saved tally when a receipt exists. `post` and `record` warn when `validation.json` was produced by a different `prompts/validate.md`, and still apply it.

  `drain` serializes writes to the same bundle, and `drain --agent --post` refreshes threads after drafting so resolved or changed threads are skipped. Replanning keeps earlier posting receipts, outstanding failures persist across runs, and a retry clears a failure whose thread was resolved or whose draft is obsolete.

- 0242675: A cost-capped `loop` or `run` now refuses typed `budget-unpriceable` before the first spawn when verdict cannot price the agent's launch. Before, a capped `loop` paid for round 1 and then stopped with `budget-unknown`, and a capped `run` launched once its projection was known, leaving spend nothing could measure in the month. `run` still launches when the invocation passes `--consent-over-budget`. The caps are `loop --max-cost-usd`, or a monthly ceiling with `require_known_estimate` on `run`. verdict cannot price an agent whose CLI reports neither its cost nor token usage (`pi`), or `codex` with no model named, or one the price table lacks. The refusal names the fixes: a priced model (`-- -m <model>`, or `model` in codex's `config.toml`), an agent that reports its cost, or dropping the cap and bounding the loop with `--max-rounds` and `--timeout`. Declaring a price or `"free"` per model is planned (STA-527/STA-543). Without a cap, nothing changes.
- 07545e7: Everything verdict writes to stderr now has its control characters (except newline and tab) folded to spaces: `warn:` and `error:` lines, `gate` lines, `fetch` split candidates, and the MCP server's progress and warnings. A spec item, diff path or directory name a stranger chose (one carrying an OSC 52 sequence, say) can no longer write your clipboard, move the cursor or clear the terminal. verdict's own colors and `upgrade`'s package-manager output are unchanged.
- 5b6e5ff: A two-finder `run` no longer reports one defect twice when the finders word it differently. The cross-check request now lists the checker's own findings, and a confirming row may name one as `same_as`; the coordinator merges the pair into one finding (higher severity, the better-evidenced quote, both origins and both checks) only when the other finder's check confirmed the named finding and the two sit in the same file and side at overlapping or adjacent lines. A `same_as` the anchors don't support is ignored with a warning naming why. `verification/plan.json` records each merge under `sources[].merged`, and `post`, `report`, and `gate` count the defect once.
- 7fadf71: Hunk receipts accept the `OLD` row `hunks.tsv` prints for a modified hunk as a name for that same hunk. A review that copies every row of `hunks.tsv` into `receipts[]` no longer fails with a false `receipt-hunk-unknown`, its `OLD` and `NEW` receipts for one hunk count the hunk once toward coverage, and a bad quote on either row is still `receipt-mismatch`.
- 1e59f22: `verdict fix` no longer refuses PR bundles without `--selected`: a default pass, `--all`, `--recipe`, and `--test` now run on a bundle a `fetch` wrote, and the instructions and `next` hint name the landing that works there — push the fix, re-fetch, then land with `post`, since `record` refuses a PR bundle. On a PR bundle, `fix` refuses `head-mismatch` unless the checkout is on the head the bundle reviewed, so a pass, a patch, or `--test --commit` never lands on main or a stale branch. `fix` still writes only its ledger and patch beside the bundle and issues read-only git commands on either bundle kind. Corpus bundles still refuse with `not-local`, because a fix ledger does not belong inside a benchmark measurement input.
- 59e8d87: Every refusal now folds the line breaks and bidi controls in a value it quotes to spaces, not only `record`, `post`, and thread refusals. A flag value, a path, a config key, a `review.json` `schema_version`, an agent's file, or a host's or child process's error text carrying a newline, a CRLF, or U+2028 could end the `error:` line and start a second one that read as verdict's own, in `fetch`, `verify`, `run`, `loop`, `ingest`, `triage`, `report`, `pack`, `serve`, `mcp`, and every other verb. The value is still named, a refusal that lays out several lines of its own keeps them, and the `--json` `error.message` carries the same folded text.

  Usage errors, `loop` refusals, and `»` progress lines fold the values they name the same way. The stderr `gh` and `git` print during `fetch`, and the agent output tail in a `run` refusal, now reach the terminal one line at a time, each line folded and led by an indented tag (`gh:`, `git:`, `agent:`), so no line another program printed can read as verdict's own `error:` or `warn:` line. Before, both reached the terminal verbatim.

- 19a1477: Every human output line that quotes a value now folds that value's line breaks and bidi controls to spaces, not only refusals and `»` progress lines: a `doctor` check's detail, a `calibrate` row, a `post --dry-run` diagram detail, `fix`'s finding locator and brief, `learn`'s import and rule lines, `backtest`'s mined candidates and fixture report, `loop`'s round lines, and the rows `report`, `trend`, `handoff`, `index`, `journal`, `pack`, `risk`, `gate`, `adopt`, `ingest`, and `triage` print. A path, a host's reply, or an agent's text carrying a newline could start a line that read as verdict's own `error:` or `warn:`. A block another author wrote (a finding's body and the `kb.txt` recall in `fix`, the attribution footer in a `post --dry-run` preview) keeps its lines, each led by an indented `|` tag. `handoff` prints a finding's suggested code as a markdown indented code block, one line per line of code, in place of a fenced block: the code renders the same, and no line of it can start as verdict's own `error:` line.
- 3232b0f: `gate` now fails on a violated spec check. The always-on `spec` dimension reads the spec checks the stored record graded: a `violated` one fails the gate with `violated spec check: <item>` and a tally, while `met`, `n/a` and a review with no spec checks pass. Before, a bundle whose only defect was a violated spec check passed the gate even though approval was withheld.
- 5a1f3a1: Every JSON verdict writes now escapes the C1 controls (U+0080–U+009F) and the bidi controls (U+202A–U+202E, U+2066–U+2069) as lowercase `\uXXXX`: `--json` envelopes, MCP, ACP and LSP responses, `serve` responses, and the receipts and state files a consumer reads. Go's encoder wrote those runes raw, so a consumer printing an envelope string to a terminal could run a CSI such as U+009B or see reordered text. Envelope bytes change only for strings that carry those characters; the decoded values are unchanged.
- c38c737: A finding that cites a pack rule `fetch` recalled into the bundle's `kb.txt`, such as the built-in `sec-injection`, keeps that cite: `record` no longer warns `ignoring unknown kb rule` and drops it, with or without a `.verdict/kb.jsonl`. A flip of such a finding credits nothing and warns nothing, because pack rules carry no counters. An id the bundle never listed and the store does not hold still warns.

  A configuration parse refusal now names the file it read: `.verdict/config.json: schema_version: missing format marker, …`, and `settings file <path>: …` for the personal settings file. A `fetch` or `fetch-local` that refuses no longer leaves a half-written bundle: the bundle directory the run created is removed. A refused re-fetch keeps the bundle directory that was already there; files it wrote into it before the refusal stay as written.

- e64bafe: `verdict lsp` resolves workspace roots concurrently, so a hung volume delays `initialize` by one 250 ms budget instead of one per root, and its slow-walk log lines name the real cause. `verdict serve` and `verdict lsp` no longer miss a bundle written just as the watcher starts. A session that ends during a stuck walk no longer waits it out, and a slow watcher no longer stalls `didChange` or `$/cancelRequest`.
- 84c26ba: On macOS, `verify --sandbox` now warns when verdict's own environment carries secret-named variables. That covers `verify --group … --sandbox` and `verdict loop --sandbox` too. Seatbelt cannot gate the `kern.procargs2` sysctl, so a sandboxed command can read the environment of verdict's own process. The warning uses the same rule as the tail redactor: a secret-shaped name such as `GH_TOKEN` with a value of at least 8 bytes. It names the variables, never their values, and points at the Linux-VM `bwrap` recipe. It goes to the diagnostic stream and to `warnings` under `--json`. `--sandbox-network-only` carries the same warning, since it keeps the host `/proc` readable. Linux `bwrap` runs never do.
- 052a524: The MCP `query-*` tools and `verdict://` dashboard resources now serve the repository the server runs in, as `acp` does, instead of the directory the binary is installed in. Before, every installed binary answered them with no bundles. A root that will not resolve now refuses `not-a-repo` on tools and resources alike. The dashboard reads (MCP and `serve`) no longer follow a `.verdict` or `.verdict/state` symlink out of the served root, and in a git checkout, a symlink the repository commits under `.verdict` refuses that root's verdict data on every dashboard read path and the `serve` watcher, so a repository cannot serve another checkout's bundles or files. An override fragment linked out of its repository refuses `bad-override`, also in `report --output`.

  Measured spend now shows in `report --cost`, the publish footer, and the `fetch` estimate (the dashboard cost rollup still reads `cost.json` only). `report --cost` adds a `measured:` line (`measured_usd` and `measured_receipts` in JSON) for the spend receipts a two-finder run settles. Its `receipts:` line counts only real `--consent-over-budget` runs, and a token count it cannot know prints `n/a` instead of `null`. The publish footer prices a two-finder bundle from that measured spend, and the `fetch` estimate treats each two-finder execution as one comparable run, however many invocations it took. Spend guards now compare the exact projection, so an estimate that rounds to the ceiling no longer slips under it. Money prints rounded to four decimals, so an agent's float noise (`$1.30604299999999994`) no longer shows, and an amount over its ceiling rounds up so the breach stays visible. The `spend-capped` refusal names the setting to raise: `spend.budget_usd` for the per-run ceiling, or `spend.monthly_budget_usd` and `defaults.monthly_budget_usd` for the monthly one.

  `run --agent agy` no longer warns about print-mode permissions when the arguments after `--` already carry `--dangerously-skip-permissions` as an option (not as another option's value).

- 235dd05: `verdict panel` now reads its verifier prompt from the binary's embedded Procedure instead of a `procedure/` folder beside the executable, so it works from an installed binary and not only inside a verdict checkout. `verdict init` presets now write panel rosters `verdict panel` accepts (`standard` was a single `main` entry, which panel refuses for lacking a verifier; it is now `lead`, `lens-1`), and the budget guard grows with strictness: `chill` `0.10`, `standard` `0.50`, `strict` `2.00` (it was inverted, so the deepest panel carried the tightest guard).
- e3befca: A two-finder `run` now launches both finders at once, then both cross-checks at once, instead of one after the other. A refusal in one phase stops its sibling; an agent failure lets the sibling finish so the run can report what completed. On timeout or Ctrl-C, an agent's process group now gets `SIGTERM` and five seconds to exit before `SIGKILL` on macOS and Linux (Windows still ends the job object), and `agent-timeout` is reported only for a real timeout, never for a cancel. A re-run on a re-fetched bundle moves stale finder and cross-check directories to `x-superseded/` instead of reading them.
- 4f0aaad: A self-hosted GitLab on a non-default port (`gitlab.corp.example:8443`) works for a URL or `origin` target and for `post`, instead of refusing `host-mismatch`. glab's `--hostname` cannot carry a port, so verdict runs such a call unpinned, but only when the environment already makes glab's default host exactly the target: `GITLAB_HOST` names the same host and port, `GITLAB_API_HOST` (if set) and, under glab's CI auto-login, `CI_SERVER_FQDN` name it too, and `GITLAB_REPO` is unset. Any other environment refuses, naming the variable to set. An ssh `origin` still refuses when `GITLAB_HOST` names a port, and GitHub hosts with a port still refuse.
- f9c0dd0: `verdict run` no longer reports every agent failure as `review-insufficient`. When every finder fails, the run refuses with the agent's own code (`agent-failed` with its exit status, or `no-review-artifact`) and a bounded, credential-scrubbed tail of its output, so an HTTP 402 or a refused write is visible. A partial two-finder run keeps `review-insufficient` but names the failed finder's cause. The `claude` permission hint now prints for two-finder launches too.

  **Behavior change:** `fetch-local` refuses a bare word before `--` as a usage error instead of treating it as a path filter; path filters stay `-- <paths>`.

  `verify`'s recorded output tail is now scrubbed before it is cut, with terminal escapes removed first, so a credential split by the 4,000-byte cut or an escape sequence no longer leaves a readable piece in `patch.json`.

- e444457: Sandboxed `opencode` reviews now sign in with your real opencode login. Current opencode keeps its logins in its database rather than `auth.json`, so verdict reads the database's credential table read-only, without SQLite, and seeds each provider's login into the launch's throwaway data home. `OPENCODE_DB` now points at that throwaway copy. The review sandbox now hides opencode's whole data directory (`~/.local/share/opencode`, `$XDG_DATA_HOME/opencode`, and the directory `OPENCODE_DB` points into, and its target's when it is a link) from every agent, because its database holds the same tokens as `auth.json`. A database or journal file created there mid-review is hidden too. `agy` now declares `~/.gemini` as read-only state. On macOS, a sandboxed `agy` review warns before launch that its keychain sign-in stays closed, and that it signs in only with `GEMINI_API_KEY` set and agy configured to use it. The keychain stays closed because agy keeps a shell, and with the keychain open that shell could read every token stored there, `gh`'s included. Under `--allow-shell`, claude's keychain stays closed for the same reason; a macOS `claude` review then warns to set `ANTHROPIC_API_KEY`. On macOS, `run`/`loop --agent`: agy always needs `GEMINI_API_KEY`, claude needs `ANTHROPIC_API_KEY` only with `--allow-shell`. Driving verdict from a harness needs no API key. Sandboxed sign-in has been run live for `claude`, `opencode`, `codex` and `pi`, but not yet for `agy`. Every other agent's login layout comes from its own documentation and is not yet verified (STA-547). If one of them fails to sign in inside the sandbox, `--allow-unsandboxed-review` runs it without the sandbox, which also lets it read your credentials.

  Credential paths now preserve operator values until the sandbox follows each link, so `..` after a link names the same target the operating system opens. This covers relocated stores and home-relative entries too, including dangling links whose targets appear after launch on macOS. A link with an empty target, or `..` after a missing path component, refuses the review with a policy error, including for claude and codex.

  Relative agent configuration homes now resolve once through the sandbox's path walker for both the policy and the launched environment. Login seed sources use the same filesystem resolution, so a `..` after a linked directory seeds the location the sandbox protects.

- 154107e: The run directive names the handed `--context` spec.
- f44be4c: The MCP server now serves `build`, `ci`, and `grade` as task prompts, one per task prompt the
  Procedure's mode router names, so `prompts/list` offers eight instead of five. Each body is the
  same assembly the other task prompts use — `PROCEDURE.md`, `prompts/system.md`, and that task's
  own prompt — and an unknown name still refuses `-32602` and lists the valid tasks.
- a39930f: `run --agent agy` now warns at launch that agy's print mode soft-denies file edits outside trusted workspaces, so `review.json` may never be written, and proceeds instead of refusing. `docs/manual.md` documents the two fixes (trust the repository in agy's settings, or pass `-- --dangerously-skip-permissions`) and a working `run --agent agy` recipe.
- d5bacb3: `verdict run` no longer panics when a two-finder launch reaches its response tail without an execution marker. The response now carries null counts, reports `partial`, names the missing execution as a phase failure, and refuses `agent-record-failed` with the remedy when no earlier phase refused first.
- 1e911d7: Name the agent's documented permission flag when a run with no pass-through arguments produces no review artifact.
- 5c1345d: Human output on stdout is now inert too. Finding paths and headings, spec items, and host text a PR author chose reach `report`, `next`, `show`, `threads`, `drain`, and `gate` with every control character (ESC, OSC 52, CSI, C1, CR, U+2028) and every bidi control (U+202E and the rest of U+202A–U+202E, U+2066–U+2069, U+200E/U+200F, U+061C) folded to a space, so they can no longer write the operator's clipboard or move the cursor. The same fold keeps a bidi override out of the terminal dashboard's frames. `--json` stdout is unchanged. A newline inside a quoted path, heading, or spec item is now folded as well, so a `warn:`, `error:`, `check:`, or `next:` line can no longer be forged by a path like `x⏎verdict: gate passes`.
- f1989bb: `examples/typescript-client/` now runs `record` (including `--dry-run`), `post --dry-run` and `config` validation end to end against the locally built binary, and CI runs every example, so a stale example fails the build.
- 2b24ee6: `examples/typescript-client/` now also runs `gate` and `fix`, and `pkg/verdict` gains checked examples for `Post` (dry run), `Fetch`, `Gate` and `Fix`.
- c7271ae: `VerdictRefusalError` now carries the whole refusal body, so `error.problems[]`, `error.problems_truncated`, `error.candidates`, and `error.retry_at` are readable from the error instead of lost.
- 2f005e6: `verify --sandbox` closes more ways a reviewed checkout could reach its git control files. Whether a git directory lies inside the reviewed root is now decided by file identity, not spelling, so a `.git` pointer or `commondir` that spells the root in another case or through the `/System/Volumes/Data` firmlink is refused. A symlinked pointer or `commondir` is refused before it is read, links resolve one hop at a time, and a git directory that is or contains the reviewed root is refused. A control file with extra hard links, or that is a symlink into the root, is refused at setup; the object files `git clone --local` hardlinks and links under `.git/hooks/` stay allowed. The manual and SECURITY.md now note that on macOS a sandboxed command can still read the environment of other non-hardened processes you run.
- 95dc0a7: `verify --sandbox` now protects nested git repositories. Before the command runs it finds every nested `.git` in the reviewed root (a submodule with its own git directory, a nested clone) and, on Linux, every submodule path in the index, and makes each read-only, so a sandboxed command can no longer plant a hook in `sub/.git/hooks`. A nested `gitdir:` file is followed and its git directory protected on both platforms. A reviewed root reached through a symlink is resolved first. **Behavior change:** on Linux, a missing submodule path or an unreadable index (a split index, or over 64 MiB), and on either platform a tree of more than 200,000 directories, now refuses `sandbox-unavailable`.

## 0.7.0

### Minor Changes

- cc6bfbf: **What's new in 0.7.0**

  Verdict 0.7.0 is a breadth release. It adds a local code index, a read-only `serve` surface with a browser
  UI, and a backtest harness that mines past bug fixes from your own history, replays them against your
  agent, and scores the result. Review output gains an auditable provenance receipt, diff-coverage
  attestation, advisory risk scoring, and an optional validation pass that floors each finding on the
  evidence behind it, while the CI gate now checks coverage by default. Posting grows a secret guard, honest
  per-host capability reporting, and a summary-comment option, GitLab and Forgejo join GitHub as full publish
  targets, and lens packs, declared recipes, and reported verification make the review pipeline itself data.
  Thirteen pre-1.0 changes alter behavior — each names the migration step below — and the known limits close
  the notes.

- cc6bfbf: **New commands and flags**

  - **`pr-wait`** — wait for a PR to merge, close, change checks, or change review state, with a bounded timeout and typed timeout data. [Bounded foreground waits](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#bounded-foreground-waits) <!-- changesets: bounded-foreground-waits.md -->
  - **`ingest`** — import SARIF 2.1.0 results into a bundle's findings store, sharing one dedupe and suppression path with model reviews. [Static-analysis ingestion](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#local-review-no-pr) <!-- changesets: ingest-sarif.md -->
  - **`threads resolve`** — close the review threads whose findings your store marks addressed, one receipt per run, with `--dry-run` and typed refusals for threads verdict did not author. [Guarded submission](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#guarded-submission-and-operator-surfaces) <!-- changesets: thread-resolve.md -->
  - **`risk`** — rank every local bundle into an advisory triage queue by band and 0–100 score, without a host call or an index build. [Triage risk queue](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#triage-risk-queue) <!-- changesets: risk-queue.md -->
  - **`report --html`** — export the six dashboard views as a static site, published through staging and backup siblings with rollback and symlink refusals. [Static dashboard export](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#static-dashboard-export) <!-- changesets: report-html.md -->
  - **`run --agent`** — run your own agent CLI against an existing bundle, assert exactly one fresh `review.json`, then hand off to the unchanged record/post validation. [Your agent, launched](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#your-agent-launched-run---agent) <!-- changesets: run-agent.md -->
  - **`panel`** — plan independent verifier dispatches locally; keep-biased results enrich findings and summaries without voting or dropping anything. [Panel verification](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#panel-verification) <!-- changesets: panel-verification.md -->
  - **Recipes** — `.verdict/recipes.json` declares a lens set, synthesizer hint, and budget as strict versioned data, read at the PR base and hashed into the bundle. [Recipes](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#recipes-declared-review-pipelines) <!-- changesets: recipes-format.md -->
  - **`skill show`, `skill status`, `skill uninstall`** — inspect one payload file byte-identically, gate on drift with an exit code, and remove only the files a receipt owns. [Install routes](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#install-routes-and-where-commands-run) <!-- changesets: skill-management.md -->
  - **`post --summary comment`** — maintain one separate PR-level summary comment, updated in place across all three publish hosts; the default `off` keeps output byte-identical. [Guarded submission](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#guarded-submission-and-operator-surfaces) <!-- changesets: post-summary-comment.md -->
  - **`fix --all`** — the batch form of the bounded fix loop: every open inline finding at or above `--min-severity`, one pass per sig under the same ceilings, per-sig outcomes in `data.batch`, and a `fix.patch` written beside the ledger. [Fix loops](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#fix-loops-bounded-verify-gated) <!-- changesets: fix-all-batch.md -->
  - **`fix --selected`** — address exactly the sigs a human 👍 selected on the root verdict comment, read through one shared reaction reader; a selection file is written only when something was reacted to. [Fix loops](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#fix-loops-bounded-verify-gated) <!-- changesets: reaction-scoped-fix.md -->
  - **`handoff`** — one markdown projection of the open inline findings `fix` would take, selecting through the same selector, with `--json`, `--format md`, and repeatable `--paths`; read-only, with no ledger and no host call. [Handoff](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#handoff-the-fix-brief-for-someone-else) <!-- changesets: handoff-verb.md -->
  - **`run --print --stage <n>`** — render one recipe stage at a time, so CI can dispatch the four stages under different models. [Recipes](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#recipes-declared-review-pipelines) <!-- changesets: run-print-stage.md -->
  - **`backtest reverts`** — record the reverts git already knows about into a sorted, byte-stable `.verdict/reverts.jsonl`, with `.verdict/reverts.md` as the fenced reading surface. [Reverts](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#reverts) <!-- changesets: backtest-reverts.md -->
  - **`backtest mine`** — mine SZZ candidates from a local clone with zero model calls and zero network, writing a timestamp-free `mine.json`. [Backtest](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#backtest-szz-candidates) <!-- changesets: backtest-mine.md -->
  - **`backtest replay`** — replay mined candidates against your own agent in a throwaway worktree per candidate, bounded by review count, projected budget, and timeout. [Replay](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#replay) <!-- changesets: backtest-replay.md -->
  - **`backtest score`** — grade a replay's findings against the candidate bug lines and report a catch rate, off-target findings per PR, and cost per review as a pure read. [Score](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#score) <!-- changesets: backtest-score.md -->
  - **`learn --incident`** — attach one revert or incident to a recorded bundle in a sorted, idempotent `.verdict/incidents.jsonl` ledger. [Knowledge base](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#knowledge-base-scoped-local-rules) <!-- changesets: incident-memory.md -->

- cc6bfbf: **Local code index**

  - **`verdict index`** — build a local Go index with the standard library, stored by tree SHA so an empty commit reuses a tree and a rename parses nothing. [Code index](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#code-index) <!-- changesets: index-callers.md -->
  - **Graph queries** — `index tests-for`, `index co-changed`, and `index owners` answer candidate tests, co-change partners, and the CODEOWNERS rule at a tree. [Code index](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#code-index) <!-- changesets: index-graph.md -->
  - **Three more languages** — the index now reads TypeScript, JavaScript, and Python through two dependency-free lexical scanners, with no tree-sitter and no cgo. [Code index](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#code-index) <!-- changesets: index-scanners.md -->
  - **`index blast-radius`** — one change's reach: callers to depth 2 plus candidate tests, co-change partners, and owners for the changed paths. [Code index](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#code-index) <!-- changesets: index-blast-radius.md -->
  - **Transitive tests** — `tests-for` walks the reverse import graph up to `--depth` hops and labels tests it reached through imports. [Code index](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#code-index) <!-- changesets: index-transitive-tests.md -->
  - **`fetch --index`** — hand an index you already built to `fetch` or `fetch-local` and the bundle gains an `impact` artifact and a fenced `## impact` section. [Impact hand-off](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#impact-hand-off-fetch---index) <!-- changesets: fetch-index-impact.md -->
  - **Missing-index hints** — `index blast-radius` and `fetch` now name the revision and tree an absent index is about, with the exact shell-quoted build step that answers for it. [Code index](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#code-index) <!-- changesets: blast-radius-index-hint.md -->
  - **Scanner fixes** — four corpus bugs are fixed: a transitive test no longer disappears, relative imports resolve, a Python attribute call is no longer misattributed, and a Go selector outside its package is counted unresolved. [Code index](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#code-index) <!-- changesets: index-corpus-bugs.md -->
  - **One impact budget** — the impact section's byte budget is shared across symbols, callers, and tests, each list carrying its own omitted count. [Code index](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#code-index) <!-- changesets: index-impact-budget.md -->

- cc6bfbf: **Review quality and provenance**

  - **Explicit context** — `fetch --context` and `fetch-local --context` attach named context files, and findings cite them. [Explicit context](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#explicit-context) <!-- changesets: context-sources.md -->
  - **Coverage basis** — every coverage read now names what its percentage counts: full hunk spans, both sides and context, not just changed lines. [Diff coverage](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#diff-coverage-agent-attestation) <!-- changesets: coverage-scoring-basis.md -->
  - **Coverage attestation** — `review.json` gains an optional `coverage[]` checked against the bundle's own hunks, with `--require-coverage` to refuse a partial claim and one body line on a partial attestation. [Diff coverage](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#diff-coverage-agent-attestation) <!-- changesets: diff-coverage-attestation.md -->
  - **`ranges: null` is malformed** — a coverage claim whose `ranges` is present but not a string now counts as unattested instead of as the whole file. [Diff coverage](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#diff-coverage-agent-attestation) <!-- changesets: null-ranges-malformed.md -->
  - **Ignore overrides** — repeatable `--ignore <glob>` and `--no-default-excludes` on fetch, both recorded in the exclusion receipt. [Exclusions](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#exclusions-and-policy-silence) <!-- changesets: ignore-overrides.md -->
  - **Suppression receipts** — excluded diff bytes and withheld findings are receipted, with `post --min-severity` and `report --include-withheld` to read them. [Exclusions](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#exclusions-and-policy-silence) <!-- changesets: ignore-suppression.md -->
  - **Proposed learnings** — propose inactive one-rule packs from resolved threads, accept them through the pack importer, or reject them permanently with a reason. [Proposed learnings](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#proposed-learnings) <!-- changesets: learnings-propose.md -->
  - **Rule import** — `learn --pack <dir> --id <rule>` imports a structured rule object and keeps its pack provenance and hash. [Import a rule object](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#import-a-rule-object) <!-- changesets: rule-pack-import.md -->
  - **Rule exposure** — `learn --status` gains `recalled_in`, `cited_in`, `exposure`, and `acceptance` per rule, reporting unmeasured recall as null rather than zero. [Knowledge base](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#knowledge-base-scoped-local-rules) <!-- changesets: rule-exposure.md -->
  - **Risk scoring** — an optional `risk` claim in `review.json` plus `report --risk` and `report --max-risk`, which reports the larger of your claim and a deterministic floor. [Triage risk queue](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#triage-risk-queue) <!-- changesets: risk-score.md -->
  - **Reaction proposals** — opt-in reaction proposals from posted comments, plus per-rule acceptance and false-hit rates. [Reaction proposals](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#reaction-proposals-and-rule-rates) <!-- changesets: reactions-trend.md -->
  - **Resolution metrics** — live resolution rollups, clean-review metrics, and calibration receipts, with clean reports getting their own headline. [Resolution and clean reviews](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#resolution-and-clean-reviews) <!-- changesets: resolution-rate.md -->
  - **Withheld capabilities** — `post.json` records, per capability, what a host could not carry and why, so a degraded publish stays auditable. [Guarded submission](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#guarded-submission-and-operator-surfaces) <!-- changesets: withheld-capabilities.md -->
  - **Payload receipts** — a post payload receipt now hashes the rendered review content and the reviewed head, not diff positions or range metadata. [Guarded submission](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#guarded-submission-and-operator-surfaces) <!-- changesets: post-content-digest.md -->
  - **Partial publication** — post only the comments and summary parts missing from the fetched remote content, and rearm at a new head with `rearmed` and `previous_head` recorded. [Guarded submission](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#guarded-submission-and-operator-surfaces) <!-- changesets: post-content-recovery.md -->
  - **Live authority** — post refreshes live reviews and comments first and refuses without writing when that read fails, so a retry no longer needs a re-fetch. [Guarded submission](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#guarded-submission-and-operator-surfaces) <!-- changesets: post-live-authority.md -->
  - **Author fingerprint** — `fetch` writes an advisory `author.json` (`{agent, model, confidence, evidence[]}`) from five harness signals, suggests a reviewer from another model family, and `record.json` carries the author/reviewer pair that `trend` tallies. [Bundle namespace](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#bundle-namespace-core-files-vs-x-) <!-- changesets: author-routing.md, author-routing-hint.md, author-routing-record.md, author-routing-skill.md, author-routing-trend.md -->
  - **Revert memory** — a changed path that matches the tracked reverts ledger gives `fetch` a bounded, fenced `reverts.txt`, and the review prompt treats a revert as evidence the change broke something once. [Reverts](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#reverts) <!-- changesets: fetch-reverts.md -->
  - **Validation pass** — a trusted `prompts/validate.md` pass writes `validation.json` per sig, `rigor_policy` floors each severity tier, and `post --require-rigor` narrows it per invocation: advisory by default, a receipted withhold in gate mode. [Rigor floors](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#rigor-floors-and-the-validation-pass) <!-- changesets: rigor-validation.md -->
  - **Published key names** — `prompts/review.md` tags the evidence rung as `rigor` and the in-bounds flag as `in-bounds`, and an archtest holds every published key to `review.schema.json`. [Rigor floors](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#rigor-floors-and-the-validation-pass) <!-- changesets: prompt-rigor-key.md, prompt-key-shapes.md -->
  - **Verdict reconciliation** — binding verdicts reconcile separately from summary and inline content, so a retried approval does not duplicate a GitLab summary note. [Submit per host](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#submit-post-per-host) <!-- changesets: post-verdict-reconciliation.md -->
  - **Plain-body Basis line** — `record` leaves a `review.json` body with no leading quote block byte-identical and reports no `basis-replaced` warning, because there is no confidence lead to insert under. [Review-input problems](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#review-input-problems) <!-- changesets: sta-425-basis-plain-body.md -->
  - **Array-valued coverage ranges** — `next --coverage` refuses a `coverage[].ranges` array on plain and chunked bundles with the same `bad-review-input` problem `record` reports, instead of reading it as a silent 0% attestation. [Diff coverage](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#diff-coverage-agent-attestation) <!-- changesets: sta-425-coverage-ranges.md -->
  - **Passing gate next step** — a passing `report --check <tier>` names `merge — gate passes` in the envelope's `next` and human footer, leaving `address open threads` for a failing gate. [Machine surface](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#machine-surface) <!-- changesets: sta-425-gate-next.md -->
  - **More generated paths** — `*.gen.*` and `zz_generated.*` now count as generated in the shared role heuristic, so `next --coverage` and the risk floor exempt them like the other generated patterns. [Diff coverage](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#diff-coverage-agent-attestation) <!-- changesets: sta-425-generated-exemption.md -->

- cc6bfbf: **Agents and MCP**

  - **Bounded repair loop** — `run --rounds N` re-prompts an agent with the shape checker's fix list, stops on the first valid round, and refuses `repair-exhausted`. [Your agent, launched](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#your-agent-launched-run---agent) <!-- changesets: run-repair-loop.md -->
  - **Self-review loop** — a bounded local self-review recipe for Claude, Codex, and OpenCode with round receipts and coverage bound to the verified diff identity. [Agent self-review loop](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#agent-self-review-loop) <!-- changesets: self-review-loop.md -->
  - **MCP config for six more hosts** — `mcp config` writes snippets for six more hosts and carries `--read-only` or `--operator` into the argv it prints. [Host config](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#host-config) <!-- changesets: mcp-config-hosts.md -->
  - **Declared mutation classes** — every MCP tool states its local mutation class, and a call hidden by `--read-only` returns the typed `read-only-tool` refusal. [MCP server](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#mcp-server-stdio-experimental-until-10) <!-- changesets: mcp-mutation-class.md -->
  - **Agent preflight** — a missing agent binary is caught before prompt assembly, with install guidance, and printed or recorded pass-through arguments are limited to safe model and cost options. [Agents, arguments, and `--print`](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#agents-arguments-and---print) <!-- changesets: agent-definition-display.md -->

- cc6bfbf: **Hosts and CI**

  - **Forgejo publish** — `post` submits one atomic Forgejo review with the body, verdict, reviewed commit, and every inline finding, and withholds what the host cannot anchor rather than guessing. [Submit per host](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#submit-post-per-host) <!-- changesets: forgejo-post.md -->
  - **GitLab publish** — each finding becomes one positioned discussion, the review body a summary note, and an approval verdict a real MR approval. [Submit per host](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#submit-post-per-host) <!-- changesets: gitlab-publish.md -->
  - **GitLab incremental** — `fetch --provider gitlab --base-sha` writes the same `incremental.diff` contract the GitHub compare path writes, and hosts without a compare adapter say so in a note. [Providers](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#providers-non-github-hosts) <!-- changesets: gitlab-incremental.md -->
  - **GitLab readiness and calibration** — `ready` advises on merge requests and plain `calibrate` joins live GitLab threads through `glab`, on the same blocker and outcome vocabulary as GitHub. [Calibration](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#calibration-predictions-vs-ground-truth) <!-- changesets: ready-calibrate-gitlab.md -->
  - **`doctor --provider`** — preflight one host at a time, with a per-host probe table and credentials reported by presence, never by value. [Doctor preflight](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#doctor-preflight) <!-- changesets: doctor-provider-adopt-gitlab.md -->
  - **GitHub Action** — the review and merge gate ships as a first-party composite action that installs a pinned verified release and uses your runner, your agent, and your keys. [The runner is your CI](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#the-runner-is-your-ci) <!-- changesets: github-action.md -->

- cc6bfbf: **Serve and the browser UI**

  - **`verdict serve`** — a read-only HTTP surface over the current repository, loopback by default, with a session-cookie exchange, strict CSP, Host and Origin gates, and per-response source traceability. [Serve](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#serve-local-http-surface) <!-- changesets: serve-s1.md -->
  - **Root discovery** — `serve --root` finds nested repositories under a bounded traversal backed by a local 24-hour registry, and reports cache, truncation, and skipped patterns. [Serve](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#serve-local-http-surface) <!-- changesets: serve-s2-discovery.md -->
  - **Cost and precision buckets** — `/v1/cost` adds repository, ISO-week, and model buckets and `/v1/precision` adds category and tier counts, keeping unmeasured prices null and marking corrupt aggregates partial. [Serve](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#serve-local-http-surface) <!-- changesets: serve-s3-cost-precision.md -->
  - **Browser UI** — `serve` opens an embedded UI with inbox, review, threads, cost, precision, and gate views, dark mode, live refresh, and static `data.json` reading. [Serve](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#serve-local-http-surface) <!-- changesets: serve-ui.md -->
  - **Live invalidation** — bundles invalidate as they change, with `--interval`, `--sweep`, and `--no-watch` to control it. [Serve](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#serve-local-http-surface) <!-- changesets: serve-live-updates.md -->
  - **Vanished thread feeds** — a thread feed removed mid-read now reads as absent instead of corrupt. [Serve](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#serve-local-http-surface) <!-- changesets: vanished-threads.md -->
  - **Cache content budget** — serve cache hashing is bounded to 16 MiB per bundle lookup and bypasses the cache when the budget is exceeded. [Serve](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#serve-local-http-surface) <!-- changesets: cache-content-budget.md -->
  - **Small-file rewrites** — artifacts up to 64 KiB are hashed, so a rewrite that preserves size and mtime is still detected. [Serve](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#serve-local-http-surface) <!-- changesets: cache-small-content.md -->
  - **Inventory accounting** — serve inventory accounting stays inside its byte budget even for oversized artifacts. [Serve](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#serve-local-http-surface) <!-- changesets: inventory-budget-exact.md -->

- cc6bfbf: **Lens packs**

  - **Built-in lenses as data** — `perf`, `api-contract`, and `test` now ship as a pack resolved into every bundle as `lenses.txt`, with the four hashed prompt files byte-identical. [Lens packs](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#lens-packs) <!-- changesets: lens-packs-s1.md -->
  - **No per-lens cap** — a lens returns every finding its evidence supports: the built-in pack declares only `max_files: 12`, and the dispatch template leads with the lens's domain. [Lens packs](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#lens-packs) <!-- changesets: lens-dispatch-deltas.md -->
  - **`pack add` and a lock** — vendor a pack from a directory or git source, validated before it loads and pinned in a strict `.verdict/verdict.lock`. [Lens packs](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#lens-packs) <!-- changesets: packs-lock.md -->
  - **Layered packs** — `pack list` reports every loaded pack with its layer, source, tree hash, ids, and drift, and a repo pack is read from the trusted base so a PR cannot edit the pack that reviews it. [Lens packs](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#lens-packs) <!-- changesets: pack-layers.md -->
  - **`pack test`** — run a pack's eval cases offline. [Lens packs](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#lens-packs) <!-- changesets: pack-test.md -->
  - **Leftover backup sibling** — a failed sweep of a previous tree is reported as success plus a warning naming the leftover, instead of refusing an add whose new tree is already durable. [Lens packs](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#lens-packs) <!-- changesets: pack-add-backup-warning.md -->
  - **Stale staging** — a crashed re-add's leftover staging or backup sibling refuses `pack-staging-stale` with the path to delete. [Lens packs](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#lens-packs) <!-- changesets: pack-staging-stale.md -->

- cc6bfbf: **Measurement**

  - **Clean controls** — the benchmark corpus accepts clean controls with empty ground truth, ships two seeded clean cases, and keeps seeded fixtures labeled as not a model-quality claim. [Benchmark corpus](https://github.com/stainless-code/verdict/blob/main/docs/benchmark.md) <!-- changesets: benchmark-clean-controls.md -->
  - **Reproducible metrics** — typed severity weights, ordered k-of-n recall, control false-positive rates, weighted F1, signal-to-noise, and explicit denominators, with provenance and argv recorded. [Benchmark corpus](https://github.com/stainless-code/verdict/blob/main/docs/benchmark.md) <!-- changesets: benchmark-controls-metrics.md -->
  - **Adjudicated rates** — a versioned per-run adjudication record reclassifies an unmatched finding as `real-unlisted`, `matches-ground-truth`, or `false-positive`, and `bench/runner -render` recomputes strict and adjudicated precision and recall without re-running a model. [Benchmark corpus](https://github.com/stainless-code/verdict/blob/main/docs/benchmark.md) <!-- changesets: bench-adjudication.md -->
  - **First real cases** — four real cases from `urfave/cli` (three bugs and one clean control), `-max-usd` stops scheduling at measured spend and marks the run uncitable, and refused samples are counted in the cost rollup. [Benchmark corpus](https://github.com/stainless-code/verdict/blob/main/docs/benchmark.md) <!-- changesets: bench-real-pilot.md -->
  - **Corpus grading guard** — a keyword the case's own diff spells refuses, a case left with no phrase asks for another keyword, and a real bundle whose `full.diff` is missing, unreadable, or prose is refused instead of graded leniently. [Benchmark corpus](https://github.com/stainless-code/verdict/blob/main/docs/benchmark.md) <!-- changesets: bench-keyword-guard.md, bench-corpus-followups.md -->

- cc6bfbf: **Breaking changes (pre-1.0)**

  Package is on `0.x`, so these ship as a minor bump; each one names the step that migrates it.

  - **`index blast-radius` truncation shape** — the integer `truncated` count and `depth_truncated` are replaced by the shared boolean receipt (`truncated`, `truncation_reason`, `returned`, `total`, `next_offset`), and a complete result omits the receipt instead of carrying `truncated: false`. _Migrate:_ read `truncation_reason` and `returned`; treat an absent receipt as complete. [Code index](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#code-index) <!-- changesets: typed-truncation.md -->
  - **`gate` checks coverage by default** — the new `coverage` dimension fails when the gated HEAD has no receipt, a receipt for another HEAD, no `coverage[]` attestation, or under 100% attested. _Migrate:_ `--coverage warn`, or the SDK `Coverage` field, keeps the old passing behavior while reviews attest nothing. [Gate](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#gate-one-composed-exit-code-for-ci) <!-- changesets: gate-coverage-default.md -->
  - **`gate --live` reads every host** — a non-GitHub bundle used to pass the live dimension without reading threads; it now reads the bundle's own host and fails closed when it cannot. _Migrate:_ `--live auto` warns and gates on the local dimensions only. [Gate](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#gate-one-composed-exit-code-for-ci) <!-- changesets: gate-live-all-hosts.md -->
  - **Unknown gate tiers refuse** — an unrecognized tier is `bad-flags` on the MCP `report` tool and in `Report.Check`/`Gate.MaxSeverity`, not only on the CLI. _Migrate:_ send a tier from the published set, or omit the field. [Gate](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#gate-one-composed-exit-code-for-ci) <!-- changesets: gate-tier-validation.md -->
  - **`verify` child environment is default-deny** — only baseline locale, terminal, shell, XDG, and toolchain names pass, and a secret-shaped name is dropped even when it matches an allowlist prefix. _Migrate:_ list what you need in `.verdict/verify-env` or `--env-allowlist`; dropped names appear in `warnings[]`. [Child environment](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#child-environment-what-inherits-what-is-explicit) <!-- changesets: verify-env-allowlist.md -->
  - **`verify` refuses command shapes it used to run** — `wget --remote-name`/`-i`, `curl -K`, `busybox wget`, `pip3 download <url>`, `telnet`, `git archive --remote`, and the rest refuse `verify-networked` in attached and clustered spellings, and a command word built from a variable (`E=rm; $E -rf ./target`, including the braced, quoted, `export`ed, loop, and wrapper spellings) refuses `verify-indirection`. _Migrate:_ write the tool word plainly and rewrite the command to a plain fetch your recipe can run; every refusal keeps its `try:` hint. [Known residuals](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#known-residuals-string-scan-ceiling) <!-- changesets: verify-static-gate-bypasses.md, verify-indirection.md -->
  - **`.verify.out` is owner-only** — the verify stream is written `0600` on Unix where it was `0644`, because a command that prints a credential leaves the value in it. _Migrate:_ read it as the user that ran `verify`, or copy the bytes inside that same step; on Windows the file keeps the directory's inherited ACL. [Enforced sandbox](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#enforced-sandbox-verify---sandbox) <!-- changesets: verify-out-owner-only.md -->
  - **Bundle paths are guarded** — a bundle name that is not one path element (`a/b`, `..`, `CON`, `x:`) refuses `bad-flags`, and a symlinked bundle root or component refuses before any write, including a link that points inside the repository. _Migrate:_ one plain name per bundle, and a real directory where a link used to work. [Bundle namespace](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#bundle-namespace-core-files-vs-x-) <!-- changesets: bundle-name-guard.md, managed-parent-symlinks.md, write-bundle-symlinks.md -->
  - **Provider is inferred from origin** — with no `--provider` flag, an unknown or conflicting origin now refuses `provider-unknown` instead of defaulting to GitHub. _Migrate:_ pass `--provider` explicitly, or keep a plain GitHub origin. [Providers](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#providers-non-github-hosts) <!-- changesets: provider-from-remote.md -->
  - **`capabilities` splits, `truncated` widens** — the binding verdict becomes `approve` and `request_changes` with `review_verdicts` kept as the compatibility sum, `unsupported` names each absent capability, and `data.truncated` widens from `boolean` to `boolean | row[]`. _Migrate:_ branch on the two new flags, and narrow `truncated` before you read it as a boolean. [Published schemas](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#published-schemas--drift-rule) <!-- changesets: honest-capabilities.md, provenance-receipt.md -->
  - **`pack add` source allowlist** — only `https://host/path` with no userinfo, `ssh://user@host/path`, and `user@host:path` are accepted; anything else refuses before a git call, and a lock row carrying one refuses at `pack list`, `fetch`, `post`, and `record`. _Migrate:_ rewrite the source in one of the three forms. [Lens packs](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#lens-packs) <!-- changesets: pack-source-allowlist.md -->
  - **Benchmark cases name their commits** — a real corpus case now requires `provenance.base_sha` and `provenance.head_sha` (40-hex), the two commits the reviewed diff spans, and a bundle stamps the same pair. _Migrate:_ add both commits to every real `case.json`; a case without them fails validation. [Benchmark corpus](https://github.com/stainless-code/verdict/blob/main/docs/benchmark.md) <!-- changesets: bench-real-pilot.md -->
  - **Rebuild indexes** — indexes written by the previous scanner over-report callers, so rebuild them. _Migrate:_ `verdict index build --at HEAD`. [Code index](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#code-index) <!-- changesets: index-import-shadow.md -->

### Patch Changes

- cc6bfbf: **Safety and trust**

  - **Tokens off the argv** — Forgejo and Bitbucket reads hand the credential to curl on stdin or a 0600 file, so it is gone from `ps` and `/proc/<pid>/cmdline`. [Providers](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#providers-non-github-hosts) <!-- changesets: curl-token-argv.md -->
  - **Secret guard** — `post` refuses a secret-shaped value before the first host call, naming the shape and never the value, with `--allow-secret-values` as the explicit exception. [Guarded submission](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#guarded-submission-and-operator-surfaces) <!-- changesets: post-secret-guard.md -->
  - **Mentions neutralized** — `record` escapes `@`-mentions once, where the body is built, so recorded, posted, dry-run, and digested bytes cannot diverge. [Mentions](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#mentions-in-the-published-text) <!-- changesets: mention-neutralization.md -->
  - **Punctuated mentions** — `(@alice)`, `**@alice**`, and `_@alice_` are escaped while real addresses stay byte-identical. [Mentions](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#mentions-in-the-published-text) <!-- changesets: punctuated-mentions.md -->
  - **Strikethrough mentions** — `~~@alice~~` is escaped too, while a lone tilde in prose stays as written. [Mentions](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#mentions-in-the-published-text) <!-- changesets: strikethrough-mentions.md -->
  - **Learning evidence** — cited paths and comment identifiers must stay inside quoted learning evidence, and unsafe ones refuse before a learning is proposed or accepted. [Learnings](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#learnings-durable-repo-memory) <!-- changesets: learnings-evidence.md -->
  - **Unicode line breaks** — U+2028 and U+2029 now refuse a serve URL, refuse a learning path, and flatten in a rendered `pack test` line. [Serve](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#serve-local-http-surface) <!-- changesets: line-break-guards.md -->
  - **Store locking** — every `.verdict/kb.jsonl` write takes one advisory lock; a second writer refuses `store-busy` instead of dropping a row. [Findings store](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#findings-store--triage-authority) <!-- changesets: store-lock-and-listing.md -->
  - **Enforced sandbox** — `verify --sandbox` runs the verify command under bubblewrap on Linux with no network, a private PID namespace, empty `~/.ssh`/`~/.aws`/`~/.docker`, and no silent fallback. [Enforced sandbox](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#enforced-sandbox-verify---sandbox) <!-- changesets: verify-sandbox.md -->
  - **macOS sandbox** — `verify --sandbox` now denies every `bind()` and `connect()` on macOS through the built-in `sandbox-exec`, loopback and unix sockets included, so filesystem isolation stays Linux-only. [Enforced sandbox](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#enforced-sandbox-verify---sandbox) <!-- changesets: verify-sandbox-macos.md -->
  - **Variable command words refuse** — `E=rm; $E -rf ./target` and the braced, quoted, `export`ed, loop, concatenated, and own-line spellings now refuse `verify-indirection`, including after `env`/`timeout`/`xargs` wrappers and inside `sh -c`. [Known residuals](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#known-residuals-string-scan-ceiling) <!-- changesets: verify-indirection.md -->
  - **`.verify.out` is owner-only** — the verify stream is written `0600` on Unix, like `.run.out`, because a command that prints a credential leaves the value in it. [Enforced sandbox](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#enforced-sandbox-verify---sandbox) <!-- changesets: verify-out-owner-only.md -->
  - **Launch-record redaction** — a secret-shaped `run --agent` operand no longer reaches the recorded `output_tail` or the 0644 child log. [What `run` asserts](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#what-run-asserts) <!-- changesets: run-argument-credential.md -->
  - **setup-verdict hardening** — the action passes its inputs through environment variables and verifies a cached archive before reuse, failing closed on a tampered one. [The runner is your CI](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#the-runner-is-your-ci) <!-- changesets: setup-verdict-hardening.md -->
  - **Install preflight** — `skill install` probes exactly the directories it will write before the first byte, so a read-only target leaves nothing half-installed. [Install routes](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#install-routes-and-where-commands-run) <!-- changesets: skill-install-preflight.md -->

- cc6bfbf: **Fixes**

  - **Read-verb refusals** — `report`, `next`, and `show` refusals carry a shell-quoted `re-enter:` line naming the invocation you typed. [Machine surface](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#machine-surface) <!-- changesets: read-verb-re-enter.md -->
  - **`next --coverage` with no positionals** — leads with one `error:` line naming what is missing, and a plain `Basis:` line is accepted without a warning. [Machine surface](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#machine-surface) <!-- changesets: next-usage-basis-plain.md -->
  - **Unnamed cost models** — `record` warns once per `cost.json` `models[]` entry with no `name`, and still accepts and prices the record. [Cost control](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#cost-control-budget-estimate-consent) <!-- changesets: record-cost-name-warnings.md -->
  - **Over-cap and missing-index refusals** — `diff-too-large` and `missing-index` now carry a `next:` line naming the exclusions that fit, and the exact build command. [Over-cap diffs](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#over-cap-diffs-chunked-fetch--manual-recipe) <!-- changesets: fetch-refusal-next-steps.md -->
  - **Five small repairs** — an agent that fails just before its budget reads `agent-failed`, MCP refusals carry their warnings, `index-capped` names the bytes it measured, `index status` points at the query, and a crashed agent's partial review can no longer block on a FIFO. [Machine surface](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#machine-surface) <!-- changesets: review-residuals.md -->
  - **Windows watchdog** — a command that failed to start and survived the cleanup kill now returns at once, naming both errors, instead of hanging forever. [Bounded foreground waits](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#bounded-foreground-waits) <!-- changesets: windows-watchdog-no-hang.md -->
  - **Quoted upgrade hints** — `verdict upgrade` quotes its project-local `cd` hint so a path with a space or quote pastes resolvably, and the uninstall sibling hint reads as a sentence. [Machine surface](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#machine-surface) <!-- changesets: upgrade-hints-quoting.md -->
  - **Release asset check** — the release script's completeness check no longer reports a shipped asset as missing when the asset list is large. [Release train](https://github.com/stainless-code/verdict/blob/main/CONTRIBUTING.md) <!-- changesets: release-asset-sigpipe.md -->
  - **`pr-wait` inconsistent reads** — a head that is not yet the same on both APIs is retried instead of failing the bounded wait, and `inconsistent_observations` keeps the retry visible. [Bounded foreground waits](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#bounded-foreground-waits) <!-- changesets: pr-wait-inconsistent-reads.md -->
  - **Windows watchdog resume** — a command that failed before starting under load retries the thread lookup instead of refusing `resume suspended verify process`. [Bounded foreground waits](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#bounded-foreground-waits) <!-- changesets: windows-watchdog-resume-retry.md -->
  - **`backtest mine` in a partial clone** — a `--filter=blob:none` clone refuses `partial-clone` with the step that fetches the missing objects, instead of stalling. [Backtest](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#backtest-szz-candidates) <!-- changesets: backtest-partial-clone.md -->
  - **Quiet local live skip** — a local bundle's live recheck writes one `info:` line to the human stream instead of a warning or a `--json` `warnings[]` entry; failed remote reads still warn. [Machine surface](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#machine-surface) <!-- changesets: sta-425-local-skip-info.md -->
  - **Optional review schema version** — `record` accepts a `review.json` without `schema_version` and reads it as version 1, while a present value other than `"1"` still refuses. [Review-input problems](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#review-input-problems) <!-- changesets: sta-425-optional-schema-version.md -->

- cc6bfbf: **Docs**

  - **Refusal code reference** — `docs/troubleshooting.md` lists every refusal code with its cause and fix, and a drift test fails the build when one is missing. [Refusal codes](https://github.com/stainless-code/verdict/blob/main/docs/troubleshooting.md) <!-- changesets: troubleshooting.md -->
  - **Docs site structure** — grouped sections with short sidebar labels, a Markdown twin for every page, and a glossary route from `verdict --help`. [docs/](https://github.com/stainless-code/verdict/blob/main/docs/README.md) <!-- changesets: docs-site-restructure.md -->
  - **Defaults at a glance** — a generated table names every default a bare run takes and the verb it shapes, so it cannot drift from the shipped binary. [Defaults](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#defaults-at-a-glance) <!-- changesets: defaults-at-a-glance.md -->

- cc6bfbf: **Known limits**

  - **The macOS sandbox covers the network half only** — `verify --sandbox` denies `bind()` and `connect()` on macOS but has no filesystem namespace, so credential paths, `/proc`, `/run`, and a private `/tmp` are hidden only on Linux under `bubblewrap`; filesystem isolation on macOS needs a Linux VM or a container running that recipe. [Enforced sandbox](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#enforced-sandbox-verify---sandbox) <!-- changesets: verify-sandbox-macos.md -->
  - **The string gate is a tripwire, not a boundary** — `sh "$V"`, a script named by a variable, still passes the pre-exec scan, which is why `verify --sandbox` is the boundary and a spelling outside the gate's reach is a gate gap to arm rather than a vulnerability. [Known residuals](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#known-residuals-string-scan-ceiling) <!-- changesets: verify-static-gate-bypasses.md, verify-indirection.md -->
  - **A backslash before a CRLF line ending is not joined as a continuation** — the gate refuses that shape rather than passing it, so a CRLF continuation fails safe. [Known residuals](https://github.com/stainless-code/verdict/blob/main/docs/manual.md#known-residuals-string-scan-ceiling) <!-- changesets: verify-indirection.md -->

## 0.6.1

### Patch Changes

- a5870d1: The npm wrapper and each platform package now ship the repository README, so the registry page renders the project documentation instead of an empty README.
- 4d7fb67: The package page now names what the tool does rather than a claim that stopped being true: the description drops "zero install" (every route installs a binary) for the loop it actually runs — fetch, review, gate, post — plus cost rollups and one static binary, and the package carries the docs site as its `homepage` instead of letting npm fall back to the repository README. Keywords gain `cli`, `mcp`, `go`, `llm`, `static-binary`, and the agent vocabulary, and drop the self-referential `verdict`. The docs site's own description moves to the same sentence, so the About box, npm, and the site stop disagreeing.
- 88508a4: Public surfaces now match the binary. The docs site, its home pages, and the CI review example use `.verdict/state/`; the operator manual documents deep validation, path-scoped profiles, the evidence ledger, posting guards, local review coverage, and the shipped CLI flags, and states that `verification.json` is agent attestation rather than tool proof; the TypeScript client throws a typed `VerdictGateError` for a gate-shaped exit 1 instead of a schema error; the Python smoke uses the real bundle path and runs in CI; the Go SDK exposes record dry-run/strict/cost-names and fetch/fetch-local head/worktree controls; `config --explain` human output shows privacy exclusions and plan group ids; `spend-capped` carries a hint; and the README says the Go SDK runs in-process.

## 0.6.0

### Minor Changes

- e59d899: **Breaking (cost record):** `cost.json` accepts one `models` shape: an array of entries. A record whose `models` is an object is no longer read by key order — that coercion dropped the keys, which are the only place the writer named a model, so the entry priced without anything able to attribute it. Such a record now reads as unreadable spend, named by the existing `cost.json unreadable — spend unrecorded for this rollup` warning, and the run still completes. Every record the shipped `prompts/review.md` template writes already carries the array form, so no template and no documented recipe changes. An existing object-valued record migrates by rewriting it as `"models": [{"name": "<panel slug>", "model": "<model id>", "input_tokens": N, "output_tokens": N}]`; `record --cost-names` will not convert it, because converting would restore the shape the reader just dropped.
- c48c94d: `record --cost-names[=<names>]` names the `models[]` entries of a bundle's cost record and its per-chunk records, in place and without changing a count. Names come from the flag's own ordered list — synthesizer first, then lenses — or from a panel pinned in the bundle; with neither, the repair refuses rather than inventing a name from a slug.

  An entry that already names its seat keeps that name. A name that sits on the wrong seat, repeats, or comes from outside the panel refuses with a code naming the entry and the seat it should hold, and an entry the panel cannot explain refuses rather than being left half-attributed. A document holding several records gives each record the whole panel, never a share of it.

  The repair plans every cost file before writing any, and it runs after the record verb's other checks, so no refusal rewrites a cost record. A cost file that changed after it was planned refuses rather than replacing the newer write, and a record that grew past the reader's 8 MiB cap refuses before anything is written. A write that fails mid-pass stops with the files already written named in the refusal, and re-running finishes the repair.

  This verb is how an operator repairs the array that the same release made the only shape: a record whose `models` is an object is `cost-models-not-array` here rather than converted, because converting would restore the second shape the reader just dropped. Migrate by writing an array whose entries name their seat — the migration note sits with the array-shape change, and this verb is its repair path.

- b7761d2: Lens certification becomes version-scoped, and aggregation stops refusing records it can read. A cost record offered as `schema_version: "2"` may carry only the top-level fields v2 defines: an extra field now refuses by name and version (`carries top-level field "notes", which that version does not define — remove the field; no version this build certifies defines it`) instead of a generic "unreadable" sentence, a record bound to the run at a version this build does not certify refuses as `version-unsupported` rather than being re-read as v2, and a `schema_version` no reader can parse (`null`, a boolean, an array, an object, an empty string) is `malformed` instead of counting as absent — so an unreadable declaration can never let an older certified record stand in for the newest binding. The group estimate and the cost rollup take the other side of the split: they price the fields they know and ignore top-level fields a newer writer added, so one run's stray key no longer voids the history an estimate is built from — that closes a `budget-unknown` refusal under `require_known_estimate` that a single unregistered field could previously trigger. Nested shapes stay strict, and every fail-closed check still fails: foreign capture, duplicated key, unreadable entry, mismatched binding.

### Patch Changes

- 8423f7a: `report --cost` keeps a chunk's spend when the bundle's own `cost.json` is unreadable. The broken top-level record used to be folded into the same rollup as the per-chunk records, so one bad file voided every chunk beside it and reported zero tokens for a run that had real measured spend. It is now dropped on its own — named once by `cost.json unreadable — spend unrecorded for this rollup`, the same way an unreadable chunk is named — and the chunk records still price. No other shape changes: a bundle with no chunk records reads its top-level record exactly as before.
- 2b08c77: The release body is asserted, not assumed. `scripts/release.sh` now reads the published release back after GoReleaser publishes the assets, rewrites the body from `CHANGELOG.md`'s section for the tagged version when it differs, and fails the run when the rewrite does not stick or the release is incomplete. A body that differs only in the whitespace GitHub hands back is accepted rather than rewritten, and a release that is missing assets never gets a body at all. Release 0.5.0 shipped a one-byte body while the notes file held 15.9 kB and every publish step reported success; the new `body` stage closes that gap, the `release-body` eval holds it offline, and a re-run leaves an already-correct body untouched.

## 0.5.0

### Minor Changes

- 027d29a: The bench runner learns a repair loop. Pass `-loop N` to let one sample try up to N rounds: the harness runs `record --dry-run --json`, feeds the problem list back, and records only the last valid round. Round evidence lives under `runs/<slug>/round-NN`, the report gains `loops`, `fix_rates`, and `loop_cost`, and `-loop 1` keeps the old single-shot files byte-identical.
- 110dda8: Findings can now carry a closed category (`logic`, `edge-case`, `security`,
  `performance`, `accidental-commit`, `style`, `docs`, `other`): an optional
  per-comment label that never changes the finding's signature. `report
--category` filters to one class, `report --check --category` gates only that
  class, and `report`, `calibrate`, and `trend` all publish per-category counts
  (untagged for unlabelled findings). Unknown or non-string values warn once
  and store null; they never refuse a review.
- 3dbe206: Findings can now carry a repair brief: the affected sites, a one-sentence
  outcome, an optional approach and open questions. `record` validates it against
  the fetched change and keeps it across re-records, `show --json` and `fix`
  print it, and it never changes a finding's signature. A malformed brief warns
  and is dropped; it never refuses a review.
- 3bd05e9: `verdict gate` is one composed CI gate over the three dimensions `report`
  already computes: open findings at-or-above a tier (`--max-severity`), a
  completed post for the fetched HEAD (`--require-head`), and live unresolved
  threads (`--live auto|strict|skip`). One exit code carries the answer — 0 pass,
  1 fail-or-unknown, 2 usage, 3 tool error — and the human tail names each
  dimension, the tally, and one `try:` hint covering every failing dimension.
  `--json` adds `data.passed` plus `data.dimensions[]` in `findings, head,
live_threads` order with `count` null where a dimension has no tally; a failed
  gate keeps `error: null` because a verdict is not a refusal. `gate` is
  read-only (no store writes, no post) and absent from MCP: a local pass is not
  authority to merge.
- 3089864: **Breaking (experimental MCP):** a host on `verdict mcp --read-only` no longer
  sees a `threads` tool, and `report` there drops its `--output`/`--sarif`
  arguments and reports `readOnlyHint: false` instead of claiming to write
  nothing. Seven tools remain: `report`, `next`, `show`, `trend`, `ready`,
  `doctor`, `check-tree`. A host that needs the thread feed should refresh with
  `verdict threads` outside the server or run it without `--read-only`; a host
  pinning `report` as a read-only tool should stop trusting that hint.
- eb2572b: Patch identity dedupes re-reviews across rebases. `fetch` and `fetch-local` write `.verdict/state/<bundle>/identity.json` (`git patch-id --stable`, labeled `diff-sha256` fallback), `post` refuses `already-reviewed-patch` on a same-patch sibling unless `--force` (recorded in `post.json` `forced[]`), `adopt --from auto` matches exactly one local twin, and `report --completeness` passes when the posted and fetched heads differ only by a rebase with the same patch.
- cdb5a61: `verdict record` can now answer "would this record?" before writing anything.
  `record --dry-run` runs the same checks and writes nothing, returning a
  summary of the comments, sections, and anchors that would move to the body.
  Both modes share one checker instead of stopping at the first problem: the
  refusal carries `error.problems[]` in document order with a JSONPath, a
  published `code`, the level, the message, the exact `fix`, and an optional
  `example`, plus `error.problems_truncated` past the 50-problem cap; human mode
  lists the same problems before its try/re-enter lines. The checker owns the
  brief, anchor/side, kb-id, chunk-attestation, confidence, security, claim,
  triage, section, and verification-input checks, not just the top-level shape.
  Warnings (moved anchors, brief parts dropped without refusing, malformed kb
  ids, unknown fields, an unset rigor) let the record proceed unless `--strict`
  or `"strict_input": true` in `controls.json` promotes them.
  `pkg/verdict.CheckReview` exposes the pure checker, the MCP `record` tool gains
  `dry_run` (served on a read-only server; a non-dry record is still refused),
  and the review skill now loops write -> dry-run -> fix for at most three
  rounds, then reports any remaining problems instead of recording a partial
  review.
- 05125d5: `verdict record` now repairs the mechanical shapes weak models emit instead
  of refusing them. Before checking, `NormalizeReview` unwraps one
  whole-document code fence, drops prose around exactly one balanced JSON
  object, wraps a scalar where a string array belongs, renames the
  `inBounds`/`rung`/`file` aliases, folds rigor synonyms and enum case, and
  removes a BOM, an explicit null on an optional key, or an exact-duplicate
  comment. Every rewrite is a warning with its own code, listed in
  `record.json`'s `repairs[]`; `--strict`, `REVIEW_STRICT_INPUT=1`, or
  `"strict_input": true` in `controls.json` turns each into an error.
  `record --dry-run` returns the exact stored object as `data.normalized`
  plus `data.repairs`, and never rewrites `review.json` on disk. The CLI also
  owns the body's `> Basis:` line, replacing a disagreeing line (or inserting
  a missing one) with the computed counts and reporting `basis-replaced`.
  Unset rigor records as `said` with `rigor_source: default`, and length and
  numeric bounds now refuse as `out-of-range` instead of `bad-enum`.
  `pkg/verdict.NormalizeReview` exposes the pure, idempotent primitive.
- 3089864: **Breaking:** the published schemas moved with the skill, so every schema's
  `$id` base path is now `skills/verdict/schemas/` rather than `schemas/` —
  `https://github.com/stainless-code/verdict/blob/main/skills/verdict/schemas/envelope.schema.json`,
  for example. If you resolve a schema by URL, or pin `$id` in a validator, update
  the base path; the schema contents are unchanged.
- bdc9938: The Go SDK and the TypeScript client can read review-thread state in-process,
  so the one review read an embedder had to shell out for is now a method.
  `verdict.Threads` wraps the `threads` verb: `ThreadsRequest` carries the repo,
  PR, and `--provider` override, and `ThreadsResponse` returns the envelope plus
  the refreshed feed tally (`Dir`, `File`, `Provider`, `HeadSHA`, `FetchedAt`,
  `Truncated`, `Threads`, `Open`, `Outdated`, `Verdict`), with `Data`
  byte-identical to `threads --json`. The TS client gains `threads(...)`
  resolving `Envelope<DataFor["threads"]>`, and every review verb now has a typed
  client method instead of falling back to `run()`. No wire or behavior change:
  the verb, its data profile, and its MCP tool already existed.
- 9cb4bfd: `verdict skill install --detect` adds the review skill to every coding agent
  already set up on the machine, instead of making you look up one path per agent.
  It names each target and its result, and `--json` lists each target with its
  status, path, and files written. With no agent set up it refuses and points at
  `--agent <name>`, and `--detect` is mutually exclusive with `--dir` and
  `--agent`. `install.sh` and `install.ps1` now close a successful install with
  that exact command.
- 7fce8bd: The TypeScript client's verb methods now return their own `data` profile
  instead of `Envelope<unknown>`: `ready` resolves `ReadyData`, `fetch` resolves
  `FetchData`, `trend` resolves `TrendData`, and so on for every verb method.
  `DataFor` exposes the same verb → profile map, so code that passes `data`
  around can name the profile without restating it.

  Every profile is generated as a `<verb>Data` `$defs` entry in
  `envelope.schema.json` (with the nested `$defs` it references), so non-TypeScript
  clients get the same shapes from the published schema. Additions only: `data`
  stays an open object that allows additional properties, no existing property,
  required key, or type changed, and `client.run()` still returns
  `Envelope<unknown>` for verbs without a profile.

- e4ed6a1: Add typed configuration in `.verdict/config.json`, with path scopes, captured explanations, review profiles, grouped spend and diff limits, and recorded deep validation. Local run artifacts now live under `.verdict/state/`; retired `.review/` knob files are no longer read.

  Cost records use schema version 2 with grouped lens and cost evidence. The Go Commander interface now takes a command pointer and preserves its working directory.

### Patch Changes

- 0c54ab2: Corpus cases accept an optional `also_lines` list of alternate anchors for
  the same defect. Each entry is matched with the same line tolerance and
  defect-naming rule as the primary line, matching any anchor counts the bug
  once, and the reported line stays the primary one. The generated
  `corpus.schema.json` now requires positive line numbers in `also_lines`
  items, so schema validation rejects `0` and negative entries just as
  `calibrate` already refused them as `corpus-invalid`.
- 72db325: `calibrate` and `trend` grade a corpus finding on the defect it names, not the
  line it picked. A finding used to count as a true positive whenever its path
  matched and its line sat within `line_tolerance` of a known bug, so on a
  four-to-seven-line hunk every anchor was correct and the reported precision
  measured line-guessing rather than review quality; the same line could hold a
  comment about variable naming and score as a catch. A true positive now also
  has to name the bug: one of its `keywords` (new, optional in
  `corpus.schema.json`), its `kind`, or a phrase from the published category
  vocabulary. The default window drops from three lines to one, and a finding `record` moved out of its hunk is
  never a true positive, so the anchor is measured and not assumed.

  The human false-positive line says which rule failed, so a run distinguishes a
  finding that landed off the bug from one that landed on it and described
  something else. Corpus bundles the benchmark runner writes now carry the
  reviewed `full.diff` and its `hunks.tsv`, so `record` scores against the hunks
  the agent read instead of rebuilding an index from a diff that may have moved.

- 58dbc7d: The docs site publishes its documentation guide as the `verdict-docs` skill
  instead of a second skill named `verdict`. The `verdict` name belongs to the
  review workflow — `skills/verdict/SKILL.md`, the payload `verdict skill install`,
  `npx skills add stainless-code/verdict`, and the embedded binary ship — and a
  harness that indexed both well-known skills loaded whichever won, handing an
  agent that asked how Verdict works the review workflow, or the reverse. The
  site's `/.well-known/agent-skills/index.json` now lists one skill,
  `verdict-docs`, and the docs build fails if two published skills ever share a
  `name:`.
- 3089864: Global flags work anywhere on the command line — before the verb, between the
  verb and the repo, or after the pr — so `verdict --no-json report acme/app 9`
  and `verdict report acme/app 9 --no-json` print the same output. A bare `--` now
  ends positional parsing, so everything after it is a positional and
  `verdict fetch-local acme/app -- --json` is a usage error instead of a stray flag.
- 0e8ed37: `verdict mcp config --agent codex --write` reads its `config.toml` the way TOML
  does, so a `verdict` table, key, or comment inside a string is text and no
  longer a merge target: a file that ends inside an unterminated string, or whose
  `[mcp_servers.verdict]` line opens one, refuses typed and keeps its bytes
  instead of having keys appended into the literal. A table spelled inline or
  dotted refuses rather than guess, the file's own CRLF or LF line endings are
  kept, and an append no longer drops a trailing line of non-ASCII whitespace.
- 72d0a60: `verdict post` on a PR that was never fetched now refuses with the typed
  `missing-bundle` error naming the fetch command, instead of failing internally
  on its first write.
- 1b16049: Every release now publishes `commit.txt` — the commit SHA the tag points at and
  the certificate's workflow-SHA claim — and `install.sh`, `install.ps1`, and
  `verdict upgrade` pin `cosign verify-blob` to it with
  `--certificate-github-workflow-sha`; the `setup-verdict` action inherits the pin
  through `install.sh`. The identity and issuer alone accept any run of the
  Release workflow, so a bundle from a different build of the same workflow used
  to satisfy the check; the pin makes that a refusal. A release that publishes no
  `commit.txt`, or a `commit.txt` that is not a full 40-character SHA, is refused
  with `verify-failed` instead of being installed unpinned.
- 3089864: A signed install no longer aborts when `cosign` is on the `PATH`: `install.sh`,
  `install.ps1`, `verdict upgrade`, and `setup-verdict` now verify the certificate
  identity the Release workflow actually mints, which is a push to `main` rather
  than the tag the run publishes under. The old check asked for a `refs/tags/…`
  identity that no release certificate ever carried.
- 3089864: The shipped review prompts no longer hand an agent a corrupt skeleton to copy.
  The "Technical details" block and the mermaid wrapper had a fence opened mid-line
  with its body flattened onto it, plus orphan closers and an unclosed block, so an
  agent following them emitted run-together headings and an unterminated fence.
  Both are well-formed blocks now.
- c27ea2f: `npx skills add stainless-code/verdict` now installs just the skill instead of
  copying the whole repository — over a thousand files — because `SKILL.md` and its
  payload moved into `skills/verdict/`. That directory holds exactly the files
  `verdict skill install` writes.
- 3089864: The skill now triggers on every review target it can handle — a pull request, a
  branch, a commit range, or staged changes — so an agent picks it for local
  reviews too. `NOTICE.md` carries the full MIT text of the upstream review
  prompts Verdict adapts, pinned to the upstream commit, and the skill copy under
  `skills/verdict/` ships that same license beside the prompts it covers.
- 3089864: The TypeScript client's generated types render `anyOf` as a union instead of
  collapsing it to `unknown`, and a null variant folds into a trailing `| null`
  the same way a `"null"` entry in `type` does. `show`'s `data.fix.verify_hint`
  now types as `VerifyHint | null` rather than `unknown`.
- 3089864: `verdict upgrade` recognizes an install made with `VERDICT_INSTALL` even after
  the variable is gone, by reading the installer receipt beside the running
  binary. A receipt is a hint, never proof of ownership: when npm, Homebrew, a
  project's `node_modules`, or a skill copy already owns the directory, `upgrade`
  and `uninstall` keep that channel and delegate or refuse with the owning command
  instead of replacing or deleting a file the manager tracks.
- d9366dc: Shell completion now offers `threads`, with its `--provider` and `--json`
  flags.
- 22f8e00: `verify` now refuses a path glued to the option that takes it: `tar -C/etc`,
  `cp -t/etc/cron.d`, `sort -o/etc/x`, `grep -f/etc/passwd`, `gcc -I/usr/include`,
  `git -C/etc`, and `go build -o/etc/x` exit before the command runs instead of
  executing it. The pre-exec gate only recognized a path after a space or a
  separator, so every spelling getopt accepts — a clustered `-czf/etc/x`, a
  positional `-C` after `--`, a quoted or backslashed `-C/etc`, an uppercase
  `-C/ETC` — reached the shell with the escape unreported. An attached value is
  now judged as the path it is, and the refusal names the fragment it read.

  One spelling to know: a relative value that starts with a letter and contains a
  slash (`git -Ctools/cli`) has the same shape as `-C/etc`, so the gate refuses
  it rather than guessing. Write `git -C tools/cli` — both spellings of a relative
  value stay allowed once a space separates the option from its value.

- 32d60ce: A canceled re-verify keeps the last completed proof, and a failed Windows handoff leaves no staged helper behind.

## 0.4.0

### Minor Changes

- 3c64fc7: Measured review rates instead of impressions. A marked `corpus-*` bundle kind
  carries a checked-in case manifest with ground-truth bugs and provenance;
  `calibrate` grades a model's findings against that ground truth offline and
  reports precision, recall, and false-positive rate, and `trend` aggregates the
  same numbers per model and panel. Ordinary `local-*` reviews keep refusing the
  live join. Ships a seeded starter corpus under `bench/corpus/` and
  `docs/benchmark.md`, an operator runbook for real PRs, real agents, cost
  estimates, and publishing measured rates.
- b21eaff: Experimental MCP server. `verdict mcp` serves the review verbs to an MCP host
  over stdio (newline-delimited JSON-RPC 2.0): no daemon, no HTTP, no new
  dependency, sharing the host's credentials and exiting when stdin closes.
  Tool schemas reflect the CLI request types, results are the `--json`
  envelope's `data`, and typed refusals return `isError` with code, message, and
  hint intact; `--read-only` exposes only the non-writing verbs, while `post` is
  available in normal mode and in `--operator` mode. `verdict mcp config --agent
claude|cursor|codex` prints the exact host entry, and `--write` merges it
  idempotently into the documented config (`.mcp.json`, `.cursor/mcp.json`, or
  `$CODEX_HOME/config.toml`), backing the old file up to `<path>.verdict.bak`
  and refusing unparseable or foreign entries instead of rewriting them. `--json`
  prints one envelope, never prompts, and never starts the server; `verdict
--help` now lists the MCP entry so the surface is discoverable.
- b21eaff: `verdict` is one static binary — no checkout, no `jq`, no shell script. Install
  it with the verified `install.sh` (macOS/Linux) or `install.ps1` (Windows) into
  `~/.verdict/bin`, the Homebrew tap, or the npm wrapper; both installers verify
  the cosign signature and the archive SHA-256, smoke-test the installed binary
  from its final path, and add `PATH` with one marked line
  (`VERDICT_NO_MODIFY_PATH=1` opts out). In CI the new `setup-verdict` action
  installs a pinned, verified copy on Ubuntu, macOS, and Windows runners without
  touching shell rc files. `verdict upgrade` updates whichever channel installed
  it — a verified self-replace for installer installs, the owning package
  manager's global update for npm and Homebrew — and `verdict uninstall` removes
  only installer-owned files; both refuse, naming the exact command, when the
  install belongs to something else. `verdict skill` prints or installs the
  payload embedded in the binary (`SKILL.md`, `prompts/`, `schemas/`, and the
  `scripts/verdict` shim), so the served skill can never skew from the binary.

  **Upgrading from 0.3.x:** the old script install is replaced, not patched.
  `scripts/review.sh` is gone — every verb is `verdict <verb>` with the same
  flags and `--json` envelopes. The old symlink-only `install.sh` became
  `install-skill.sh` (checkout-only), and the copy-based `install` verb is gone,
  so reinstall the skill with `verdict skill install` or `npx skills add`. Docs
  and scripts that call the old script path must be updated. Start at the
  rewritten quickstart, the new glossary of review terms, the CI recipe in
  `docs/ci-recipe.md`, and `verdict --help`, which now names each verb's
  provider scope.

- b21eaff: Posting is harder to get wrong and harder to do twice. `post` refuses a second
  review on the same reviewed SHA before any network read, naming the exact
  `fetch --base-sha` advance; it also refuses duplicates found in the bundle's
  own fetched review list, so a fresh checkout, a CI re-run, or another machine
  cannot repost what the PR already has — `--force` still overrides, and the
  refusal names the re-fetch needed when the earlier review was deleted. Points
  outside the diff hunks post under "Unanchored observations" instead of as
  inline anchors GitHub would reject. A body past the 65,536-character hosting
  limit splits into follow-up COMMENT reviews — first review keeps the footer,
  later parts carry a continuation header, and a failed follow-up refuses typed
  naming the review that already landed — instead of being trimmed. A 403/429
  carrying a rate-limit signal retries within bounds, honoring `retry-after` or
  the reset marker and never publishing twice; when the last permitted attempt
  is still retryable, the refusal carries `retry_at` in RFC 3339 so an agent can
  wait for that instant.

  A stale-fetch downgrade note is now followed by a blank line, like the drift note, instead of a stray literal `\n` in the posted review body.

- 9fb0cad: The PR conversation is a local, schema'd record instead of something each verb
  re-reads from the host. Every fetch writes `.review/<pr>/threads.json` (schema
  published) holding each thread's host id, diff anchor, resolved and outdated
  state read from the host's own fields, root author kind, and the comments with
  authors, timestamps, and capped bodies; GitHub comes from GraphQL
  `reviewThreads` (REST carries no resolution), and GitLab, GitCafe, Forgejo,
  Bitbucket, and Azure fill the same shape, paging rather than truncating.
  `verdict threads <owner/repo> <pr> [--provider …] [--json]` re-reads just that
  surface and rewrites only that file, so re-review, address-reviews, readiness,
  and calibration can join prior feedback on any of the six hosts. Posted inline
  comments now carry a hidden final-line `<!-- verdict-finding sig=… -->` marker,
  so a new comment links back to the finding that produced it while older
  comments keep reading as unlinked.
- b21eaff: Published machine contracts beside the CLI. `schemas/` ships JSON Schemas for
  the review input, findings store, cost and usage records, PR threads, and the
  `--json` envelope with per-verb `data` profiles, included in the npm tarball;
  `schemas/validate.sh` checks documents against them offline (no `jq`; exit 0
  valid, 1 rejected, 2 bad usage), and the drift rule is additive within schema
  version 1. An experimental TypeScript client
  (`@stainless-code/verdict/client`) wraps each verb with envelope types
  generated from those schemas and refusal codes generated from the CLI, with no
  runtime dependencies. The experimental Go package `pkg/verdict` embeds the
  same operations in-process through a context-first client with typed requests
  and responses and a `*RefusalError` for `errors.As`; its API may still change
  before 1.0. A new "Build a client" guide documents the `--json` protocol —
  envelope keys, exit and refusal codes, versioning, binary resolution,
  timeouts, output caps, and when MCP beats spawning a process — with a
  dependency-free Python client generated from the schemas and an offline smoke
  test.
- b21eaff: The readiness gate can say what it knows. `report --check` and
  `report --completeness` gain a live-thread dimension: open threads the local
  store does not know about fail the gate, and unreachable lookups warn and stay
  neutral. New `--live auto|strict|skip` picks the posture — `auto` keeps that
  fail-open default, `strict` fails closed when the host cannot be reached, and
  `skip` pays zero network and gates on the local dimensions only, saying so in
  the output and the `--json` warnings. `post` and the gate now name the same
  advance for head drift, and a binding APPROVE on a known-stale fetch
  downgrades to COMMENT instead of approving past the moved head.
- 21ac180: Chunked split-range reviews. Very large PRs are reviewable again: with
  `--chunk` (or `.review/chunk`) an over-cap diff is sliced on file boundaries
  into numbered chunks with a versioned manifest, the agent reviews one chunk at
  a time, and the results synthesize into one verdict with a summed Basis line.
  `post` and `record` refuse coverage that claims to be complete but is attested
  partial, and warn loudly when a chunked review is unattested. `fetch-local`
  supports the same flow.
- 21ac180: `verify` refuses before it runs. Destructive, networked, credential-reading,
  and repo-escaping commands are rejected with a typed error before execution
  (over 40 regression shapes), so a hostile or careless `verifyCmd` cannot
  delete, exfiltrate, or escape the repository. The recorded tail also drops
  credentials no environment value names — vendor-prefixed tokens,
  `Authorization` and `Proxy-Authorization` headers, bare `Bearer` values, and
  URL userinfo — and strips ANSI escapes and other control characters, so a
  command cannot rewrite the proof it lands in; `.verify.out` keeps the raw
  bytes. `doctor` warns on excess token scope, and the manual documents an
  opt-in hostile-PR sandbox recipe plus the residual risk a string scan cannot
  remove.

### Patch Changes

- b21eaff: Cost accounting reads what the operator actually knows. `cost.json` entries
  gain optional `cache_read_input_tokens` and `cache_creation_input_tokens`
  (subsets of `input_tokens`; absent or `null` reads unknown), and
  `report --cost` rolls them into a top-level `cached_input_tokens` scalar with
  totals unchanged. The spend gate can now price comparable runs from usage
  records the operator hands over with `VERDICT_USAGE=<file|dir>`, in a published
  schema (schema, model, tokens, optional USD, source, window), with the
  self-reported `cost.json` as the fallback; a handed record joins by its window
  bracketing the run, and a named path that is missing or malformed refuses typed
  before the bundle is written. Automatic discovery of other tools' transcripts
  is gone — the optional converters live outside the binary in
  `examples/usage-adapters/`. With nothing handed over, output is byte-identical.
- 0ab8a01: Isolated local reviews. `fetch-local --worktree` checks the review target into
  a temporary detached worktree and builds the bundle from that checkout instead
  of the live tree, so caller tracing and verification run against real, stable
  code without disturbing the working tree. The checkout is always removed
  before the verb exits (success, refusal, or cancel); when no checkout can be
  created the verb refuses typed `worktree-failed` and writes nothing. Without
  the flag every byte behaves as before.
- b21eaff: Small additive reads. `post.json` and `record.json` now record `tool_version`,
  `prompts_hash`, and `model`, and `calibrate` and `trend` surface them so a
  mixed-version history reads as mixed. `show --json` gains a `data.fix` block
  beside the unchanged `data.item`: full sig, status, severity, deduped files,
  locator, suggestion, a `verify_hint` read from the bundle, and a fixed
  untrusted-evidence caution — a pure read with no flags, no execution, and no
  store writes, and missing history reads as null rather than invented text.
  `doctor` human output opens with a run stamp and closes with one line,
  `Summary: healthy|unhealthy — N ok, M warn, K fail`, while `--json` envelopes
  and exits stay byte-identical. Builds with no release metadata now say
  `0.0.0-unknown` everywhere — `--version`, the skill payload and receipt, and
  the posted or reported footer — instead of the footer saying `unknown`.
- b21eaff: Failures a 0.3.1 user could hit are gone. `fetch` on GitLab looks up the
  project id with the plain `owner%2Frepo` path — the old request carried a
  trailing encoded newline, which could fail the lookup at the first step.
  Provider lists are read completely: Forgejo files and reviews, GitLab MR notes,
  and Azure change entries and threads page instead of stopping at the first
  page, and a page cap, a failed page, or a failed thread read lands an envelope
  warning instead of silently under-covering. `fetch` retries GitHub's transient
  "internal error, please try again" on the same three-attempt budget `post`
  already had, while auth and validation failures still fail fast. Machine mode
  never prompts, even on a TTY: `init --json` and `install --json` without
  `--yes`/`--force` refuse typed `needs-confirm`, write nothing, and print the
  hint instead of waiting on a `Proceed?` prompt. Malformed agent input fails
  closed with `bad-review-input` — unparsable `review.json`, non-object
  `comments`/`triage`/`sections` entries, non-string scalars, misshapen
  verification claims — and a corrupt findings store now refuses typed
  `corrupt-store` across `report`, `show`, `calibrate`, and `trend` instead of
  crashing mid-render with no envelope, never starting fresh over history.

  A value flag given without its value now fails as a usage error naming what
  it wanted (`--provider needs a value (want <name>)`, exit 2) before any
  network call, instead of falling back to a default target. A bare `--stash`
  still means `stash@{0}`.

  Cancellation is clean. SIGTERM now cancels a running verb the way Ctrl-C
  does: exit 3 with a `canceled` envelope, and child process groups are killed
  instead of orphaned. A cancelled verb returns promptly even when a grandchild
  process still holds its output pipe. Interrupting `verify` refuses
  `canceled` and records nothing, where it used to record a false
  `timeout after 300s` proof and exit 0. The `mcp` stdio server takes the same
  signal: SIGTERM cancels an in-flight tool call and kills its process tree
  instead of orphaning a running `verify` for the rest of the verify budget.
  On Windows, `verdict upgrade` stages its
  helper beside the installed binary, so an install directory on another drive
  no longer makes the final move fail after the upgrade reported success.
  On Windows, `verify` starts its command suspended and puts it in its
  kill-on-close job object before it runs, so a grandchild spawned in the first
  instant can no longer escape a timeout or cancel kill.
  `threads.json` always labels a thread's author kind, including a thread with
  no readable comments, as the published schema requires. A cancel that lands
  inside a read verb's service (`ready`, `trend`, `threads`, `calibrate`,
  `check-tree`, `doctor`, `report`, `next`) exits 3 with the `canceled` envelope
  like every other verb, where it used to exit 1 as an ordinary refusal.

  Provider page walks finish what they start. A GitLab merge request whose diff
  list spans host pages (`glab api --paginate` concatenates one JSON array per
  page) now yields every changed file instead of a `PR.json` with no files at
  all, and a merged Bitbucket pull request (`state: MERGED`) folds to `merged`
  rather than `unknown`. A GitCafe review/comment cursor walk that stops early — a
  failed page, an unreadable page, or the 20-page cap — keeps the pages it already
  read, warns, and marks `threads.json` `truncated: true` instead of quietly
  under-covering the conversation; Forgejo's review list does the same at the
  provider cap. Hosts with no failures read exactly as before.
  A failed or unreadable Bitbucket diffstat/comments page, a GitLab changed-file
  payload that is not a diff list, and a failed Azure iteration read no longer
  pass as an empty list: each warns, and the Bitbucket threads feed is marked
  `truncated: true`.

  Report artifacts survive a kill. `report --output`, `report --sarif`, and the
  MCP report artifact are staged beside the target and renamed into place, so a
  crash mid-write can no longer leave a truncated report behind. A symlinked
  `--output` path keeps the link and replaces only the file it names, an existing
  file keeps its own mode (a new one is still owner-only), and a device like
  `/dev/stdout` still receives the bytes directly. A failed write refuses
  `report-failed` and leaves the previous report intact.

  Numbers in machine output render the way jq 1.8 renders them. A literal such as
  `9.223372036854775808e18` now prints as `9223372036854775808` on every
  architecture, where arm64 used to saturate at `9223372036854775807` and amd64
  wrap to `-9223372036854775808`.

  `verdict mcp install` no longer corrupts a Codex `config.toml` that uses a TOML
  multi-line string: a `[mcp_servers.verdict]` header spelled inside a `'''` or
  `"""` literal used to be merged into as if it were real, and the merge now
  refuses `mcp-config-unmergeable` (file untouched, add the table by hand)
  instead of guessing.

- b21eaff: Reviews of other repositories stop carrying this repository's own conventions.
  The shared system prompt now defers branch, commit-message, formatting, and
  style rules to the target repo's `AGENTS.md`/`CLAUDE.md`/`CONTRIBUTING`, with
  verdict's own rules staying in its `AGENTS.md`, so reviews of this repo keep
  them. The fix flow opens with the untrusted-data preamble, the abort-if-not-
  introduced pin is stated where the flow can follow it, and the manual records
  the machine-modes-never-prompt doctrine and a combined
  `report --check --completeness` pre-push probe. `--show-prompts` wraps every
  untrusted bundle section in a per-run nonce fence named in a trusted prologue,
  so a forged close smuggled in a diff stays evidence instead of ending the
  section; thread files stay off the initial-review reading list by design.
  Missing trusted prompt files no longer vanish silently: a missing
  `prompts/system.md` refuses typed, while a missing `SKILL.md`,
  `prompts/review.md`, or `prompts/diagrams.md` warns and proceeds; missing
  bundle files still skip silently.
- b21eaff: Credential and untrusted-input hardening. Every credential-bearing provider
  read is bound to the configured origin: `fetch` refuses a Bitbucket pagination
  `next`, a Forgejo metadata `diff_url`, or an Azure metadata `remoteUrl` whose
  scheme and host differ from the API base, instead of sending the token to that
  host, and Bitbucket paging stops at a page cap or a repeated cursor with a
  warning that the bundle may be incomplete. Command-stderr sidecars written by
  `fetch` and `post` (`.diff.err`, `.post.err`, `.recheck.err`, provider `.err`)
  are now owner-only `0600`, since they can carry token-adjacent forge
  diagnostics. Untrusted input is capped: `record` and `post` refuse an oversized
  agent file while reading it instead of after allocating the whole document,
  `verify` and the fetch runners cap captured child output (a capped proof
  records a truncation marker instead of growing the process), and every store
  reader rejects oversized documents while keeping its own refusal code. A review
  anchor, knowledge-base counter, override threshold, adopt anchor, verify
  timeout, or read number that does not fit in an integer now takes its typed
  refusal instead of wrapping into a nonsense negative span or count.

  The `verify` gate now refuses a command it cannot fully scan. A destructive
  tool name hidden inside `$(…)` substitutions nested deeper than the gate's
  scan bound, or inside a substitution body glued directly to a word (for
  example `$( (echo r))m -rf dir`), was previously treated as clean. Both now
  refuse before anything runs. A pull request file name containing a tab or
  newline can no longer forge or shift rows in `hunks.tsv`, and the ordered
  JSON reader rejects trailing bytes after the document (`{"a":1}}`) instead of
  silently ignoring them.

## 0.3.1

### Patch Changes

- 045744b: Basis-line validation compares the agent's line against the review's own comments instead of the merged findings store, so re-reviews with carried-over findings no longer warn. Genuine miscounts still warn and keep the body.
- 0c323cb: Same-head reposts omit the review link in the footer instead of pointing at the superseded review. Fresh posts keep the link; plain-text fallback unchanged.

## 0.3.0

### Minor Changes

- e8fa26c: Binding APPROVE now rechecks live open threads before posting: unresolved live threads the local store doesn't know about refuse APPROVE with a typed error (triage or `--force`); an unreachable lookup degrades to COMMENT with the reason stated instead of approving blind. COMMENT, dry-run, and local flows make no new network calls.
- 928fe5b: Attribution v2: the footer default is now a linked, leveled, logo-bearing line — a product-linked 18px logo (defaulting to the tracked `apps/docs/public/logo.svg` raw asset) plus `Reviewed with [**Verdict {version}**](https://stainless-code.com/verdict)` with the version inline space-separated, followed by the receipt fields. `publish.attribution` migrates from the day-old P1 bool to an `off`/`product`/`full` enum (file bools refuse with the mapping named: `"off"` for false, `"product"` for true; `full` appends `by [Stainless Code](https://stainless-code.com)`). New `publish.logo` (default true) and `publish.logo_url` knobs (empty reads text-only, never a broken image); a `<!-- verdict-footer -->` machine divider separates content from footer with strip-and-replace re-rendering; `attribution_text` gains the `{version}`/`{tier}`/`{product_url}` inventory with unknown placeholders refused.
- 401176b: Opt-in behavioral diagram: agent-drawn mermaid under a ported contract (`prompts/diagrams.md` with the 7-type selection map, honesty rule, and copy-exact skeletons), gated by a new `publish.diagrams` namespace (`behavioral` default off, `structural` default on for the P3 topology half, `expanded` default collapsed), grammar-level warn-and-drop validation that never fails a review, and `--diagram-behavioral/--diagram-structural/--diagram-expanded` flag pairs.
- 7e46a2e: Bounded verify-gated fix loops: new `fix` verb runs one pass per invocation over local bundles (top open inline sig or `--sig`), printing finding + full `kb.txt` context with patch → verification.json → record → re-run instructions. Hard-coded ceilings (max 3 passes per sig in `.review/<bundle>/fix.json`, scope-escape on out-of-anchor worktree touches), start gates (conflicted tree, closed bundle), terminal completion summary, local-only in v1.
- aa2ca37: Self-grading probe: `calibrate --probe` grades recorded findings against triage dispositions as the script-owned baseline, and merges agent judgments (`--judge`/`--judge-file`, validated, atomic, sticky) that may diverge — the agreement/dissent rate over doubly-judged rows is the instrument reading. Additive grade file only; findings, joins, and calibration rows untouched.
- 76135b5: Anchors validate against the fetch-time snapshot: `fetch` pins the commentable set and `post`/`record`/`adopt` reuse it (rebuilding only if missing, with a warning); dropped anchors ship as named body notes with reasons instead of failing or misplacing silently. Mid-run pushes read as drift context, not a moving validation target.
- 561e316: Structured local knowledge base: `learn` verb (append/list/retire/restore scoped rules with provenance, rigor, hit counters), `kb.txt` recall onto relevant diffs as context-never-orders, flip-time credit with false-hit-rate auto-retirement, and per-credit audit rows.
- 6cd6af8: Knowledge base: re-posting a finding without `kb` keeps the prior cite while explicit `"kb": []` clears it (uncite); every `.review/kb-joins.jsonl` audit row now also carries `bundle` and `review_id`.
- 7ea467f: Overlap audit: `trend --overlap` joins recall manifests × findings cites × the joins ledger into recall precision, recall coverage of cites, and the cited-outcome split, with joins corroboration and calibrate eligibility. Pure read, no network; missing manifests read as unmeasured, never zero.
- 05efe51: Change overview: `report --output` renders a script-computed collapsed file table plus a `flowchart TD`
  change-topology diagram on large diffs (more than 8 files or 24KB, `--overview`/`--no-overview` override),
  with stable F-row ids, deterministic relations only, and a new `publish.overview` knob.
- 106b40c: Permalink-first evidence: findings reference GitHub blob links pinned to the reviewed SHA (reference-style,
  `path:line` locators with definitions), with quotes over three lines kept inside collapsed detail blocks
  only. Non-GitHub bundles degrade to plain `path:line` text. Link validation warns and keeps, never failing
  a review over garnish.
- 9bcedfb: Debug legibility: `fetch`/`fetch-local --show-prompts` dumps the exact assembled prompt (system + mode + recall + coverage manifest) without running an agent; `report --output` bodies lead with confidence plus safety and list up to seven checked files on clean reviews, with full reasoning staying in the machine envelope.
- 55a5a27: Publish footer: posted bodies and `report --output` end with a merged receipt block (tool version, tier, cost or unrecorded, files reviewed, review link when known) including a factual one-sentence attribution, plus a deterministic confidence-basis line (rigor counts, cited rules, verified fixes). New `publish` settings namespace (footer/attribution/attribution_text/copyright) with flags beating file beating env; agent Basis lines validate warn-and-keep, never failing a review.
- 84e7c28: Recall manifest: the recall step emits `kb-recall.json` (recalled rule ids in rank order with scores) beside `kb.txt`, rendered from the same single sort. The human note is byte-identical, silence on clean is preserved, and all readers tolerate a missing manifest.
- 056099a: Report gate answers "is HEAD reviewed?": new opt-in `--completeness` flag on `report` (alone or with `--check <tier>`) compares the fetched HEAD against the completed-post record, failing closed with a typed error naming the re-fetch command. Without the flag, output and exits are unchanged.
- 1ec07ca: Onboarding pair: `review.sh init` scaffolds the standard local control files (budget, panel, KB policy, KB starter) with chill/standard/strict presets and runs check-tree validation, offline with preview-then-confirm; an XDG-homed presentation-only settings file (`output`/`layout`/`color`, schema-hinted, credentials refused) ends env sprawl with flags winning over file over env and byte-identical behavior when absent.
- 6674504: Operator-owned template overrides: per-block fragments under `.review/overrides/` (preamble, footer
  order, walkthrough threshold, attribution) guarded by an enumerated locked-invariant list (marker line,
  findings structure, guard satisfiability, metadata presence, divider shape), validated at check-tree time
  and revalidated at post time with typed refusals. `check-tree --json` emits the standard envelope on all
  paths.
- 53a4bbe: Exactly one terminal write, enforced: `post` refuses a second completed post on the same reviewed SHA with a typed error (`--force` overrides, new commits re-arm automatically). Dry runs, attempts, and failures never arm the guard. The existing exact-payload replay guard stays as the fine layer underneath.
- e304108: Copy-based skill installer: new `review.sh install` verb detects the de-facto agent skill dirs (.agents/skills, .claude/skills, .codex/skills at the repo root and $HOME), previews the copy plan for the tool-owned verdict/ payload (SKILL.md + prompts/), and applies only on explicit confirm (same preview-then-confirm idiom as init, typed needs-confirm). Only installer-owned verdict/ dirs (with a .verdict-managed receipt) are ever written — foreign skills and unowned dirs stay byte-identical; re-runs are idempotent and receipt/tool version skew warns instead of failing. Offline, shellcheck-clean, operator docs updated.

### Patch Changes

- c8b81f3: Bird's-eye review follow-ups: indented fenced blocks (up to 3 columns) now validate, single-line
  fences never open an end-of-body swallow, drop-notes insert before a carried footer divider so
  strip-replace preserves them, the doc wrapper is a parseable multi-line block, and the manual
  documents the full v1 placeholder inventory.
- 54cfcff: Pre-commit runs `shellcheck -S warning` over staged shell files (missing binary skips with
  a notice, same degrade-gracefully idiom as the formatter check), and the CI lint job covers
  every tracked `*.sh` instead of three hardcoded paths. Pinned by a hermetic hook eval
  (violate fails, clean passes, missing binary skips).

## 0.2.0

### Minor Changes

- 332d20b: Overridable fetch diff cap: REVIEW_MAX_BYTES env (or .review/max_bytes file) overrides the 512000-byte default for deliberate exceptions; refusal names the override last and the envelope carries cap provenance when set.
- 5341cde: Cost control plane: operator spend budget (env/file), pre-run estimate from comparable past runs, and consent flag for projected-over-budget runs with receipts in the cost rollup. Unset preserves today's behavior exactly.
- dd0b17b: Record effort tiers (cheap/standard/deep) on cost runs with per-tier rollup, default delta re-reviews to cheap lenses, and emit a sensitive-path manifest plus no-quote rule on fetch (advisory warning, evidence untouched).
- f96d7f5: Add a `ready` readiness-advisory verb (CI state, open threads, draft/conflict, description completeness — advice, never a gate) and per-review cost accounting (agent-reported run records plus `report --cost` rollup).

### Patch Changes

- dd0b17b: Robustness bundle: optional review-level `confidence` (1–5) and `security_summary` recorded on `record.json`/`post.json` (absent stays absent, out-of-range warns and drops); exact re-entry commands on post/record failures plus an echo-only interrupt trap on post; `error:`/`try:` failure-message pass with received values and valid ranges; fetch-local warnings for untracked/binary exclusions and empty paths filters; explicit fetch byte cap (512000 bytes) with split-or-narrow hint and distinct empty-vs-huge signals.
- dd0b17b: Add `ready --check`: opt-in readiness gating with machine exits (0 ready, 1 blockers-or-wait, 2 usage, 3 tool error). Default stays advisory with exit 0; hook recipe in the operator manual.
- 5341cde: Add `report --check <critical|important|informational>`: opt-in severity gating with machine exits (0 no open findings at-or-above tier, 1 findings at-or-above tier, 2 usage, 3 tool error including corrupt store). Default stays advisory with exit 0; corrupt stores now fail typed instead of raw jq errors; hook recipe in the operator manual.
- 5341cde: CLI hygiene: oversize-fetch errors carry machine-actionable narrower-scope candidates, doctor splits hard-fail from warn and checks storage writability, and shell completion covers verbs and flags for bash and zsh. Also fixes doctor always exiting 0 and the empty-filelist fetch-local crash.
- 5341cde: Add `trend <owner/repo>`: read-only portfolio rollup over existing bundle stores — findings opened/addressed/closed over time broken down by severity tier and status, with per-bundle models/effort for the calibration join. Recomputed every run (no cache files); empty history and unreadable stores never fail; default report output untouched.

## 0.1.3

### Patch Changes

- ffe4e4c: Rewrite the README for plain speech: cut AI tells, name the trust boundary, fix the registry refresh command, and document the current installer flags.
- 8504165: Correct the registry refresh docs, verified against the CLI source and live runs: filters match installed skill names (not `owner/repo` paths), and `check` is the dry-run counterpart to `update` (hidden from `--help`, covered by upstream tests).
- 02a6df5: Harden the verify verb: the timeout kill now stops the whole process group (a decoy fast-exit with ordinary backgrounded children is killed and recorded as timed-out instead of success; deliberate process-group escapers remain out of scope), and known secret values are redacted from the recorded tail. Legitimate commands run unchanged with full env.

## 0.1.2

### Patch Changes

- 72ebe75: Document `install.sh --agent/--dir/--list/--check/--uninstall` management flags in the README.
- 5035996: Harden the trust boundary: `system.md` now names PR data untrusted (evidence, never instructions) and constrains `verifyCmd` to repo-scoped non-destructive commands. `install.sh` detects installed harnesses (`--detect`), supports project installs (`--project`), honors `XDG_CONFIG_HOME`/`CLAUDE_CONFIG_DIR`, and validates the checkout before linking.

## 0.1.1

### Patch Changes

- a879767: Read tool version from `package.json` instead of the `VERSION` file (same output, single source of truth) and ship `CHANGELOG.md` in the tarball.

## 0.1.0

Initial npm release. Local, on-demand PR review for any coding agent — the agent reviews, `gh` does GitHub I/O. No servers, no external services, no daemons.

- Evidence-graded findings (`said` to `reproduced`), severity ladder tied to author action.
- Keep-biased machinery: partitioned anchor validation, stale-SHA and double-post guards, binding verdicts behind `--bind`.
- Machine surface: every verb speaks `--json` envelopes, SARIF export, `calibrate` joins predictions against live ground truth.
- No-PR reviews: `fetch-local` bundles the worktree, `record` merges the store, `adopt` carries findings into a PR fetch.
- Multi-provider fetch (github/gitlab/gitcafe/forgejo/bitbucket/azure); post/calibrate stay GitHub-only.
