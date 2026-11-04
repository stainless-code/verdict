# Go rules

Adopted from the Go knowledge base. One rule per line, each with
its `GO-<TOPIC>-<NN>` KB ID: the ID is the stable handle, the wording is
not. Combined IDs (`GO-CONC-02/04`) are the draft's shorthand for sibling
cards: every ID is present, none is merged or dropped. Every card's full text
(Rule / Why / Enforce / Since / Sources) lives in
[`docs/go-kb/knowledge-base.md`](go-kb/knowledge-base.md), the corpus the installed
`go-write`, `go-test`, `go-review`, and `go-cli-hardening` skills are built from. Each
card's `Enforce` field names the gate that holds it, and the markers below
summarise that per rule.

**Markers.** `— enforced:` means a machine gate fails on violation today.
`— partly:` means the named gate covers part of the rule. `— review-only:`
means a human reviewer is the only gate. The markers were checked against
this repo, and the repo files stay the truth for what is gated:
`.golangci.yml`, `Makefile`, `.github/workflows/ci.yml`.

## MUST (block merge on violation)

- Return `error` (static type) as the last value; never concrete error types (GO-ERRORS-01). — partly: `lint:errcheck` (unhandled returns); the static type is review-only.
- `%w` only with a documented/tested inspection contract; `%v` seals boundaries (GO-ERRORS-03). — enforced: `lint:errorlint,wrapcheck` (sealed constructors are exempt through `ignore-sigs`).
- Match errors with `errors.Is`/`As`, never `==` or message text (GO-ERRORS-04). — enforced: `lint:errorlint` (`comparison`, `asserts`).
- Handle each error once: wrap up, log at the edge; advise only at the command layer (GO-ERRORS-06). — partly: `lint:errcheck,nilerr,nilnesserr`.
- `ctx` is the first param; never store it in structs (GO-CONTEXT-01). — enforced: `lint:containedctx,contextcheck` + `revive:context-as-argument`.
- On `ctx.Done()`, stop and return `ctx.Err()` (GO-CONTEXT-02). — review-only.
- Context keys are unexported types; pass loggers explicitly (GO-CONTEXT-03/04). — review-only (analyzer candidate, map §2).
- Every goroutine has an exit path reviewable from its spawn site (GO-CONC-01). — review-only.
- Channels move ownership; mutexes guard state; maps are never shared unsynchronized (GO-CONC-03). — partly: `test -race`, `vet:copylock`.
- `WaitGroup.Add` before `go`, same frame (GO-CONC-05). — enforced: `vet:waitgroup` (runs under `go test`).
- Set `Cmd.WaitDelay` on every command with captured pipes (GO-CONC-06). — review-only (analyzer candidate, map §2).
- `go test -race -shuffle=on -count=1 ./...` is green on 3 OSes (GO-CONC-07). — partly: `make test-race` runs `-race -shuffle=on -count=1` on macOS + Ubuntu; Windows runs plain `go test`, so both flags are gated on two of the three OSes.
- No `*io.Reader`; comma-ok every type assertion (GO-IFACE-04). — partly: `lint:forcetypeassert`.
- Copy collections at trust boundaries (GO-COLLECT-02). — review-only.
- `defer` cleanup at the acquisition site (GO-DEFER-01). — partly: `lint:bodyclose,sqlclosecheck,rowserrcheck`.
- Never panic for ordinary errors; `recover` only at API edges with re-panic (GO-DEFER-03). — review-only (analyzer candidate, map §2).
- `slog` to stderr only; stdout is machine-parseable data (GO-LOG-01). — partly: `lint:sloglint` (`no-global`) + `lint:forbidigo` (no `fmt.Print*`). This repo emits frozen `warn:`/`error:` lines instead of `slog` records, for byte stability (`internal/cli/cli.go`).
- Never `Printf` a dynamic string (GO-LOG-03). — enforced: `vet:printf`, `lint:goprintffuncname,perfsprint`.
- Secrets never reach logs; redact via `LogValue` + scrub helpers (GO-LOG-04). — partly: the recorded verify tail passes `redactSecrets` (`internal/core/verify/service.go`); review-only elsewhere.
- Exact `Test/Fuzz/Benchmark/Example` signatures (GO-TEST-07). — enforced: `vet:tests` (runs under `go test`).
- Honest `go 1.N` floor; `vet:stdversion` clean (GO-API-04). — enforced: `vet:stdversion` (enabled in `.golangci.yml`).
- `main` is thin: parse, `run()`, exit once; only `main` calls `os.Exit` (GO-LAYOUT-02). — enforced: `lint:forbidigo` (`os.Exit` banned; `main` carries a scoped `//nolint:forbidigo`).
- `govulncheck ./...` blocks on reachable vulns (GO-MOD-04). — enforced: `make vuln`, part of `make ci-go` in the CI go job.
- `--json` prints exactly one envelope to stdout; everything else to stderr (GO-CLI-01). — partly: `evals` (`terminal-write`, `envelope-shape`) + `lint:forbidigo`; elsewhere review-only.
- No prompt without a TTY; `--yes`-or-fail otherwise (GO-CLI-03). — partly: `test` (`internal/core/init` machine-mode refusal, `--yes`).
- Root ctx from `signal.NotifyContext`; cancel kills children, exits 3 + JSON envelope (GO-CLI-05). — partly: `cmd/verdict/main.go` installs the root ctx and the exec watchdog group-kills on cancel; SIGINT and SIGTERM are pinned end to end by `TestShutdownSignalsCancelTheVerb` and, for the stdio transport, by `TestMCPShutdownSignalKillsTheToolTree`, and the shared renderer's cancel-to-3 mapping, including a cancel inside a read verb's service, by `TestReadVerbCancelInsideServiceExitsBroke` and `TestRefuseMapsCancelToBroke`.
- Kill process trees (pgid/Job Objects), never just the direct child (GO-EXEC-01). — partly: `internal/core/exec` (`setGroup`/`killGroup`, Windows Job objects) with tests.
- `exec.CommandContext` + `WaitDelay`; timeouts read from `ctx.Err()`, not `err` (GO-EXEC-02). — partly: `internal/core/exec` sets `WaitDelay` on the watchdog; review-only elsewhere.
- No shell for agent-supplied commands; argv only (GO-EXEC-03). — partly: every command verdict issues is argv. The one exception is by design: `verify` runs the agent's own `verifyCmd` through `bash -c`. Analyzer candidate, map §2.
- Scrub child env (allowlist); cap output ~1 MiB/stream; `ExitCode()` for status (GO-EXEC-04/05). — partly: capture caps and `ExitCode` in `internal/core/exec` (`VerifyOutputCap` 1 MiB); `internal/testenv` scrubs test children. `gh`/`git` inherit on purpose — see `SECURITY.md` § Child process environment.
- Strict capped JSON entry (`DisallowUnknownFields`, `UseNumber`, size caps) (GO-JSON-01). — partly: `test` reject-cases; analyzer candidate, map §2.
- Preserve unknown store fields via `RawMessage` maps, never struct round-trips (GO-JSON-02). — partly: byte-exact store goldens in `internal/record`.
- No `encoding/json/v2` or `jsontext` imports until stable (GO-JSON-05). — enforced: `lint:depguard`.
- Stores: temp-file + fsync + rename + dir-fsync; never truncate in place (GO-ATOMIC-01). — partly: `internal/atomic` owns store writes (temp file + fsync + rename + dir fsync), exercised by tests.
- Secrets at `0600`; scratch via `MkdirTemp` + deferred cleanup (GO-FS-02). — partly: `lint:gosec` (permission literals) + `internal/testenv` writes its config `0600`.
- `filepath` for disk, `path` for URLs; never split separators by hand (GO-FS-04). — review-only (analyzer candidate, map §2).
- One hardened HTTP constructor; never `http.DefaultClient`/`Get`/`Post` (GO-HTTP-01). — enforced: `lint:forbidigo` (`http.Get/Post/PostForm/Head`, `http.DefaultClient/Transport`).
- Cap every response body (`LimitReader(N+1)`, abort past cap) (GO-HTTP-02). — partly: `internal/release` caps its redirect body; analyzer candidate, map §2.
- Retry idempotent methods only; honor `Retry-After`; jitter; ctx-cancellable (GO-HTTP-03). — partly: `internal/retry` (3 attempts, `Retry-After` honored, bounded backoff; no jitter).
- No network except configured code hosts; no telemetry (GO-OBS-03). — partly: `evals` put a network-dead `gh` stub first on `PATH` and tally zero real calls; review-only elsewhere.
- Stream hashes; constant-time secret comparison (GO-CRYPTO-01). — review-only (analyzer candidate, map §2).
- No SHA-1 outside the one wire-compat package (scoped lint exception, not-a-security-boundary doc, explicit FIPS stance); suite runs once under `GODEBUG=fips140=only` (GO-CRYPTO-02). — partly: `lint:depguard` bans `crypto/sha1` outside `internal/sig`; the FIPS-only run is not a CI step.
- Windows runs in CI, not just cross-compiles (GO-XPLAT-01). — enforced: `ci:go-windows`.
- `default: none`-equivalent explicit lint set; `gofumpt` + `gci` formatting (GO-LINT-01/02). — partly: `.golangci.yml` pins an explicit enable list over `default: standard` plus the two formatters; `make gofmt-check` and CI gate plain `gofmt`; `golangci-lint fmt` is not a CI step.
- CI: lint, vet, 3-OS test matrix, vuln, fuzz, build, tag-release (GO-CI-01/03). — partly: `ci.yml` runs format, lint, go (race + vet + vuln + golangci), go-windows, cross (every shipped `GOOS/GOARCH`, from `release/contract.json`), evals, docs, ts-client, audit, release dry-run — every lane except format/docs/audit on any diff that is not docs-only, and unfiltered on `main` pushes; `fuzz` is advisory and runs on the nightly schedule alone, and tagging lives in `release.yml`.
- Releases via goreleaser: signed SBOMs + cosign bundles (GO-CI-04). — enforced: `.goreleaser.yml` (`sboms`, keyless `signs`) + `ci:release-dry-run`.

