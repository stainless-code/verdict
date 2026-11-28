# Security Policy

Verdict reviews hostile pull requests and executes agent-supplied
verification commands. That makes its trust boundary load-bearing:
please report vulnerabilities privately so a fix can land before
details go public.

## Reporting a vulnerability

Use **GitHub private vulnerability reporting**: the repository's
Security tab → Advisories → _Report a vulnerability_. Do not open a
public issue for anything you believe is a security bug.

Include the verdict version (`verdict --version`), the install route
(installer script, npm, Homebrew, skill copy, or source build), the
verb and flags involved, and steps to reproduce. Redact tokens and
private repository data from the report.

We aim to acknowledge reports within 3 business days, agree a fix and
disclosure timeline with you, and credit you in the release notes
unless you prefer to stay anonymous.

## Supported versions

Pre-1.0, only the latest release is supported with security fixes.
Upgrade with `verdict upgrade` (or re-run the installer) and re-check
before reporting: many findings are already fixed on the latest tag.

## Scope

In scope (highest priority first):

- `verify --sandbox`, the enforced boundary: mount and namespace isolation
  under `bubblewrap` on Linux; under `sandbox-exec` on macOS, the network and
  Mach service denial (default-deny with one lookup-only allow, so no
  keychain, preference, pasteboard, LaunchServices, AppleEvents, or launchd
  channel acts for the command), the denial of the `lsopen`,
  `appleevent-send`, `job-creation`, `distributed-notification-post`, and
  cross-process `signal` operations, the denial of every write outside the
  reviewed root (the snapshot `verify` runs in) and the per-run private
  `TMPDIR`, and the read denial of the
  credential table and the shared host temp roots; the credential-path hiding
  on both; and the `sandbox-unavailable` refusal that never degrades to an
  unwrapped run or to the network-only mode. **`sandboxed` means exactly that
  full shape** — `"sandboxed": true` in the patch record, the deep-group
  receipt, or `--json` data asserts `bwrap` or `sandbox-exec` enforced it. An
  escape from that sandbox, or a run that records `"sandboxed": true` without
  the enforcement (or for the network-only mode), is a vulnerability. One
  macOS residual is documented, not a finding: Seatbelt cannot gate the
  `kern.procargs2` sysctl, so a sandboxed command can read the environment
  of same-uid processes that are not restricted platform binaries — a
  third-party editor, an agent CLI, `node` (see the
  [manual](docs/manual.md#hostile-pr-sandbox-recipe)); `verify --sandbox`
  warns, naming the variables and never their values, when verdict's own
  environment carries a secret-named variable a command could read that
  way. A route to another
  process's environment that does not go through `kern.procargs2`, or a
  profile that could deny it and does not, is in scope.
- `verify --sandbox-network-only`, the explicit Linux opt-in
  (`unshare --user --map-root-user --net`): the promise is network isolation
  alone, a `sandboxMechanism: "unshare-net"` record that never claims
  `sandboxed`, the warning on every run, and the `sandbox-unavailable`
  refusal on every other host. A route to the network that does not go
  through a filesystem path is a vulnerability. Reaching the host
  filesystem, the host `/proc`, or a filesystem unix socket (`docker.sock`,
  the `ssh-agent` socket) is the documented boundary of this mode, not a
  finding.
- Under `--sandbox`, the reviewed repository's git control files are part of
  the boundary: writes to `<root>/.git`, the git directory a `.git` file
  names, and a linked worktree's common directory are denied on both
  platforms; on macOS so is creating any `.git` path below the root. On both
  platforms a symlinked `.git` or git pointer file, a git directory that is
  the reviewed root or holds it, a pointer that reaches inside the root
  under another spelling (letter case, a macOS firmlink), a git control file
  with a second hard link (object payload — loose objects, packs, and the
  commit-graph and pack-list caches of each git directory, submodules'
  included — excepted; `objects/info/alternates` is not payload), and a git
  control file outside `hooks/` that is a symlink into the root refuse `sandbox-unavailable` ("inside the root" is
  decided by file identity); on Linux
  so does a root whose repository cannot be pinned read-only (no `.git`, a
  git pointer that does not resolve, or a git location whose walk passes
  through a symlink or a missing component inside the root, a link reached
  through an outside-root link included). A sandboxed command that plants a hook or a
  `.git/config` key for the root's repository, for a nested repository or
  submodule whose `.git` existed when the sandbox started, through any
  submodule path in the root's index, or (on macOS) through any new nested
  `.git`, is a vulnerability. Out of scope: the rest of the working tree is
  the PR's content and writable by design, and so is hook code or config the
  operator keeps in the tree (a `core.hooksPath` directory, a `.git/hooks/`
  symlink into the tree, an `include.path` naming a tree file), and on Linux a new nested `.git`
  in a subdirectory that is no submodule path is a documented residual of
  `bwrap`, which can only protect paths that already exist (git honors it
  only when it runs in that subdirectory, as an editor or repository
  scanner that discovers nested repositories may).
- `run --agent` and `loop` review launches: every review launch runs in its
  own disposable copy of the reviewed tree, never in the operator's checkout,
  and only the artifacts that launch is expected to write come back — each a
  lone regular file within its cap, read from a pinned copy as the very file
  the walk saw, and written atomically into the pinned bundle — while anything
  else it wrote inside the copy's wrapper is discarded and named in a
  warning. A write beyond the wrapper is not discarded by the copy: the OS
  review sandbox (below) blocks it on macOS and Linux, and `claude`'s
  (while the operator's own Claude settings allow no write tool) and
  `codex`'s own tools keep them in their copy; on Windows, which has no OS
  sandbox verdict can apply, and under `--allow-unsandboxed-review`, nothing
  blocks it for another agent, and the sandbox's own residuals are tracked
  in STA-539 and STA-540. A copy holds no symlink,
  no `.git` or `.verdict/state` of the tree's in any case, and no pointer into
  the operator's repository, and neither does the shared snapshot it is made
  from; outside a git checkout it holds the bundle alone.
  Also in scope: the shell denial every review launch carries unless
  `--allow-shell` is set (`claude`, `pi`, `bob`; the operator may not pass the
  flag the denial uses), `claude`'s `--setting-sources=user` and
  `--strict-mcp-config` and the flags a review launch owns, `codex`'s
  workspace sandbox with `/tmp` excluded, the private TMPDIR, and `claude`'s
  review write grant, scoped to the launch's own directory inside the bundle
  copy, with the pre-launch walk that refuses a symlink, a non-regular entry,
  or a hard-linked file in the bundle. A review launch that runs in the
  checkout, copies back a file outside its expected artifacts or through a
  link, runs in a copy holding a symlink or a path back to the operator's
  repository, loads the reviewed tree's own agent configuration, runs without
  the denial, or carries a grant verdict renders wider than the launch's own
  bundle directory, is a vulnerability. The shared snapshot is
  symlink-free, owner-only read-only, and fingerprinted by path, mode, size,
  and modification time, plus inode and change time on unix: any write to it
  there, even one that restores the size, mode, and modification time,
  refuses every later copy. On Windows the fingerprint holds the first four
  alone, so a same-size rewrite that restores the modification time is not
  detected.
- The review sandbox around every review launch (`run --agent` finders,
  cross-checks, repair rounds and drain, `loop`'s review, `demo` and
  `backtest` reviews; not the loop's fix spawn, which edits the checkout by
  design), applied to every launch through verdict's own process runners and
  any wrapper around them — only a test double that declares it simulates
  its processes skips it. On macOS the launch runs under `sandbox-exec` with
  a generated profile, on Linux under `bwrap`; the network stays open so the
  agent reaches its model API. A review agent can write only its launch's
  snapshot directory (its working copy, private `TMPDIR`, and the throwaway
  homes opencode, codex and pi get there — `XDG_DATA_HOME`/`XDG_STATE_HOME`,
  `CODEX_HOME`, `PI_CODING_AGENT_DIR` — seeded with no-follow copies of
  their logins and model configuration, for every review launch, sandboxed or
  not), and, for claude alone, its debug log, its login's token refresh, its
  CLI cache, and its launch's own `~/.claude/projects/<slug>` (made before
  the launch and removed after), and the devices a process needs; no other
  row writes anything under the real `HOME`. It cannot write
  anything a later session loads as instructions or code or trusts
  (`~/.claude.json`, claude's settings, hooks, plugins, shell snapshots,
  sessions, todos, file history, backups and caches, every project's memory
  and every other project, all of `~/.codex` — its login, config, cache,
  scratch, `tmp/arg0*` PATH shims, sessions, goals and memories —
  `~/.config/opencode`, opencode's real data and state directories — its
  shadow git repositories, database, managed tools and packages — and all
  of `~/.pi`, including `auth.json`, whose `!command` values run), any other
  dotfile in `HOME`, the rest of
  `~/.config`, the operator's checkout, the execution's shared snapshot, or
  another launch's snapshot directory, and it cannot replace a data
  directory with a link (its own entry is pinned, a linked state path
  refuses, and the state is re-checked before every launch). It cannot read
  the credential table `verify --sandbox` hides (`~/.ssh`, `~/.aws`, gh and
  glab tokens, `.netrc`, `.git-credentials`, the agent logins of codex,
  opencode (its whole data directory: logins, MCP logins, and the database
  that holds them), pi, claude, copilot and gemini, the X11
  cookie, the 1Password, Bitwarden and Secretive ssh-agent socket
  directories, the rest of that list and the variables that relocate them),
  except its own login, nor the host temp roots (and on Linux `/run`). On
  macOS it cannot connect to a unix socket beyond the resolver's and
  syslog's, and cannot look up a Mach service beyond the allowlist
  (directory lookups, trustd, SystemConfiguration, and for claude without
  `--allow-shell` the keychain brokers) — so no preference write,
  pasteboard, keychain (but claude's own while its shell is denied), LaunchServices registration, login item, window server, or
  Shortcuts. On Linux it cannot reach a filesystem socket in `/tmp`,
  `/var/tmp`, `/run`, `/var/run`, or the table, and other processes'
  environments are hidden (a private PID namespace). It inherits no socket
  or display variable, and it cannot push input into the operator's
  terminal: it starts in a session of its own with no controlling terminal,
  and on macOS every read, write, and ioctl on `/dev/tty` and `/dev/ttys*` is
  denied. codex's throwaway home gets only the model, provider, and
  connection keys of the operator's `config.toml` — never its MCP servers,
  `notify` or hook commands, profiles, or sandbox settings, which codex would
  start or apply outside every sandbox. A sandbox policy that
  cannot be built refuses the launch. A review launch that writes, reads,
  connects, or reaches a service past that policy is a vulnerability. Not
  boundaries, and so not findings: everything else stays readable (the agent
  reads the tree and its own config; on Linux a sibling launch's snapshot
  outside a hidden temp root too); on Linux the network namespace is the
  host's, so an abstract-namespace unix socket (a `dbus-launch` session bus,
  an X11 display without its cookie) and a filesystem socket under `HOME`
  outside the table stay reachable; claude's own data paths stay writable,
  so it can alter its own logs, CLI cache, or token store, though nothing a
  later session loads or trusts; the review shell is denied by flags only for
  `claude`, `bob`, and `pi` — every other row's agent has a live shell
  inside the sandbox. `codex` runs in its own `--sandbox workspace-write`
  sandbox instead of verdict's — a nested Seatbelt profile fails on macOS —
  so codex confines its writes to the snapshot but nothing hides the
  credential table from its reads. With no sandbox (Windows, Linux without
  `bwrap`, a failed probe), `claude` (shell denied, write grant scoped to the
  bundle, `--permission-mode default` pinned, and no write-tool allow rule
  at all in the operator's own settings) and `codex` (its own
  sandbox, no operator flag deciding it) run with a warning that their reads
  are not confined, and every other agent refuses
  `review-sandbox-unavailable`; `--allow-unsandboxed-review` (on `run` and
  `loop`) runs every launch without the sandbox, warned, which is the
  operator's choice. Without the sandbox only `claude` and `codex` stay in
  their copy through their own tools, `claude` only while the operator's own
  Claude settings allow no write tool (the opt-out does not check them); the opt-out accepts that any other
  agent can climb out with `..` or an absolute path — to a sibling launch's
  live copy (forging the artifact it hands back) or anything the operator's
  user can write — and read the credential table, so what an opted-out
  review does there is not a finding.
- The `verify` gate's own contract: `verify` executes the agent's claimed
  proof (`verifyCmd` via `bash -c`, 300s watchdog, tail recorded into the
  patch record). The pre-exec refusals are a **tripwire, not a boundary**: the
  gate refuses the spellings it knows, and a `verifyCmd` spelling it does not
  know executes inside whatever boundary you gave it — that ceiling is
  documented in the [manual](docs/manual.md#hostile-pr-sandbox-recipe) and a
  hit on it is a gate gap to report and arm, not a vulnerability. What is in
  scope is the tripwire's own promise: a command that runs after the gate
  refused it, a spawn that happens before the refusal, a refusal that names
  the wrong reason, or a spelling the docs and the fuzz corpus claim refuses
  while it executes.
- The installers (`install.sh`, `install.ps1`): archive checksum, cosign
  verification pinned to the release's published build commit and to a
  push-triggered run, atomic replace, no execution before verification. A
  verification bypass, a path that runs an unverified download, or an install
  that accepts a release without `commit.txt` is a vulnerability.
- The `upgrade` channel: verified self-replace for
  installer-managed installs, owning-manager delegation otherwise
  (never privilege escalation, never background checks).
- The `setup-verdict` action and the skill install/bootstrap paths
  (`install-skill.sh`, `verdict skill install`,
  `scripts/verdict` bootstrap): same bar — no unverified execution,
  no writes outside installer-owned directories.
- Fetched diffs and review comments are untrusted input throughout;
  prompt-injection handling and secret redaction in recorded tails
  are in scope where they cross a trust boundary.
- Control-plane integrity: the review controls (`.verdict/config.json`,
  `.verdict/kb.jsonl`, `.verdict/learnings.md`, and `.verdict/overrides/`
  fragments) are read from the PR base commit and
  snapshotted into the bundle, so a pull request cannot edit the rules
  it is judged by. `post` and `record` refuse a missing or drifted
  snapshot (`controls-unavailable`, `controls-stale`). Any path where
  head bytes steer a review without the recorded
  `--allow-head-controls` opt-out is a vulnerability.
- The verification receipt: `record` and `post` flip a `VERIFIED` claim to
  `addressed` only on the bundle's `verify-receipt.json`, which
  `verdict verify` alone writes. It binds the command, the finding, and the
  git tree of the content the command ran on — a snapshot of that content
  outside the user's checkout: reads that resolve inside the snapshot see the
  snapshot, while a path resolving outside it (an absolute path, or a symlink
  the tree carries) reaches the real filesystem, read-only under `--sandbox`
  and possibly hidden by its rules — and verify writes it only through the bundle directory it
  pinned before the command ran (a symlinked bundle refuses; a bundle moved
  or replaced mid-run records nothing). A path where a claim flips without a
  passing receipt for its command, finding, and the graded content's tree — a
  pass read from `patch.json`, a `triage[]` rationale, content that changed
  after verify, content outside the reviewed bundle (an untracked file), or a
  command that reaches the user's checkout from the snapshot and changes what
  the receipt names — is a vulnerability. The one deliberate exception is
  `verify.share`: ignored directories the operator lists in the bundle's base
  config (the PR base commit, or `HEAD` for a local bundle) join the snapshot
  and can influence a passing receipt. The receipt records them in `shared`,
  `verify` and `gate` show them, and under `--sandbox` they are read-only; a
  shared path that is tracked, not ignored, or not listed in that config, or
  one writable under `--sandbox`, is a vulnerability. So is any code the checkout controls
  running while `verify` makes the snapshot, before the sandbox exists: a git
  hook (including through a `core.hooksPath` that names tree content), a clean
  or smudge filter (git-lfs included), or an fsmonitor program; and so is a
  run that falls back to the user's checkout when the snapshot cannot be made.
  The same holds for every other git call verdict makes on reviewed content
  (`fetch-local`, `fix --test --commit`, `backtest` replay): a hook, an
  fsmonitor, a textconv or external diff driver, or a smudge filter on a
  checkout verdict writes is a vulnerability. Clean and process filters (a
  driver's `clean` or `process` command) on the operator's own working tree
  run by design. The receipt defends against claims nobody observed, not
  against a process with the operator's own file access: such a process
  can rewrite the receipt or `findings.json` directly, so forging the
  receipt that way is out of scope.
- The write posture of `mcp`, `serve`, and `lsp` (`docs/manual.md` § Write
  posture). Each of these is a vulnerability: any write through a `serve`
  bound to a non-loopback `--host`, or under `--read-only` on any surface; a
  `serve` write that passes without the session token, the CSRF token, or the
  Host and Origin checks; and a host post from `serve` without `--allow-post`
  plus a confirmed preview, its single-use nonce, and a matching payload
  digest.

Out of scope: a `verifyCmd` spelling the string gate does not know (the
documented tripwire ceiling above — send it, it gets armed and added to
`evals/fixtures/verify-fuzz-corpus.tsv`), documentation typos (open a regular
issue), missing
hardening wishes with no exploit path (open a feature request), and
vulnerabilities in `apps/docs` build-time dependencies that never
reach the published static site (still reportable, assessed by
reachability).

## Assurance case and audit

The structural argument behind this policy is
[`ASSURANCE_CASE.md`](ASSURANCE_CASE.md): the threat model, the base-owned
review controls, the secret shield and scope preview, the evidence gates, the
sandbox, and the supply chain, each paired with the file, refusal code, or test
that enforces it. The OpenSSF Best Practices audit, including the criteria this
repository does not yet meet, is
[`docs/openssf-best-practices.md`](docs/openssf-best-practices.md).

## Child process environment

Verdict spawns two kinds of child, and their environments differ on purpose.
Do not "fix" the inherited side: it is the documented local posture.

- **Inherited: the fixed `gh` and `git` argv verdict itself issues** (fetch,
  post, triage, `doctor`, and the base-owned control reads). These run with
  the developer's environment because verdict is a local CLI acting for the
  operator: the calls must use the operator's existing `gh` auth and git
  configuration, and shadowing either would break the command rather than
  harden it. A `gh` spawn passes no environment, so the child inherits the
  parent's (`internal/core/exec`). The host choice is not inherited: when the
  target names a host (a URL or `origin` target, `demo <url>`, and every
  `post`, which pins to its bundle's host), every `gh` and `glab` argv names it
  explicitly (`core.PinHostArgv`), so `GH_HOST` or `GITLAB_HOST` cannot redirect
  a call to another host's pull request; the one exception, a GitLab host on a
  non-default port that glab cannot pin, runs unpinned only when the
  environment provably names that exact host and port. A typed `<owner/repo> <pr>` read names
  no host and follows the CLI's default (`docs/manual.md` § Targets). A `git`
  call inherits the environment minus git's location variables,
  `GIT_EXEC_PATH`, and `GIT_CONFIG_PARAMETERS`, with hooks and fsmonitor off
  (`internal/gitcmd`). A syntactic guard in `internal/archtest` fails on a
  `git` argv spelled outside it — a `"git"` literal, a constant concatenation,
  a `sh`/`bash` script naming git — but not on a name computed at run time.
- **Default-deny allowlist: the agent-written `bash -c` verify command.**
  `verify` runs the agent's claimed proof (or deep-group `--command`) with a
  default-deny filtered slice of the caller's environment
  (`internal/core/verify/env.go`), never by inheriting `os.Environ()`
  unfiltered. Only baseline locale, terminal, shell, XDG, and toolchain
  variables (`PATH`, `HOME`, `LANG`, `LC_*`, `TERM`, `TMPDIR`, `TZ`, `CI`,
  `USER`, `LOGNAME`, `SHELL`, color flags, `XDG_*_HOME`, Go/C/Rust/JS/Python
  cache and home variables, plus Windows OS runtime variables on
  `GOOS=windows`) pass by default; any variable whose name matches
  `envsecret.IsSecret` (credential-shaped endings such as `*_TOKEN`,
  `*_SECRET`, `*_KEY`, `*_PASSWORD`, plus `AWS_ACCESS_KEY_ID` and
  `SSH_AUTH_SOCK`, including `LC_TOKEN`) is dropped even when it
  matches an allowlist prefix. An explicit opt-in keeps the named variable even
  when its name is secret-shaped. Operators opt additional variable names in with
  `--env-allowlist NAME[,NAME...]` or `.verdict/verify-env` (one variable name
  per line, `#` comments and blank lines ignored, read exclusively from the
  verified base-commit `controls` snapshot, checked against the bundle's current
  base, so an untrusted PR head cannot widen its own environment unless the
  operator passes `--allow-head-controls`, which reads the worktree instead). Dropped variable names — never values — are reported in
  sorted order in `warnings[]`, and the full parent snapshot remains the
  redaction list for `verifiedTail` in `patch.json`.

The hermetic counterpart for tests is `internal/testenv`: every harness that
spawns `git`, `gh`, or the verdict binary builds its child environment there.
It drops secret-shaped variables (`*_TOKEN`, `*_KEY`, `*_SECRET`, `AWS_*`),
harness knobs (`REVIEW_*`, `GH_*`, `GITHUB_*`, `GIT_CONFIG_*`), and git's own
location variables (`GIT_DIR` and friends, which git exports to hooks), then
pins git's configuration and identity, so a test never inherits the runner's
environment. Each git-running package's `TestMain` applies the same scrub to
the process, so a suite launched from a hook writes only to its fixtures.
Production inherits the operator's, tests never do — both are deliberate.

## Ground rules

No auto-updater phones home by design, and `GH_TOKEN` never leaves
the operator's machine — please do not test with tokens or private data
you do not own, and do not probe live infrastructure beyond a minimal
proof of concept. Local file scope: `verdict` opens only files it is
explicitly handed (bundle inputs, `VERDICT_USAGE`, settings, stores it
owns); it never discovers or opens other tools' transcripts, logs, or
config directories.
