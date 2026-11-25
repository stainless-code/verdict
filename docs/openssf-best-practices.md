# OpenSSF Best Practices badge audit

This page audits Verdict against the **OpenSSF Best Practices Badge** criteria
at the **Passing** level — the numeric criteria categories the badge project
publishes (Basics, Change Control, Reporting, Quality, Security, Analysis).
It is a working audit, not a badge claim: the badge itself is issued by
`bestpractices.dev` after a maintainer registers the project and answers the
form, and no badge is claimed here until that has happened.

Two rules for reading it:

- **`Met` means a receipt exists in this checkout.** Every `Met` row names the
  file, refusal code, or CI lane that substantiates it.
- **No invented identity.** There is no numeric `bestpractices.dev` project ID
  in this repository, and no badge image URL, because none has been issued
  yet. `README.md` links here instead of showing a badge it cannot back up.

Legend: `Met` — proven by a repo artifact; `N/A` — genuinely not applicable to
this project, with the reason given; `Operator action required` — only a
maintainer can answer it: a live-hosting fact, a GitHub setting, a response
history, a first-person knowledge or remediation attestation, or the
registration itself.

Status levels follow the badge's own vocabulary: **MUST** criteria block the
Passing badge, **SHOULD** and **SUGGESTED** criteria do not.

## Registration: the maintainer step

This is the one step no repository file can perform, and it is why this audit
carries no badge.

1. Sign in at <https://www.bestpractices.dev> with the maintainer's GitHub
   account.
2. Register the project once, naming the repository URL
   `https://github.com/stainless-code/verdict` and its license (MIT). The site
   shows its project-creation flow after sign-in; completing it yields a
   numeric **project ID** in the project URL.
3. Answer the Passing form using the evidence columns below, and attach the
   URL the badge form requests to each `URL required` criterion. Repository
   files are addressable directly, for example
   `https://github.com/stainless-code/verdict/blob/main/SECURITY.md`.
4. Only after the site awards **Passing**, add the badge image and the project
   page link to `README.md` beside the Trust and assurance links, and replace
   the "no badge is claimed" sentence at the top of this page with the awarded
   project ID.
5. Record the awarded date here, so a later reader can tell an audit in
   progress from an awarded badge.

Re-run this audit whenever a criterion's evidence moves — a change to
`SECURITY.md`, `CONTRIBUTING.md`, the CI required-lane list, or the release
signing chain invalidates the rows that name them.

## Basics

| Criterion                   | Level     | Status | Evidence                                                                                                                                    |
| --------------------------- | --------- | ------ | ------------------------------------------------------------------------------------------------------------------------------------------- |
| `description_good`          | MUST      | Met    | `README.md` opening paragraph; `package.json` `description`: "Code review any coding agent can run: fetch, review, gate, post."             |
| `interact`                  | MUST      | Met    | `README.md` § Quickstart and § Install (obtain); `.github/ISSUE_TEMPLATE/config.yml` (feedback); `CONTRIBUTING.md` (contribute)             |
| `contribution`              | MUST      | Met    | `CONTRIBUTING.md` § Development setup, § The merge bar, § Commit style; `docs/contributing-packs.md` for packs, fixtures, and host adapters |
| `contribution_requirements` | SHOULD    | Met    | `CONTRIBUTING.md` § The merge bar (five checks, each a CI lane); `AGENTS.md` § Tests and § Verification ladder                              |
| `floss_license`             | MUST      | Met    | `LICENSE` (MIT); `package.json` `"license": "MIT"`                                                                                          |
| `floss_license_osi`         | SUGGESTED | Met    | MIT is OSI-approved — `LICENSE`                                                                                                             |
| `license_location`          | MUST      | Met    | `LICENSE` at the repository root; `NOTICE.md` records the adapted-prompt lineage                                                            |
| `documentation_basics`      | MUST      | Met    | `README.md` § Quickstart; `docs/manual.md`; `docs/troubleshooting.md` (every refusal code and its fix)                                      |
| `documentation_interface`   | MUST      | Met    | `docs/manual.md` (every verb, flag, store file, exit code); `schemas/` (published JSON Schemas); `docs/adding-a-verb.md`                    |
| `sites_https`               | MUST      | Met    | `https://stainless-code.com/verdict` in `README.md`; `install.sh`/`install.ps1` fetch over HTTPS only; `package.json` `homepage`            |
| `discussion`                | MUST      | Met    | GitHub Issues with templates in `.github/ISSUE_TEMPLATE/`; `CONTRIBUTING.md` § Issues and pull requests                                     |
| `english`                   | SHOULD    | Met    | All documentation and templates are written in English                                                                                      |
| `maintained`                | MUST      | Met    | `CHANGELOG.md` (changeset-driven releases); `.github/workflows/release.yml`; `CODEOWNERS`. Maintainer confirms current activity in the form |