## SHOULD (fix or record a reason)

- Short lowercase package names; `MixedCaps` + consistent initialisms (GO-NAMING-01/02). — partly: `revive:var-naming` + `gofumpt`.
- Minimal module layout; `internal/` by default; no `util` catch-alls (GO-LAYOUT-01/03/04). — partly: `internal/archtest` guards the package layering.
- One package per CLI noun; no `init` wiring; no mutable globals (GO-LAYOUT-05/06). — review-only.
- Lowercase error strings; `%w` last; `errors.Join` for independent failures (GO-ERRORS-02/05/07). — partly: `lint:revive` (default rules), `lint:errorlint`, `vet:printf`.
- Synchronous functions first; `errgroup` fan-out at the caller (GO-CONC-02/04). — review-only.
- Consumer-side minimal interfaces; concrete returns; compile-time compliance asserts (GO-IFACE-01/02/03). — review-only (the compiler is the gate for the asserts).
- Zero-value-ready types; `make` for refs; generics on the third copy, containers first (GO-TYPES-01/03). — review-only.
- Nil-over-empty slices; `slices`/`maps`/`cmp` over hand loops; sorted snapshot iteration (GO-COLLECT-01/03/04). — partly: `lint:modernize,usestdlibvars` + `forbidigo` (`sort.`, `reflect.DeepEqual`).
- Table tests with `ErrorIs`; `t.Helper` + `t.Cleanup`; goldens with `-update` (GO-TEST-01/02/04). — partly: `lint:testifylint,thelper,usetesting`; the `-update` flag lives in `internal/golden`.
- Fuzz parsers (`-fuzztime=30s` in CI); `B.Loop` + `benchstat` (GO-TEST-05/06). — partly: `make fuzz` runs all 11 targets for 30 s each in the nightly `fuzz` lane, and `internal/archtest` fails when a target in the tree is missing from `FUZZ_TARGETS`; `B.Loop` + `benchstat` stay review-only.
- `t.Context`/`t.Chdir`; testscript txtar for CLI e2e; build-tagged integration (GO-TEST-08). — partly: `lint:usetesting`; CLI e2e is the bash `evals` suite (this repo has no `testscript` dependency).
- Doc every export name-first; runnable Examples; document sharp edges (GO-DOCS-01/02). — partly: `lint:godoclint,godot,misspell`.
- Evolve by addition; tidy minimal modules; tool directives; GODEBUG in go.mod (GO-API-03, GO-MOD-02/03/05). — partly: `lint:gomoddirectives` + `make vet` (`go mod verify`); `go mod tidy -diff` is not a CI step.
- Measure before optimizing (`benchstat` + profiles) (GO-PERF-01/03). — review-only.
- A benchmark's input is built from a byte budget the product accepts, and the benchmark asserts it: `b.SetBytes` reports a size, it never proves one (GO-PERF-01). — review-only.
- `os.Root` confinement; `crypto/rand` secrets; validated edge input (GO-SEC-01/02/04). — partly: `lint:depguard` (`math/rand`), `lint:gosec`, `lint:nosprintfhostport`; `os.Root` is review-only, and operator-supplied bundle paths are exempt by decision.
- Exit-code contract; `--json/--jq` exporters; generated completions/manpages (GO-CLI-02/06/07). — partly: exit codes are frozen constants in `internal/cli` with tests; no generated completions or manpages.
- Start std-`flag` under a zero-dep policy; graduate to cobra when justified (GO-CLI-08). — partly: `go.mod` carries no third-party requirements; argv parsing is hand-rolled in `internal/cli` to keep the frozen contract byte-identical.
- `embed` prompts; stamped `--version` + `-trimpath` builds (GO-CLI-09). — partly: `skillfs.go` embeds `skills/verdict/` (`SKILL.md`, `prompts`, `schemas`, `scripts/verdict`); `make build` and goreleaser pass `-trimpath`.
- flags > env > file > defaults; report secret sources; lenient config reads (GO-CONFIG-01/02/03). — partly: `internal/cli` precedence + `internal/cli/flags_test.go`.
- Advisory `flock` across read-modify-write; JSON schema libs where beyond `DisallowUnknownFields` (GO-ATOMIC-02, GO-JSON-04); tag linters apply to new types only, legacy wire fields frozen (GO-JSON-03). — partly: `lint:musttag,tagalign`; the rest is review-only.
- Progress as stderr attrs + JSON error shapes; log after flag parsing (GO-OBS-01/02). — partly: envelope error-shape goldens.
- Paginate defensively; keep proxy support on (GO-HTTP-04). — review-only.
- Build-tagged OS splits; `AGENTS.md` + Makefile mirroring CI (GO-FS-03, GO-CI-06). — partly: `_unix`/`_windows` splits; `Makefile` mirrors `ci.yml` (`make ci-go`, `make evals`); `AGENTS.md` carries the ladder.

