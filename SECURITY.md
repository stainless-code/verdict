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

- The `verify` gate: `verify` executes the agent's claimed proof
  (`verifyCmd` via `bash -c`, 300s watchdog, tail recorded into the
  patch record). Its pre-exec refusals (destructive, networked,
  credential-reading, repo-escaping commands) are a security boundary;
  any bypass is a vulnerability.
- The installers (`install.sh`, `install.ps1`): archive checksum and
  cosign verification, atomic replace, no execution before
  verification. A verification bypass or a path that runs an
  unverified download is a vulnerability.
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
- Control-plane integrity: the review controls (`.review/kb.jsonl`,
  `panel`, `budget`, `max_bytes`, `chunk`, `kb-policy`, `learnings.md`,
  and the overrides fragments) are read from the PR base commit and
  snapshotted into the bundle, so a pull request cannot edit the rules
  it is judged by. `post` and `record` refuse a missing or drifted
  snapshot (`controls-unavailable`, `controls-stale`). Any path where
  head bytes steer a review without the recorded
  `--allow-head-controls` opt-out is a vulnerability.

Out of scope: documentation typos (open a regular issue), missing
hardening wishes with no exploit path (open a feature request), and
vulnerabilities in `apps/docs` build-time dependencies that never
reach the published static site (still reportable, assessed by
reachability).

## Child process environment

Verdict spawns two kinds of child, and their environments differ on purpose.
Do not "fix" the inherited side: it is the documented local posture.

- **Inherited: the fixed `gh` and `git` argv verdict itself issues** (fetch,
  post, triage, `doctor`, and the base-owned control reads). These run with
  the developer's environment because verdict is a local CLI acting for the
  operator: the calls must use the operator's existing `gh` auth and git
  configuration, and shadowing either would break the command rather than
  harden it. The spawn site passes no environment, so the child inherits the
  parent's (`internal/core/exec`).
- **Explicit: the agent-written `bash -c` verify command.** `verify` runs the
  agent's claimed proof with an environment the caller handed in as an
  ordered snapshot (`internal/core/verify/service.go`), never by inheriting
  silently. The same snapshot is the redaction list for the recorded tail, so
  a token the operator passed to verdict is scrubbed from `.verify.out` and
  `patch.json`. This is the untrusted side: the command text is agent output,
  so what the child can see is decided at the edge instead of defaulting to
  whatever the shell happened to export.

The hermetic counterpart for tests is `internal/testenv`: every harness that
spawns `git`, `gh`, or the verdict binary builds its child environment there.
It drops secret-shaped variables (`*_TOKEN`, `*_KEY`, `*_SECRET`, `AWS_*`) and
harness knobs (`REVIEW_*`, `GH_*`, `GITHUB_*`, `GIT_CONFIG_*`), and pins git's
configuration, so a test never inherits the runner's environment. Production
inherits the operator's, tests never do — both are deliberate.

## Ground rules

No auto-updater phones home by design, and `GH_TOKEN` never leaves
the operator's machine — please do not test with tokens or private data
you do not own, and do not probe live infrastructure beyond a minimal
proof of concept. Local file scope: `verdict` opens only files it is
explicitly handed (bundle inputs, `VERDICT_USAGE`, settings, stores it
owns); it never discovers or opens other tools' transcripts, logs, or
config directories.