## Change Control

| Criterion             | Level     | Status | Evidence                                                                                                                                                                            |
| --------------------- | --------- | ------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `repo_public`         | MUST      | Met    | Public repository `stainless-code/verdict` — `package.json` `repository`, every workflow `uses:`/URL, and `README.md` badge and clone links                                         |
| `repo_track`          | MUST      | Met    | git; `.gitattributes`; `AGENTS.md` § Git and commits                                                                                                                                |
| `repo_interim`        | MUST      | Met    | Small, focused commits and PRs — `CONTRIBUTING.md` § Commit style; `CHANGELOG.md`                                                                                                   |
| `repo_distributed`    | SUGGESTED | Met    | git                                                                                                                                                                                 |
| `version_unique`      | MUST      | Met    | `package.json` `version`; `vX.Y.Z` tags; `CHANGELOG.md`; `release/contract.json` freezes artifact names per version                                                                 |
| `version_semver`      | SUGGESTED | Met    | `package.json` `version`; `CONTRIBUTING.md` § Versioning                                                                                                                            |
| `version_tags`        | SUGGESTED | Met    | `CONTRIBUTING.md` § Versioning: "Tags (`vX.Y.Z`) follow the releases"; `scripts/release.sh`                                                                                         |
| `release_notes`       | MUST      | Met    | `CHANGELOG.md`; `scripts/release-notes.mjs` renders the release body from it, led by `docs/releases/<version>.md` highlights; `.goreleaser.yml` disables GoReleaser's own changelog |
| `release_notes_vulns` | MUST      | N/A    | No publicly known vulnerability has been fixed to date. When one is, its entry lands in `CHANGELOG.md`, which is the release body: `scripts/release-notes.mjs`                      |

## Reporting

| Criterion                       | Level  | Status                   | Evidence                                                                                                                            |
| ------------------------------- | ------ | ------------------------ | ----------------------------------------------------------------------------------------------------------------------------------- |
| `report_process`                | MUST   | Met                      | `.github/ISSUE_TEMPLATE/bug_report.yml`; `CONTRIBUTING.md` § Issues and pull requests                                               |
| `report_tracker`                | SHOULD | Met                      | GitHub Issues, with `blank_issues_enabled: false` and routed contact links in `.github/ISSUE_TEMPLATE/config.yml`                   |
| `report_responses`              | MUST   | Operator action required | Live tracker behaviour. `CODEOWNERS` names the reviewer; the maintainer confirms response history in the form                       |
| `enhancement_responses`         | SHOULD | Operator action required | Same live behaviour; `.github/ISSUE_TEMPLATE/feature_request.yml` routes proposals                                                  |
| `report_archive`                | MUST   | Met                      | GitHub Issues archive is public and permanent for a public repository                                                               |
| `vulnerability_report_process`  | MUST   | Met                      | `SECURITY.md` § Reporting a vulnerability                                                                                           |
| `vulnerability_report_private`  | MUST   | Met                      | `SECURITY.md` requires GitHub private vulnerability reporting; `.github/ISSUE_TEMPLATE/config.yml` links `/security/advisories/new` |
| `vulnerability_report_response` | MUST   | Operator action required | Response history. `SECURITY.md` commits to acknowledgement within 3 business days and an agreed disclosure timeline                 |

## Quality

