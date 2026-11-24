# Verdict assurance case

This is the security and trust statement for `stainless-code/verdict` — the
structural argument that a review produced by this tool means what it claims.
It is written for an evaluator who will not take the README's word for
anything: every row below names a file, refusal code, test, or CI lane that
exists in this checkout at the commit you are reading, so the claim can be
re-checked without trusting this document.

Two conventions:

- **One claim, one receipt.** The evidence column names the artifact that
  actually enforces the claim. Where the enforcement is a test, the test name
  is given; where it is a review-time refusal, the refusal code is given.
- **Fail closed, then say so.** Requirements Verdict cannot meet are refused
  with a typed code rather than degraded to a warning — except where a section
  of [Known boundaries and non-goals](#7-known-boundaries-and-non-goals)
  states the ceiling out loud. An honest ceiling is part of the case, not a
  hole in it.

Verify any cited path with `git ls-files --error-unmatch <path>`, and any cited
refusal code with
`rg '"<code>"' internal/core/refusalcodes.go internal/core/refusal.go` — the
second file holds the review-input problem codes (`receipt-*`, `spec-*`).

## Contents

1. [Scope and threat model](#1-scope-and-threat-model)
2. [Base-owned review controls](#2-base-owned-review-controls)
3. [Pre-egress secret shield and scope visibility](#3-pre-egress-secret-shield-and-scope-visibility)
4. [Deterministic evidence verification and fail-closed gating](#4-deterministic-evidence-verification-and-fail-closed-gating)
5. [Sandboxed reproduction and network-dead verification](#5-sandboxed-reproduction-and-network-dead-verification)
6. [Zero-dependency static binary and supply-chain integrity](#6-zero-dependency-static-binary-and-supply-chain-integrity)
7. [Known boundaries and non-goals](#7-known-boundaries-and-non-goals)

---

## 1. Scope and threat model

Verdict reviews **hostile pull requests** on the **operator's own machine**. It
is a local CLI: no hosted service, no account, no daemon, no telemetry, and no
background updater. That shape decides the threat model.

| Asset                                     | Trust                               | Where the boundary is drawn                                                                                     |
| ----------------------------------------- | ----------------------------------- | --------------------------------------------------------------------------------------------------------------- |
| The base commit's review controls         | **Trusted**                         | Read through `git show <base>:<path>` by `internal/controls/controls.go`; see §2                                |
| The operator's git and `gh` credentials   | **Trusted, never leaving the host** | Inherited by the fixed `gh`/`git` argv only (`internal/core/exec`); `SECURITY.md` § Child process environment   |
| The review agent's own network egress     | **Trusted, operator-chosen**        | Verdict pins no models and proxies no key; see §7                                                               |
| Pull-request diff bytes                   | **Untrusted**                       | Fenced as evidence for prompts, withheld by exclusion rules (§3)                                                |
| PR title, body, comments, author, threads | **Untrusted**                       | Recorded with `trust` fields; the agent fingerprint is advisory (`internal/core/author.go`)                     |
| Linked issue, `--spec`, `--context` files | **Untrusted**                       | `context.json` stamps `"trust": "untrusted"` and `context.txt` fences the same bytes (manual § Evidence ledger) |
| `.verdict/` control files **on the head** | **Untrusted**                       | Never read for a review unless `--allow-head-controls` is passed, and that choice is stamped (§2)               |
| The agent's `review.json` and `verifyCmd` | **Untrusted**                       | Re-verified against the pinned diff (`internal/evidence`), or executed inside a sandbox (§4, §5)                |
| The installed `verdict` binary            | **Trusted**                         | Checksum always; keyless cosign over `checksums.txt` when `cosign` is installed (§6)                            |

**In scope.** Prompt injection through the diff, the PR body, threads, or a
linked issue; a pull request editing the rules it is judged by; a credential
path reaching the reviewing agent; a fabricated or unverifiable finding quote;
a review whose coverage receipt does not match the fetched diff; an agent
`verifyCmd` that reaches
the network, reads credentials, or escapes the repository; a tampered release
archive.

**Out of scope.** Protecting an operator from an agent CLI they chose to trust;
protecting a machine that is already compromised; OS-level containment of the
`verdict` process itself (that is the operator's shell, container, or VM); and
judging the review model's accuracy — the case below is about _evidence
integrity_, which is why §4 gates on verifiability rather than on agreement.

## 2. Base-owned review controls

A pull request must not be able to edit the policy that judges it. Verdict
resolves every `.verdict/` control from the **PR base commit**, snapshots it
into the bundle, re-hashes the snapshot at posting time, and refuses when the
snapshot is missing or has drifted.

| Claim                                                                  | Enforced by                                                                                                       | Evidence                                                                                                                                          |
| ---------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------- |
| Controls are read from the base commit, never the head                 | `internal/controls`                                                                                               | `internal/controls/controls.go` (package doc), `internal/controls/git.go` — `TestResolveReadsBaseNotHead` in `internal/controls/controls_test.go` |
| The inventory is closed: typed config plus tracked support files       | `internal/controls/controls.go` (`Inventory`)                                                                     | same file, plus `internal/controls/contents.go`                                                                                                   |
| A missing or drifted snapshot refuses posting                          | `controls-unavailable`, `controls-stale` (`internal/controls/controls.go`)                                        | `TestGateRefusals`, `TestGateRefusesAPolicyBaseSnapshotOfAnotherCommit` in `internal/controls/controls_test.go`                                   |
| The single head-reading opt-out is recorded, not silent                | `--allow-head-controls` → `policy: "head"` in `controls.json`                                                     | `PolicyBase` / `PolicyHead` in `internal/controls/controls.go`; `docs/manual.md` § Review controls are base-owned                                 |
| Built-in pack composition is measured against the default branch       | `internal/core/pack/baseconfig.go`                                                                                | `baseRefCandidates` deliberately omits `@{upstream}` so a PR branch cannot merge-base against itself                                              |
| Repository packs are vendored and locked                               | `verdict pack add` writes the lock file; `pack-lock-missing` / `pack-lock-drift` refuse an unpinned or moved pack | `internal/core/pack/lock.go`; `cmd/verdict/usage.go` § `pack`; both codes in `internal/core/refusalcodes.go`                                      |
| An edited pack or config snapshot refuses instead of re-reading head   | `controls-stale`                                                                                                  | `internal/controls/controls.go` (`CodeStale`); `cmd/verdict/usage.go` § `pack`; `internal/core/recipe/load.go` reads a recorded snapshot          |
| Strict, typed configuration: unknown fields and credential keys refuse | `bad-config`, `bad-config-credentials`, `bad-requirement`, `bad-scope`                                            | `internal/core/refusalcodes.go`; `.verdict/config.json` in this repository                                                                        |
| Layer precedence cannot raise the repository budget                    | personal settings carry a ceiling only                                                                            | `docs/policy-contract.md` § Terms; `spend-capped` vs `budget-exceeded` split                                                                      |

The shipped repository exercises this on itself: `.verdict/config.json`,
`.verdict/kb.jsonl`, and `.verdict/recipes.json` are committed, and
`evals/cases/controls-base.sh` grades the base-read path offline. A control
change takes effect on the **next** review after it lands, which is what makes
the guarantee one-directional.

## 3. Pre-egress secret shield and scope visibility

Two promises: a secret-bearing path never reaches an agent's context, and an
operator can see exactly what would reach it **before** anything is written or
spent. One stated exception keeps the first promise honest: the five `.env`
template spellings (`.env.example`, `.env.sample`, `.env.template`, `.env.dist`,
`.env.defaults`) are not withheld, and the advisory `sensitive.txt` scan exempts
the same five, because they carry key names instead of values — matched by exact
basename, so a state document such as `prod.tfstate.env.example` is still
withheld.

| Claim                                                                | Enforced by                                                                      | Evidence                                                                                                                                                  |
| -------------------------------------------------------------------- | -------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------- |
| A built-in secret floor withholds credential paths unconditionally   | `internal/exclude/secrets.go`, reason `secret-builtin`, rule id `default:secret` | `internal/exclude/secrets.go`; `docs/exclusions.md` § Diff exclusions                                                                                     |
| No flag, negation, or ignore file can re-include a withheld secret   | the floor is applied last and is not overridable                                 | `TestSecretExclusionsSurviveEveryOverride` in `internal/core/fetch/secret_exclusion_test.go`; `evals/cases/ignore-floor.sh`                               |
| `.env` templates stay reviewable, secrets do not                     | five `.env.*` template spellings are exempt by exact basename                    | `TestSecretExclusionsKeepEnvTemplates` in `internal/core/fetch/secret_exclusion_test.go`                                                                  |
| A rename cannot smuggle a secret past the floor                      | both sides of a rename are probed                                                | `docs/exclusions.md` § Diff exclusions; matching is ASCII case-insensitive over the cleaned path                                                          |
| Withholding is receipted, per section, with rule and byte count      | `excluded.json`                                                                  | `internal/core/excluded.go`; `schemas/excluded.schema.json`                                                                                               |
| `fetch --preview` / `fetch-local --preview` show scope before egress | `internal/core/fetch/preview.go`                                                 | `TestFetchPreviewLeavesNoBundle`, `TestFetchPreviewJSONEnvelope` in `internal/core/fetch/secret_exclusion_test.go`; `--preview` in `cmd/verdict/usage.go` |
| A preview writes no bundle, spends nothing, and cannot post          | preview stops before any bundle write                                            | `docs/exclusions.md` § Reports and measurements; `data.preview` in `schemas/envelope.schema.json`                                                         |
| Secret values are redacted out of recorded command tails             | the parent environment snapshot is the redaction list for `verifiedTail`         | `internal/core/verify/redact.go`; `SECURITY.md` § Child process environment                                                                               |

The preview is deliberately **not** a receipt: it resolves and prints, and it
records nothing (see `docs/policy-contract.md`, "Untrusted preview"). That
separation is why a preview cannot be mistaken for a passed gate.

## 4. Deterministic evidence verification and fail-closed gating

The agent writes findings; the binary decides whether those findings are
backed by evidence. Nothing in this section involves a model call: every
comparison is a byte or offset comparison against the fetched diff.

| Claim                                                                         | Enforced by                                                                       | Evidence                                                                                                                                                             |
| ----------------------------------------------------------------------------- | --------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| A finding's quote must match the pinned diff under documented tolerances      | `internal/evidence/quote.go` (`MatchQuote`, `MaxQuoteLines`, `MaxQuoteBytes`)     | `TestMatchQuoteA2`, `TestMatchQuoteBounds` in `internal/evidence/quote_test.go`                                                                                      |
| Anchors validate against hunks pinned at fetch time, not live `HEAD`          | the commentable set is written by `fetch`/`fetch-local`                           | `docs/manual.md` § Quickstart; `internal/hunks`; `internal/core/post/payload.go`                                                                                     |
| Every non-exempt changed hunk needs a receipt whose quote is in that hunk     | `internal/evidence/receipt.go` (`VerifyReceipts`, coverage `Complete`)            | `receipt-hunk-unknown`, `receipt-mismatch` (`internal/core/refusal.go`); `TestVerifyReceiptsA3` in `internal/evidence/receipt_test.go`                               |
| Every enumerated spec item needs a `spec_checks[]` row                        | `internal/evidence/spec.go` (`MaxSpecItems` = 150, `DefaultSpecCoverageMin` = 80) | `spec-item-unknown`, `spec-check-missing`, `spec-check-unmet` (`internal/core/refusal.go`); `TestVerifySpecChecksAndCoverage` in `internal/evidence/spec_test.go`    |
| An unbacked review is refused, not approved                                   | `review-insufficient`                                                             | `internal/core/evidence.go` (`CodeReviewInsufficient`); `internal/core/refusalcodes.go`                                                                              |
| The approval decision is computed by the binary, not asked of the agent       | `internal/core/approval`                                                          | package doc: "No model writes the result"; `DefaultApprovalTier`; `TestNoConfigDefaultThreshold` in `internal/core/approval/approval_test.go`                        |
| A finding the evidence pass did not verify never gates, and never reads green | unbacked findings leave an insufficient decision                                  | `TestUnbackedFindingNeverGatesButIsInsufficient`, `TestFindingWithoutVerificationRecordIsUnbacked` in the same file                                                  |
| `--force` overrides a count of findings, never the absence of evidence        | `post` binding approval                                                           | `docs/manual.md` § Guarded submission and operator surfaces; `docs/policy-contract.md` § Rule table                                                                  |
| The recorded decision is bound to the review, diff, and head it decided on    | `record.json`                                                                     | `record-stale` (`internal/core/refusalcodes.go`); `internal/core/record/service.go`                                                                                  |
| An interrupted two-finder run resumes only on a verified fingerprint          | `internal/core/run/resume.go` (`resumeFingerprint`, `digest`)                     | `cannot-resume`, `resume-drift`; `TestResumeRefusesDriftedInputs` in `internal/core/run/resume_test.go`                                                              |
| Bound artifacts are sealed by SHA-256, one key per input                      | `internal/core/run/resume.go`                                                     | `full_diff_sha256`, `run_json_sha256`, `inputs_sha256`, `config_sha256`, `context_txt_sha256`, `launch_def_sha256`, `pass_through_sha256`                            |
| Re-running a completed execution spends nothing                               | resume short-circuits a complete record                                           | `TestResumeOfCompleteExecutionIsIdempotent`, `TestResumeReusesCompletedCrossChecks` in `internal/core/run/resume_test.go`                                            |
| Gates fail closed on the receipt, not on the wording                          | `report --check` / `gate` `evidence` dimension                                    | `internal/core/evidence.go` (`InsufficientRefusal`); `docs/manual.md` § Evidence checks; `TestVerifyReceiptsVacuousZeroHunks` in `internal/evidence/receipt_test.go` |

One consequence worth stating plainly: a review whose findings are all below
the approval tier still needs a complete receipt
(`docs/manual.md`, "Receipts"). A quiet review is not a cheap one to fake:
what a receipt proves is that quoted bytes sit in the fetched diff for that
hunk. It does not prove the agent read, understood, or assessed the hunk —
matching diff text can be copied without either — so a clean receipt means the
review is _checkable_, not that it is thorough.

## 5. Sandboxed reproduction and network-dead verification

`verify` executes the agent's claimed proof. That is the single most dangerous
operation in the tool, so it is layered: a static refusal gate, then an
enforced sandbox, then an honest statement of what the gate cannot see.

| Claim                                                                                | Enforced by                                                                                                                           | Evidence                                                                                                                                                                               |
| ------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Hostile spellings are refused before anything spawns                                 | `internal/core/verify/gate.go` and the per-class gate files                                                                           | `verify-destructive`, `verify-networked`, `verify-credential-read`, `verify-repo-escape`, `verify-indirection`                                                                         |
| `verify --sandbox` hides credential paths and denies the network on Linux            | `bubblewrap`: private `/tmp`, empty `/run`, `--unshare-pid`, `--unshare-net`, `--new-session`, `--die-with-parent`, credential mounts | `internal/core/verify/sandbox.go`; `docs/manual.md` § Enforced sandbox. `--ro-bind / /` leaves the rest of the host readable, so out-of-repo read scope still rests on the string gate |
| Credential paths are mounted away, not merely unmentioned                            | `sandboxCredentialPaths` (dirs behind an empty tmpfs, files behind `/dev/null`)                                                       | `internal/core/verify/sandbox.go`                                                                                                                                                      |
| macOS denies the network, every Mach service, and every write outside the run        | `(deny network*)`, `(deny mach-lookup)` with one lookup-only allow, `(deny file-write* (subpath "/"))`, allows, credential denies     | `sandboxExecProfile` in `internal/core/verify/sandbox.go`; `TestSandboxExecDeniesServiceMediatedChannels` runs the real `sandbox-exec`                                                 |
| A host that cannot enforce refuses rather than degrading                             | `sandbox-unavailable`                                                                                                                 | `internal/core/verify/sandbox.go`; `docs/manual.md` § Enforced sandbox                                                                                                                 |
| The sandbox shape is recorded in the artifact, not inferred                          | `"sandboxed": true` (full shape only) plus the mechanism name; network-only records `unshare-net`, never `sandboxed`                  | `internal/core/verifyapi.go` (`Sandboxed`, `SandboxMechanism`); `TestRecordOutcomeNetworkOnlyIsNotSandboxed`; `docs/manual.md` § Enforced sandbox                                      |
| The gate's own ceiling is documented as a tripwire, not a boundary                   | re-execution cannot be proven by string matching                                                                                      | `SECURITY.md` § Scope; `docs/manual.md` § Known residuals (string-scan ceiling)                                                                                                        |
| Eval hosts are network-dead by construction                                          | `evals/lib/hoststub.sh` refuses any argv a case did not register, with proxied env                                                    | `evals/lib/hoststub.sh`; `AGENTS.md` § Hazards                                                                                                                                         |
| The Linux eval lane proves containment, not just refusal                             | `EVAL_REQUIRE_SANDBOX=1`, a real `bwrap` namespace probe, and a summary re-read                                                       | `.github/workflows/ci.yml` § `evals` — "Prove bubblewrap can create its namespaces", "Prove the verify sandbox containment case ran"                                                   |
| Child processes get a hermetic environment in tests and a filtered one in production | `internal/testenv`; `internal/core/verify/env.go`                                                                                     | `SECURITY.md` § Child process environment; `internal/testenv/testenv.go`                                                                                                               |

## 6. Zero-dependency static binary and supply-chain integrity

| Claim                                                              | Enforced by                                                                 | Evidence                                                                                                                  |
| ------------------------------------------------------------------ | --------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------- |
| Zero third-party runtime dependencies                              | `go.mod` lists a module path and a Go version, nothing else                 | `go.mod`                                                                                                                  |
| The module graph is ratcheted at zero                              | `deps 0` in the byte budget                                                 | `scripts/size-baseline.txt`; `scripts/size.sh` (`graph_deps`) refuses growth over the `deps` ceiling                      |
| Six static binaries are built and vetted for every shipped target  | `make cross` over `release/contract.json`                                   | `cmd/crossplatforms`; `release/contract.json`; `.goreleaser.yml` (`CGO_ENABLED=0`, `-trimpath`, `-s -w`)                  |
| The binary's size is ratcheted per target                          | `make size`                                                                 | `scripts/size.sh`; runs in the `cross` job of `.github/workflows/ci.yml`                                                  |
| Release checksums are signed keylessly and the signature is graded | cosign over `checksums.txt`                                                 | `.goreleaser.yml` § `checksums`/`signs`; `.github/workflows/release.yml` § "Verify the published checksums signature"     |
| Every archive carries an SPDX SBOM                                 | syft via GoReleaser                                                         | `.goreleaser.yml` § `sboms`; `.github/workflows/release.yml` § "Install Syft"                                             |
| The signature is pinned to one workflow, one ref, one commit       | identity, issuer, `push` trigger, and the `commit.txt` SHA claim            | `release/contract.json` (`identity`, `issuer`, `identityRegexp`); `.github/workflows/release.yml`                         |
| Installers verify before executing                                 | `cosign` when present, archive SHA-256 always, refusal without `commit.txt` | `install.sh`, `install.ps1`; `CONTRIBUTING.md` § Release train; `evals/cases/installer-binary.sh`                         |
| Dependencies are bumped by automation, grouped and reviewable      | Dependabot for actions, Go modules, and npm                                 | `.github/dependabot.yml`                                                                                                  |
| Known vulnerabilities are checked on every Go run                  | `govulncheck`, pinned version                                               | `Makefile` § `vuln`, called from `ci-go`; `.github/workflows/ci.yml` job `go`                                             |
| Linters, formatters, and shell checks gate the merge               | `golangci-lint`, `gofmt`, `shellcheck -S warning`, `bun fmt --check`        | `.github/workflows/ci.yml` jobs `go`, `go-windows`, `lint`, `format`; `CONTRIBUTING.md` § The merge bar                   |
| The required-lane list is itself reviewed                          | one declaration in `ci-complete`, drift fails the build                     | `.github/workflows/ci.yml` § `ci-complete`; `TestCICompleteGateDeclaration` in `internal/archtest/ci_gate_test.go`        |
| A golden test that silently skipped cannot look green              | CI re-runs one golden verbosely and greps for `--- PASS`                    | `.github/workflows/ci.yml` § "Prove the golden tests ran"; `TestGateGolden` in `internal/core/verify/gate_golden_test.go` |
| Supply-chain posture is scored publicly                            | OpenSSF Scorecard, SARIF uploaded to code scanning                          | `.github/workflows/scorecard.yml`                                                                                         |
| Fuzzing and benchmarks run, off the pull-request path              | nightly schedule lanes                                                      | `.github/workflows/ci.yml` jobs `fuzz`, `bench`; `FUZZ_TARGETS` in `Makefile`                                             |

## 7. Known boundaries and non-goals

Everything below is a limit the project states deliberately. An evaluator
should treat this list as part of the case: a boundary nobody wrote down is a
boundary nobody can rely on.

**The `verify` string gate is a tripwire, not a boundary.** It refuses the
spellings it knows and does not resolve variables. A `verifyCmd` spelling it
has not learned executes inside whatever boundary the operator supplied. The
ceiling, including file-content weapons, module-resolution paths, interpreter
mediation, and `curl | python3` pipes, is enumerated in `docs/manual.md` §
Known residuals (string-scan ceiling) and restated as in-scope-but-armed signal
in `SECURITY.md`. The enforced boundary is `verify --sandbox`, or a container
the operator runs the whole review in.

**macOS has no filesystem namespace.** On macOS the sandbox is a Seatbelt
policy, not a set of private mounts: `sandbox-exec` denies the network, the
Mach services (one lookup-only allow), every write outside the reviewed root
and the per-run private `TMPDIR`, and reads of the credential table and the
shared host temp roots, while the command can still read the rest of the host
filesystem and see the host process tree. The profile is built in
`sandboxExecProfile` (`internal/core/verify/sandbox.go`) and stated in
`docs/manual.md` § Enforced sandbox, which points full mount and PID isolation
at a Linux VM or container. Windows, and any host with neither `bwrap` nor
`sandbox-exec`, refuses `sandbox-unavailable` and runs nothing.

**The network-only mode is not the sandbox.** `verify --sandbox-network-only`
is an explicit Linux opt-in for hosts without `bwrap`: `unshare --net` removes
the route out and nothing else, so the host filesystem, the host `/proc`, and
filesystem unix sockets stay reachable. It records `sandboxMechanism:
"unshare-net"`, never `sandboxed`, and warns on every run.

**The reviewed root is writable; its git control files are not.** The working
tree is the PR's own content, so writes there are by design. Writes to the
repository's git directories (`.git`, a `.git` file's target, a linked
worktree's common directory) are denied on both platforms, and macOS also
denies creating any `.git` path below the root (`sandboxGitDirs`,
`sandboxGitBinds`, `sandboxGitWriteDenials` in
`internal/core/verify/sandbox.go`; `TestSandboxExecDeniesGitControlWrites`).
On Linux a root whose repository cannot be pinned read-only refuses — no
`.git`, a symlinked `.git`, a git pointer that does not resolve, or one
reached through an in-root symlink — because `bwrap` could protect it only by
writing a mount point into the host tree
(`TestSandboxArgvRefusesARootWithoutGitDir`,
`TestSandboxArgvRefusesUnprotectableGitPointers`); a new nested `.git` in a
subdirectory remains possible there, and `docs/manual.md` § Enforced sandbox
says so.

**The review agent's egress is the operator's choice.** Verdict pins no models,
proxies no key, and makes no network call of its own for a review. The agent CLI
authenticates with the operator's credential and contacts its model provider
directly, so "code never leaves the host" is a property of the model the
operator configures, not of `verdict`. `README.md` § Who it's for states the
same limit for the regulated-environment case.

**The head-reading opt-out is real.** `--allow-head-controls` reads controls
from the worktree and stamps that choice in `controls.json`. It exists for
local iteration; a review run with it is not base-protected, and the stamp is
how a reader can tell.

**A preview is not a gate.** `config --preview` and
`fetch --preview` resolve and print, write no state, and certify nothing
(`docs/policy-contract.md`, "Untrusted preview"; `docs/exclusions.md` § Reports
and measurements). Display never certifies a check: `report` records no
outcome.

**Scanner evidence is a pilot, not a claim.** `contrib/scanner-evidence/` is an
operator-invoked out-of-tree adapter. Its README states plainly that a rule
match is a candidate, never "reproduced", and that a clean scan never means
safe. Verdict core does not import, discover, or execute it.

**Review quality is bounded by the model, not proven by the gate.** The gate
proves that a finding's quote is real and that every required hunk carries a
receipt quoting bytes the fetched diff contains. It does not prove the agent
understood those bytes — a receipt can copy matching diff text without
assessing it — and it does not prove the finding is the most important one. A
clean receipt on a review that missed a defect is still a clean receipt. The
measured results — including the recall figures that are _not_ flattering —
are published under `bench/quality/results/` and receipted row by row in
`docs/landing-claims.md`.

**Governance artifacts are out of scope for this document.** There is no
`CODE_OF_CONDUCT.md` or separate governance policy in this checkout; the
OpenSSF criteria that require them are Silver-level and are recorded as such in
`docs/openssf-best-practices.md`. Commit rights, `CODEOWNERS`, and the release
gate are described in `CONTRIBUTING.md`.

**Nothing phones home.** No telemetry, no auto-updater, no background checks.
`verdict upgrade` runs only when invoked. That is a design constraint, and it
is why the tool's security value does not depend on a service the project
operates.