## CONSIDER (evaluate per project)

- Startup is a product surface: keep total package init under 1 ms, hoist a package-level `regexp.MustCompile` or any other parse behind `sync.OnceValue` when only one verb needs it, and paste `GODEBUG=inittrace=1 ./bin/verdict --version` in any PR that touches init (GO-PERF-04). — review-only.

## Review-only MUSTs worth a custom analyzer

No lint, vet, test, or CI gate covers these today; map §2 holds the
analyzer sketches, highest value first. The analyzer is separate work, not
part of adopting these rules.

- GO-CONTEXT-03 — unexported context key types (type-checkable; easiest win).
- GO-EXEC-03 — shell-arg matcher (`exec.Command("sh"|"bash", "-c", …)` outside an allowlisted wrapper).
- GO-SEC-01 — path-arg check on `os.Root` accessors.
- GO-HTTP-02 — bare `io.ReadAll(resp.Body)`.
- GO-CONC-06 — `CommandContext` without a `WaitDelay` assignment.

## Accepted deviations

Recorded here so a later reader does not "fix" them by accident. Each is a
decision with a reason, not an oversight.

- **GO-EXEC-03/04 (shell, env):** `verify` executes the agent's own command through `bash -c` with the environment snapshot the caller handed it, and the fixed `gh`/`git` argv inherits the developer's environment. Both are the product contract; see `SECURITY.md` § Child process environment.
- **GO-LOG-01 (slog):** the CLI writes frozen `warn:`/`error:` lines and one JSON envelope instead of `slog` records, because byte-identical stdout/stderr is the output contract.
- **GO-SEC-01 (os.Root):** bundle paths are operator-supplied (G304 excluded in `.golangci.yml`), not agent-supplied.
- **GO-CRYPTO-02 (SHA-1):** `sig_version "1"` identifiers stay on SHA-1, confined to `internal/sig` by a scoped `depguard` exception; FIPS-only is documented as unsupported rather than tested.