| Criterion                     | Level     | Status                   | Evidence                                                                                                                              |
| ----------------------------- | --------- | ------------------------ | ------------------------------------------------------------------------------------------------------------------------------------- |
| `build`                       | MUST      | Met                      | `make build`; `CONTRIBUTING.md` § Development setup; `.github/actions/setup`                                                          |
| `build_common_tools`          | SUGGESTED | Met                      | `Makefile` plus the Go toolchain (`go.mod`); `package.json` scripts                                                                   |
| `build_floss_tools`           | SHOULD    | Met                      | Go, `make`, Bun, and syft/cosign are all FLOSS; `go.mod` carries no third-party module                                                |
| `test`                        | MUST      | Met                      | `make test-race` (`go test -race -shuffle=on -count=1 ./...`) and `make evals` (`evals/run.sh`)                                       |
| `test_invocation`             | SHOULD    | Met                      | `Makefile` targets `test-race` and `evals`; `evals/one.sh <case>` grades one case                                                     |
| `test_most`                   | SUGGESTED | Operator action required | No published coverage figure or badge. Add one if the project wants the point; nothing in the tree claims a coverage percentage       |
| `test_continuous_integration` | SUGGESTED | Met                      | `.github/workflows/ci.yml` runs on every push to `main`, every PR, and nightly                                                        |
| `test_policy`                 | MUST      | Met                      | `AGENTS.md` § Tests (what a test must be able to fail on); `CONTRIBUTING.md` § The merge bar                                          |
| `tests_are_added`             | MUST      | Met                      | `internal/archtest` guards the suite's own structure; `docs/ci-and-tests.md` (skip-token ban, determinism); review is gated on tests  |
| `tests_documented_added`      | SUGGESTED | Met                      | `AGENTS.md` § Verification ladder; `CONTRIBUTING.md` § Golden tests and § The merge bar                                               |
| `warnings`                    | MUST      | Met                      | `go vet`, `gofmt`, `golangci-lint` with `.golangci.yml`, `shellcheck -S warning`, `bun fmt --check` — `.github/workflows/ci.yml`      |
| `warnings_fixed`              | MUST      | Met                      | `.golangci.yml` must report 0 issues (`CONTRIBUTING.md` § The merge bar #3); CI runs the pinned action                                |
| `warnings_strict`             | SUGGESTED | Met                      | `.golangci.yml` enables `gosec`, `staticcheck`, `revive`, `errorlint`, `containedctx`, `noctx`, `forcetypeassert`, `cyclop`, and more |

## Security

| Criterion                        | Level  | Status                   | Evidence                                                                                                                                                                                                                                                            |
| -------------------------------- | ------ | ------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `know_secure_design`             | MUST   | Operator action required | Primary-developer knowledge, attested in the badge form. Supporting evidence: `ASSURANCE_CASE.md` § 1–§ 5; `SECURITY.md` § Scope; `docs/manual.md` § Hostile-PR sandbox recipe                                                                                      |
| `know_common_errors`             | MUST   | Operator action required | Primary-developer knowledge, attested in the badge form. Supporting evidence: `SECURITY.md` § Scope names the failure classes it owns; `internal/core/verify/gate_security_test.go` and `gate_fuzz_test.go`; `evals/fixtures/verify-fuzz-corpus.tsv`                |
| `crypto_published`               | MUST   | Met                      | The choices are published in `ASSURANCE_CASE.md` § 6: Go standard-library SHA-256 (FIPS 180-4) for artifact sealing, cosign/sigstore (keyless, GitHub OIDC) for release signatures. No algorithm is implemented here                                                |
| `crypto_call`                    | SHOULD | N/A                      | No cryptographic functionality is exposed. Hashing calls the standard library (`crypto/sha256`); nothing hand-rolls a cipher or mode                                                                                                                                |
| `crypto_floss`                   | MUST   | Met                      | Go standard library `crypto/sha256` (BSD-3-Clause); cosign (Apache-2.0) is a separate, documented external tool                                                                                                                                                     |
| `crypto_keylength`               | MUST   | Met                      | SHA-256 (256-bit) is the only digest used for artifact sealing — `internal/core/run/resume.go`, `internal/controls/controls.go`                                                                                                                                     |
| `crypto_working`                 | MUST   | Met                      | SHA-256 only; no broken algorithm ships                                                                                                                                                                                                                             |
| `crypto_weaknesses`              | SHOULD | Met                      | No MD5, no CBC. One SHA-1 use is a wire-compatible finding identifier, not a security boundary: `internal/sig/sig.go` (`//nolint:gosec`)                                                                                                                            |
| `crypto_pfs`                     | SHOULD | N/A                      | The project terminates no TLS session. HTTPS is performed by the operator's `gh`/`glab`/`curl`, which own their own cipher policy                                                                                                                                   |
| `crypto_password_storage`        | MUST   | N/A                      | Verdict stores no passwords and no credentials. Tokens come from the environment and go only to their own host (`SECURITY.md`)                                                                                                                                      |
| `crypto_random`                  | MUST   | Met                      | `crypto/rand` for tokens and identifiers — `internal/serve/auth.go`, `internal/atomic/atomic.go`                                                                                                                                                                    |
| `delivery_mitm`                  | MUST   | Met                      | HTTPS-only downloads in `install.sh`/`install.ps1`; archive SHA-256 verified always; cosign bundle verified when present                                                                                                                                            |
| `delivery_unsigned`              | MUST   | Met                      | Release `checksums.txt` is signed (cosign keyless) and graded by `.github/workflows/release.yml` § "Verify the published checksums signature"                                                                                                                       |
| `vulnerabilities_fixed_60_days`  | MUST   | Operator action required | No public vulnerability report has been filed to date. `SECURITY.md` commits to an acknowledgement and fix/disclosure timeline; the maintainer confirms there is no open report in the form                                                                         |
| `vulnerabilities_critical_fixed` | SHOULD | Operator action required | Same as above                                                                                                                                                                                                                                                       |
| `no_leaked_credentials`          | MUST   | Met                      | No credential file is committed; `.env.example` carries empty keys only. The built-in secret floor is `internal/exclude/secrets.go` with `TestSecretExclusionsSurviveEveryOverride`; `package.json` `prepublishOnly` fails a tarball containing secret-shaped paths |

## Analysis

| Criterion                                | Level     | Status                   | Evidence                                                                                                                                                                                                                                                                             |
| ---------------------------------------- | --------- | ------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `static_analysis`                        | MUST      | Met                      | `golangci-lint` (pinned v2.15.0, `.golangci.yml`, `gosec` + `staticcheck` + `govet`), `go vet`, `govulncheck` via `make vuln`, `shellcheck` — `.github/workflows/ci.yml`                                                                                                             |
| `static_analysis_common_vulnerabilities` | SUGGESTED | Met                      | `gosec` in `.golangci.yml`; `govulncheck` against the Go vulnerability database (`Makefile` § `vuln`, part of `ci-go`)                                                                                                                                                               |
| `static_analysis_fixed`                  | MUST      | Operator action required | Remediation history, attested by the maintainer in the form. Supporting evidence: the merge bar requires zero linter issues and `nolintlint` gates every `//nolint` with a reason (`CONTRIBUTING.md` § The merge bar), so nothing reachable ships unfixed                            |
| `static_analysis_often`                  | SUGGESTED | Met                      | Every push to `main` and every pull request; `hooks/pre-commit` and `hooks/pre-push` add the same checks locally                                                                                                                                                                     |
| `dynamic_analysis`                       | SUGGESTED | Met                      | Nightly fuzzing over 19 targets (`FUZZ_TARGETS` in `Makefile`, job `fuzz`) and the race detector in `make test-race`                                                                                                                                                                 |
| `dynamic_analysis_unsafe`                | SUGGESTED | N/A                      | Go is memory-safe, so neither valgrind nor ASan applies. The shipped analogue — `-race` and `-shuffle` — runs in the required `go` lane                                                                                                                                              |
| `dynamic_analysis_enable_assertions`     | SUGGESTED | N/A                      | Go has no assertion mechanism. Invariants are enforced by typed refusals and by tests that compare against literals (`AGENTS.md` § Tests)                                                                                                                                            |
| `dynamic_analysis_fixed`                 | MUST      | Operator action required | Remediation history of anything the fuzz or race lane surfaced, attested by the maintainer. Supporting evidence: fuzz findings become pinned regressions in `evals/fixtures/verify-fuzz-corpus.tsv`, and `internal/archtest` fails when a fuzz target is missing from `FUZZ_TARGETS` |

## What this audit deliberately does not claim

- **No badge, no project ID, no badge URL.** Registering is the maintainer step
  above; until it runs, this page is an audit and `README.md` carries a link
  here rather than a badge image.
- **No Silver or Gold criteria.** Those add `CODE_OF_CONDUCT.md`, documented
  governance, `roles_responsibilities`, `bus_factor`, `documentation_roadmap`,
  and `documentation_security`, which do not exist in this checkout as distinct
  documents. Recording them honestly here is cheaper than a badge application
  that fails on them.
- **No third-party attestation.** Every row is self-assessed against this
  repository's own artifacts. The independent signals are
  `.github/workflows/scorecard.yml` (OpenSSF Scorecard) and the Dependabot and
  `govulncheck` lanes, all of which publish their own results.
