# Go Production Knowledge Base (Go 1.27, 2026-11-02)

Merged, deduplicated synthesis of four research files:

- **A** — canonical guidance (Effective Go, Code Review Comments, Google + Uber style guides, release notes 1.21–1.27)
- **B** — OSS exemplars (16 maintained repos + linter-frequency table)
- **C** — validated tooling enforcement (golangci-lint v2.15 config, CI, goreleaser, Makefile)
- **D** — CLI domain patterns (44 cards, 30 experiment-verified, with runnable snippets)

Card ID: `GO-<TOPIC>-<NN>`. Each card keeps its source IDs (e.g. `from A-ERRORS-03, B-ERR-02`).
`Enforce` uses the A vocabulary: `gofmt` | `goimports` | `vet[:analyzer]` | `test` | `errcheck` |
`govulncheck` | `lint:<name>` | `ci` | `review-only`.
`Verified` means the underlying claim was executed green (D experiments, C sample runs);
`(source-only)` means it was read in code/docs but not executed here.

## Tiers

- **MUST** — correctness, security, or data-loss. Violations are bugs. Enforced by linter/vet/test/CI where possible; the remainder are review gates, carried by the review-only MUST cards whose `Enforce` field names no machine check.
- **SHOULD** — strong default. Deviate only with a reason recorded (comment, ADR, or nolint with explanation).
- **CONSIDER** — context-dependent. Evaluate per project; the card states when it applies.

## Topic index

| Prefix       | Topic                               | Cards                                               |
| ------------ | ----------------------------------- | --------------------------------------------------- |
| `GO-NAMING`  | Naming                              | 6                                                   |
| `GO-LAYOUT`  | Package design and layout           | 6                                                   |
| `GO-ERRORS`  | Errors                              | 8                                                   |
| `GO-CONTEXT` | Context and cancellation            | 4                                                   |
| `GO-CONC`    | Concurrency                         | 7                                                   |
| `GO-IFACE`   | Interfaces                          | 4                                                   |
| `GO-TYPES`   | Zero values, constructors, generics | 5                                                   |
| `GO-COLLECT` | Slices, maps, iteration             | 4                                                   |
| `GO-DEFER`   | Defer, panic, recover               | 3                                                   |
| `GO-LOG`     | Logging                             | 4                                                   |
| `GO-TEST`    | Testing                             | 8                                                   |
| `GO-DOCS`    | Docs and godoc                      | 2                                                   |
| `GO-API`     | API stability and versioning        | 4                                                   |
| `GO-MOD`     | Modules and dependencies            | 5                                                   |
| `GO-PERF`    | Performance                         | 3                                                   |
| `GO-SEC`     | Security basics                     | 5                                                   |
| `GO-CLI`     | CLI structure and UX                | 9                                                   |
| `GO-CONFIG`  | Config and env precedence           | 3                                                   |
| `GO-EXEC`    | Subprocess execution                | 6                                                   |
| `GO-JSON`    | JSON handling                       | 5                                                   |
| `GO-ATOMIC`  | Atomic writes and locking           | 3                                                   |
| `GO-FS`      | Filesystem security                 | 4                                                   |
| `GO-HTTP`    | HTTP client hygiene                 | 4                                                   |
| `GO-OBS`     | Observability without telemetry     | 3                                                   |
| `GO-CRYPTO`  | Hashing and crypto                  | 2                                                   |
| `GO-XPLAT`   | Cross-platform                      | 3                                                   |
| `GO-LINT`    | Lint and format config              | 3                                                   |
| `GO-CI`      | CI and release                      | 7                                                   |
| `GO-GOV`     | Contributor docs and governance     | none — governance lives in `GO-CI-07` and `GO-DOCS` |

Total: **130 cards** — MUST: 61, SHOULD: 61, CONSIDER: 8.

---

## Naming

### GO-NAMING-01 — **SHOULD** — Keep package names short, lowercase, single-word

- from A-NAMING-01, B-LAYOUT-02
- Rule: One short lowercase word, no underscores or MixedCaps (`bufio`, `strconv`); avoid repetition (`http.Server`, not `http.HTTPServer`).
- Why: The package name prefixes every use; long or repeated names tax every call site and invite import renames.
- Enforce: review-only
- Sources: https://go.dev/doc/effective_go, https://go.dev/blog/package-names

### GO-NAMING-02 — **SHOULD** — Use MixedCaps, capitalize initialisms consistently

- from A-NAMING-03, A-NAMING-04
- Rule: `maxLength`, `ServeHTTP`, `appID` — never `max_length`, `MAX_PACKET_SIZE`, `ServeHttp`, `appId`.
- Why: Mixed-case initialisms fork vocabulary; every reader must guess which spelling you used.
- Enforce: gofmt (mechanics); review-only (word choice)
- Sources: https://go.dev/wiki/CodeReviewComments, https://google.github.io/styleguide/go/decisions

### GO-NAMING-03 — **SHOULD** — Omit Get from getters; name cost with verbs

- from A-NAMING-05
- Rule: Accessor is `Owner()`, not `GetOwner()`; use `Fetch`/`Compute` when the call blocks, computes, or can fail.
- Why: `Get` adds no information, and a bare noun hides expensive work; the name should signal whether the call is free.
- Enforce: review-only
- Sources: https://go.dev/doc/effective_go, https://google.github.io/styleguide/go/decisions

### GO-NAMING-04 — **SHOULD** — Keep receivers short, uniform, type-derived

- from A-NAMING-06
- Rule: One or two letters abbreviating the type, identical on every method; never `this`/`self`/`me`.
- Why: Receivers appear on nearly every line; long or shifting names add noise and fake OO semantics.
- Enforce: review-only (+ `lint:revive` receiver-naming)
- Sources: https://go.dev/wiki/CodeReviewComments, https://github.com/uber-go/guide

### GO-NAMING-05 — **SHOULD** — Scale variable names to scope, never to type

- from A-NAMING-07
- Rule: Short names (`i`, `db`, `err`) in small scopes, descriptive names at file/package scope; never embed the type (`numUsers`, `nameString`).
- Why: Over-qualified locals bury logic; under-qualified globals force readers to re-derive meaning far from declaration.
- Enforce: review-only
- Sources: https://go.dev/wiki/CodeReviewComments

### GO-NAMING-06 — **MUST** — Never shadow predeclared identifiers or common package names

- from A-NAMING-08
- Rule: Do not name variables `error`, `string`, `len`, `url`, `context`; rename the import instead of shadowing.
- Why: Shadowing silently rebinds builtins and package references, producing bugs that compile cleanly and grep badly.
- Enforce: `lint:predeclared,import-shadowing` + `vet`
- Sources: https://google.github.io/styleguide/go/best-practices, https://github.com/uber-go/guide

---

## Package design and layout

### GO-LAYOUT-01 — **SHOULD** — Start from the official minimal module layout

- from A-PACKAGE-01, A-PACKAGE-08, B-LAYOUT-02
- Rule: Package files in the repo root, commands under `cmd/<name>/main.go`, shared code under `internal/`. Treat `golang-standards/project-layout` (`pkg/`, `api/`, `web/`) as opt-in, not required.
- Why: The official layout is the only one every Go tool understands; scaffolding up front slows builds and confuses imports.
- Enforce: review-only
- Sources: https://go.dev/doc/modules/layout
- Example: `cmd/<bin>/main.go` wiring-only in gh, lazygit, caddy, hugo (B-LAYOUT-01).

### GO-LAYOUT-02 — **MUST** — Keep main thin: parse, delegate to run(), exit once

- from A-PACKAGE-04, B-LAYOUT-01
- Rule: `main` calls one `run()` returning an error/exit code; only `main` calls `os.Exit`; use `defer` so cleanup runs.
- Why: Exit-anywhere control flow is untestable and skips deferred cleanup; a single exit point keeps behavior explicit.
- Enforce: `lint:forbidigo` (os.Exit ban outside main) + review-only
- Sources: https://github.com/uber-go/guide
- Example:

```go
func main() {
	if err := run(); err != nil {
		os.Exit(1)
	}
}
```

### GO-LAYOUT-03 — **SHOULD** — Hide non-exported code behind internal/

- from A-PACKAGE-02, B-LAYOUT-02
- Rule: Implementation packages live in `internal/` (nestable); only expose what other modules may depend on; one module per repo unless components version independently (then multi-module + `replace`, per etcd).
- Why: `internal/` is compiler-enforced, so refactors stay safe; without it every helper is a de-facto public API.
- Enforce: review-only
- Sources: https://go.dev/doc/modules/layout

### GO-LAYOUT-04 — **SHOULD** — Never use util/common/misc catch-all packages

- from A-PACKAGE-03
- Rule: Split generic helpers into packages named for what they provide (`stringset`, not `util`); never centralize interfaces in `api`/`types`.
- Why: Catch-all packages accumulate unrelated dependencies, slow compilation, and tell users nothing.
- Enforce: review-only
- Sources: https://go.dev/blog/package-names, https://google.github.io/styleguide/go/best-practices

### GO-LAYOUT-05 — **SHOULD** — Group CLI commands as one package per noun

- from B-LAYOUT-03
- Rule: `pkg/cmd/<noun>/<verb>.go` (or per-stage for pipelines), one directory per resource.
- Why: Scales to 50+ subcommands without merge conflicts; matches how users think (`gh pr create`).
- Enforce: review-only
- Sources: cli/cli `pkg/cmd` tree at `9490d20`.

### GO-LAYOUT-06 — **SHOULD** — Avoid init() for wiring, I/O, and environment reads; avoid mutable globals

- from A-PACKAGE-05, A-PACKAGE-06
- Rule: Prefer explicit constructors called from `main`; `init()` must be deterministic, order-independent, side-effect free (registrations are the narrow exception). Pass clocks/config/clients as struct fields, not mutated package `var`s.
- Why: `init()` runs implicitly with hidden ordering; mutable globals serialize tests and turn every test into an ordering hazard.
- Enforce: review-only
- Sources: https://github.com/uber-go/guide

---

## Errors

### GO-ERRORS-01 — **MUST** — Return error as the last value with the error type

- from A-ERRORS-01
- Rule: Signal failure with a final `error` return of static type `error`, never a concrete `*os.PathError`-style type (nil pointer becomes non-nil interface). Non-error results are unspecified when err != nil.
- Why: Concrete error types in interfaces turn nil pointers into non-nil errors and freeze your implementation in the signature.
- Enforce: `errcheck` (unchecked returns); `vet`
- Sources: https://go.dev/wiki/CodeReviewComments, https://google.github.io/styleguide/go/decisions, https://go.dev/doc/faq

### GO-ERRORS-02 — **SHOULD** — Write lowercase, unpunctuated error strings

- from A-ERRORS-02
- Rule: `fmt.Errorf("something bad")`, not `"Something bad."`; reserve capitals for proper nouns and exported names.
- Why: Errors print inside larger messages; mid-sentence capitals and trailing periods corrupt every embedding log line.
- Enforce: `lint:revive` (error-strings), `lint:errorlint`
- Sources: https://go.dev/wiki/CodeReviewComments

### GO-ERRORS-03 — **MUST** — Choose %w vs %v deliberately; wrap only with a documented contract

- from A-ERRORS-03, A-API-05, B-ERR-01
- Rule: `%w` only when callers should programmatically inspect the cause AND you document/test that contract; `%v` to annotate or seal a boundary (RPC/storage). If you wrap `sql.ErrNoRows`-style sentinels, that is an API commitment.
- Why: Wrapping publishes the inner error as API — callers will `errors.Is` on it forever; accidental `%w` leaks implementation details across modules. (Resolves the A/B tension: B's "wrap at every layer" applies to CLIs adding context; A's deliberateness governs what crosses a package API.)
- Enforce: `lint:errorlint,wrapcheck` + review-only
- Sources: https://go.dev/blog/go1.13-errors, https://google.github.io/styleguide/go/best-practices

### GO-ERRORS-04 — **MUST** — Match with errors.Is/As, never == or string content

- from A-ERRORS-04, B-ERR-02
- Rule: Sentinels via `errors.Is`, types via `errors.As`; never `err.Error() == "not found"`. Package-level `var ErrX = errors.New(...)` for matchable conditions; structs carrying exit codes/details when callers need data.
- Why: Wrapping changes identity and text but preserves the chain; string matching breaks on every reword.
- Enforce: `lint:errorlint` (comparison, asserts) + review-only
- Sources: https://go.dev/blog/go1.13-errors
- Example:

```go
if errors.Is(err, ErrNotFound) { /* ... */ }
var ee *ExitError
if errors.As(err, &ee) { os.Exit(ee.Code) }
```

### GO-ERRORS-05 — **SHOULD** — Place %w last; lead with sentinels only for categorization

- from A-ERRORS-05
- Rule: `fmt.Errorf("load certificate: %w", err)`; leading `"%w: ..."` only when the sentinel is the primary category.
- Why: Trailing `%w` keeps printed text in newest-to-oldest chain order.
- Enforce: `vet` (printf analyzer flags suspicious `%w`, Go 1.27)
- Since: Go 1.27
- Sources: https://google.github.io/styleguide/go/best-practices, https://go.dev/doc/go1.27

### GO-ERRORS-06 — **MUST** — Handle each error once: wrap up, log at the edge

- from A-ERRORS-06, B-ERR-03, B-ERR-04
- Rule: Either handle (degrade/retry) or wrap-and-return — never `log.Printf` and return the same error. Keep wrap messages terse. Advisory failures (config/telemetry/plugin loads) degrade to stderr warnings, not fatal errors. Low-level packages return wrapped errors; only the command layer renders human advice.
- Why: Log-and-return duplicates every failure up the stack into logspam; a corrupt cosmetic config must never block the main command.
- Enforce: `errcheck`; `lint:nilerr,nilnesserr` + review-only
- Sources: https://github.com/uber-go/guide, https://google.github.io/styleguide/go/best-practices

### GO-ERRORS-07 — **SHOULD** — Aggregate independent failures with errors.Join

- from A-ERRORS-07
- Rule: Combine multiple goroutine/task errors with `errors.Join`; inspect via `errors.Is`/`As`, which traverse the tree.
- Why: Returning only the first error discards evidence; concatenated strings destroy programmability.
- Enforce: review-only
- Since: Go 1.20
- Sources: https://go.dev/blog/go1.13-errors

### GO-ERRORS-08 — **SHOULD** — Reject in-band errors; return (value, ok) or error

- from A-ERRORS-08
- Rule: Return explicit `error` or `ok bool` as the final value instead of `-1`/`""`/`nil`-means-failure encodings.
- Why: In-band sentinels compile at misuse sites and misattribute the failure to the wrong function.
- Enforce: review-only
- Sources: https://go.dev/wiki/CodeReviewComments

---

## Context and cancellation

### GO-CONTEXT-01 — **MUST** — Take ctx as the first parameter; never store it in structs

- from A-CONTEXT-01
- Rule: `func F(ctx context.Context, ...)` for anything request-scoped; do not add `Ctx` fields to structs (`http.Request` is the back-compat exception).
- Why: Stored contexts fuse unrelated lifetimes and rob callers of per-call deadlines.
- Enforce: `lint:containedctx,contextcheck,noctx` + review-only
- Sources: https://go.dev/wiki/CodeReviewComments, https://go.dev/blog/context-and-structs

### GO-CONTEXT-02 — **MUST** — Propagate cancellation; return ctx.Err() when it fires

- from A-CONTEXT-02
- Rule: Select on `ctx.Done()` in blocking loops and return the context error; document any deviation.
- Why: Ignored cancellation turns one slow dependency into resource exhaustion across the whole server.
- Enforce: review-only
- Sources: https://go.dev/blog/context-and-structs
- Example:

```go
select {
case <-ctx.Done():
	return ctx.Err()
case v := <-ch:
	return use(v)
}
```

### GO-CONTEXT-03 — **MUST** — Carry request-scoped values with unexported keys only

- from A-CONTEXT-03
- Rule: Keys must be unexported types; never strings or exported keys; prefer explicit parameters for application data.
- Why: String keys collide across packages and leak; explicit parameters keep data flow visible.
- Enforce: review-only
- Sources: https://pkg.go.dev/context

### GO-CONTEXT-04 — **SHOULD** — Do not smuggle loggers through context

- from A-CONTEXT-04
- Rule: Pass `*slog.Logger` explicitly; there is no stdlib logger-in-context API by design.
- Why: Implicit dependencies hide data flow and make call sites lie about what a function needs.
- Enforce: review-only (+ `lint:sloglint` no-global)
- Sources: https://go.dev/blog/slog

---

## Concurrency

### GO-CONC-01 — **MUST** — Give every goroutine an obvious exit path

- from A-CONCURRENCY-01
- Rule: Document when and why each goroutine stops; block-or-exit must be reviewable from the spawn site; probe with `goleak`-style tests and the `goroutineleak` pprof profile.
- Why: Blocked senders/receivers are invisible to the GC; leaked goroutines pin memory and panic later on closed channels.
- Enforce: `test` (goleak-style); review-only
- Since: Go 1.27 (`goroutineleak` profile generally available)
- Sources: https://go.dev/wiki/CodeReviewComments, https://go.dev/doc/go1.27

### GO-CONC-02 — **SHOULD** — Prefer synchronous functions; add concurrency at the caller

- from A-CONCURRENCY-02
- Rule: Return results directly instead of callback/channel hand-offs; let callers wrap in `go` + `errgroup` when they need parallelism.
- Why: Async APIs scatter lifetimes across call frames, defeating reasoning, tests, and leak detection.
- Enforce: review-only
- Sources: https://go.dev/wiki/CodeReviewComments

### GO-CONC-03 — **MUST** — Prefer channels for ownership transfer, mutexes for state

- from A-CONCURRENCY-03, A-SLICEMAP-03
- Rule: Channels pass ownership between goroutines; `sync.Mutex`/`RWMutex` (zero value, unembedded, pointer receiver) guard shared state. Never assume maps are goroutine-safe — guard with a mutex or use `sync.Map` for mostly-read caches. Keep buffered channels at size 0 or 1 without a written justification.
- Why: Large buffers hide backpressure; a single concurrent map write/read is a fatal error, not a race warning.
- Enforce: `vet:copylock`; `test` with `-race`
- Sources: https://go.dev/wiki/CodeReviewComments, https://go.dev/doc/faq

### GO-CONC-04 — **SHOULD** — Coordinate fan-out with errgroup, not hand-rolled WaitGroups

- from A-CONCURRENCY-04
- Rule: Use `golang.org/x/sync/errgroup` (with `SetLimit`) for groups of fallible goroutines sharing a context; check `errors.Is(group.Wait(), context.Canceled)` at join.
- Why: Hand-rolled first-error-wins logic drops errors and leaks stragglers; errgroup propagates cancellation to the whole group.
- Enforce: review-only
- Sources: https://google.github.io/styleguide/go/best-practices
- Example:

```go
g, ctx := errgroup.WithContext(ctx)
for _, u := range urls {
	g.Go(func() error { return fetch(ctx, u) })
}
return g.Wait()
```

### GO-CONC-05 — **MUST** — Call sync.WaitGroup.Add before go, in the same frame

- from A-CONCURRENCY-05
- Rule: `Add(n)` must precede the `go` statement it counts (same lexical scope); never `Add` inside the goroutine.
- Why: Late `Add` races with `Wait`'s counter hitting zero, ending the wait early or panicking on a negative counter.
- Enforce: `vet:waitgroupgo`; `test` with `-race`
- Since: Go 1.25 (analyzer); renamed `waitgroupgo` in Go 1.27
- Sources: https://go.dev/doc/go1.25, https://go.dev/doc/go1.27

### GO-CONC-06 — **MUST** — Bound os/exec waits with WaitDelay

- from A-CONCURRENCY-06, D-EXEC-02
- Rule: Set `Cmd.WaitDelay` (e.g. 2–5 s) on every command with captured output pipes. Verified: without it, a SIGKILLED child with a leaked grandchild holding the pipe blocked `Run` for 30 s; with group-kill + `WaitDelay`, return was 502 ms.
- Why: Without it, one wedged child pins `Wait` (and its goroutines) indefinitely after the process itself exited.
- Enforce: review-only (+ grep for `CommandContext` without `WaitDelay`)
- Since: Go 1.20
- Verified: yes (exp-subproc B vs C)
- Sources: https://pkg.go.dev/os/exec

### GO-CONC-07 — **MUST** — Test concurrent code with -race and testing/synctest

- from A-CONCURRENCY-07, B-TEST-05, C-TEST-01, C-TEST-06
- Rule: `go test -race -shuffle=on -count=1` in CI on 3 OSes; write timing-sensitive tests with `synctest.Test`/`synctest.Wait` instead of `time.Sleep` guesses; fuzz every hand-written parser.
- Why: Races are schedule-dependent; sleeps make tests flaky and slow, while the virtual clock makes interleavings deterministic.
- Enforce: `test` (`-race`, `testing/synctest`); `ci`
- Since: Go 1.25 (`testing/synctest` stable); `synctest.Sleep` added Go 1.27
- Sources: https://go.dev/doc/go1.25, https://go.dev/doc/go1.27
- Verified: yes (synctest PASS in 0.00 s; 15 s fuzz smoke 3M execs PASS)

---

## Interfaces

### GO-IFACE-01 — **SHOULD** — Define interfaces at the consumer, keep them minimal

- from A-INTERFACES-01
- Rule: Declare the narrow interface where you use it (one or two methods); do not pre-create interfaces "for mocking" on the producer side.
- Why: Producer-side abstractions guess wrong about needed methods; consumer interfaces stay exactly as wide as use.
- Enforce: review-only
- Sources: https://go.dev/wiki/CodeReviewComments, https://google.github.io/styleguide/go/best-practices

### GO-IFACE-02 — **SHOULD** — Accept interfaces, return concrete structs

- from A-INTERFACES-02
- Rule: Parameters take interfaces; constructors return concrete types, with `error` and multi-implementation factories as the deliberate exceptions.
- Why: Concrete returns keep the full API available and let each caller mock differently; interface returns lock all callers into your abstraction.
- Enforce: review-only
- Sources: https://go.dev/wiki/CodeReviewComments

### GO-IFACE-03 — **SHOULD** — Assert compliance at compile time with a var block

- from A-INTERFACES-03
- Rule: `var _ http.Handler = (*Handler)(nil)` for every exported type-to-interface contract.
- Why: Silent drift breaks users at upgrade time; the assertion fails the build the moment the method set stops matching.
- Enforce: `test` (compilation itself)
- Sources: https://github.com/uber-go/guide

### GO-IFACE-04 — **MUST** — Never use pointers to interfaces; handle type assertions with comma-ok

- from A-INTERFACES-04, A-INTERFACES-05
- Rule: Pass `io.Reader`, not `*io.Reader`. Use `v, ok := i.(string)` plus graceful handling; reserve single-value asserts for provably-total cases.
- Why: A pointer to an interface adds indirection with no benefit; a wrong dynamic type panics the whole goroutine.
- Enforce: `lint:forcetypeassert` + review-only
- Sources: https://github.com/uber-go/guide, https://go.dev/doc/faq

---

## Zero values, constructors, generics

### GO-TYPES-01 — **SHOULD** — Design types so the zero value is immediately useful

- from A-ZERO-01, A-ZERO-02, A-ZERO-03
- Rule: Make `var buf bytes.Buffer` and `var mu sync.Mutex` work with no constructor; document that property. Use `make` for slices/maps/channels, `new` only for plain values (`new([]int)` yields a pointer to nil and is almost always wrong). Name constructors `New`/`NewX` and return ready values so callers never need a second `Init`.
- Why: Zero-value readiness composes transitively through structs and removes an entire class of init-ordering bugs.
- Enforce: review-only
- Sources: https://go.dev/doc/effective_go
- Example: `q := list.New()` — `q` is a ready `*list.List`.

### GO-TYPES-02 — **SHOULD** — Use new(expr) for optional pointer fields

- from A-ZERO-04
- Rule: Prefer `new(yearsSince(t))` over a temp variable when populating `*int` JSON/proto optional fields inline.
- Why: Eliminates throwaway locals at composite-literal sites while keeping the pointer semantics serialization needs.
- Enforce: review-only
- Since: Go 1.26
- Sources: https://go.dev/doc/go1.26

### GO-TYPES-03 — **SHOULD** — Write concrete code first; generalize on the third copy

- from A-GENERICS-01, A-GENERICS-02
- Rule: Start with plain functions; introduce type parameters only when identical logic repeats across types. The canonical wins are language containers (slices/maps/channels) and data structures (trees, sets).
- Why: Premature constraints ossify APIs around the wrong abstraction; concrete code reveals the true common shape.
- Enforce: review-only
- Sources: https://go.dev/blog/when-generics

### GO-TYPES-04 — **SHOULD** — Prefer function parameters over method constraints; never replace method-dispatch interfaces with type parameters

- from A-GENERICS-03, A-GENERICS-04
- Rule: Keep `io.Reader` as an interface — do not rewrite `ReadSome(r io.Reader)` as `ReadSome[T io.Reader](r T)`. Take `cmp func(T, T) int` as an argument rather than constraining `T` to a `Compare` method.
- Why: Same-shape implementations are the exception; callers can pass method expressions or lambdas, while method constraints force wrapper types on everyone.
- Enforce: review-only
- Sources: https://go.dev/blog/when-generics

### GO-TYPES-05 — **CONSIDER** — Reach for reflection when types differ structurally

- from A-GENERICS-05
- Rule: Use `reflect` (as `encoding/json` does) when operations vary per type and methods cannot be required — not generics, not `any`-switches.
- Why: Generics require uniform code; heterogeneous per-type behavior needs runtime dispatch, centralized with tests.
- Enforce: review-only
- Sources: https://go.dev/blog/when-generics

---

## Slices, maps, iteration

### GO-COLLECT-01 — **SHOULD** — Declare empty slices as nil; test emptiness with len

- from A-SLICEMAP-01
- Rule: `var t []string`, never `t := []string{}`; check `len(s) == 0`, never `s == nil` — except JSON APIs where null vs `[]` matters.
- Why: Nil and empty behave identically for `len`/`cap`/`range`/`append`, but APIs branching on nil-ness create subtle caller bugs.
- Enforce: review-only
- Sources: https://go.dev/wiki/CodeReviewComments, https://github.com/uber-go/guide

### GO-COLLECT-02 — **MUST** — Copy slices and maps at trust boundaries; never assume maps are goroutine-safe

- from A-SLICEMAP-02, A-SLICEMAP-03
- Rule: `copy` inbound collections on store and return fresh copies (or read-only views) of internal maps/slices. Guard concurrent map access with a mutex, or `sync.Map` for mostly-read shared caches.
- Why: Slices and maps are headers over shared arrays — retaining the caller's reference invites aliasing races; a single concurrent map write/read is a fatal error.
- Enforce: `test` with `-race`; review-only
- Sources: https://github.com/uber-go/guide, https://go.dev/doc/faq

### GO-COLLECT-03 — **SHOULD** — Use clear/min/max and the slices/maps/cmp packages

- from A-SLICEMAP-04
- Rule: Prefer builtins `clear`, `min`, `max` and `slices.*` / `maps.*` / `cmp.*` over hand-rolled loops; note `slices.Delete`-family zeroes the tail.
- Why: Stdlib versions are tuned, reviewed, and self-documenting; hand loops re-invite off-by-one and stale-reference bugs.
- Enforce: `lint:modernize,usestdlibvars,perfsprint` + review-only
- Since: Go 1.21
- Sources: https://go.dev/blog/go1.21

### GO-COLLECT-04 — **SHOULD** — Range maps only for snapshots; iterate with range-over-func

- from A-SLICEMAP-05, A-SLICEMAP-06
- Rule: Collect-then-act (`slices.Sorted(maps.Keys(m))`) when order or stability matters; `delete` during range is safe but insert visibility is unspecified. Expose `iter.Seq[V]`/`iter.Seq2[K,V]` (conventionally via an `All()` method) instead of ad-hoc Push/Pull channel adapters.
- Why: Map iteration order is randomized by design; ad-hoc channel adapters each invent a protocol (and goroutines) where the stdlib convention composes.
- Enforce: review-only
- Since: Go 1.23 (range over func)
- Sources: https://go.dev/blog/range-functions, https://go.dev/doc/go1.23

---

## Defer, panic, recover

### GO-DEFER-01 — **MUST** — Defer every cleanup at the acquisition site

- from A-DEFER-01
- Rule: `defer f.Close()` / `defer mu.Unlock()` immediately after success; never scatter manual closes across return paths.
- Why: Later edits add return paths; acquisition-site defers make leaks structurally impossible instead of review-dependent.
- Enforce: `lint:bodyclose,sqlclosecheck,rowserrcheck` + review-only
- Sources: https://go.dev/blog/defer-panic-and-recover, https://github.com/uber-go/guide

### GO-DEFER-02 — **SHOULD** — Remember defer evaluates arguments now and runs LIFO

- from A-DEFER-02
- Rule: Arguments bind at `defer` time; multiple defers run last-in-first-out; named returns are visible to deferred closures. Wrap `defer func() { log.Println(time.Since(t)) }()` — never `defer log.Println(time.Since(t))`.
- Why: Loop-variable capture and ordering surprises are the classic defer bugs.
- Enforce: `vet` (defers analyzer family; 1.22 vet flags non-deferred `time.Since` inside `defer`)
- Sources: https://go.dev/blog/defer-panic-and-recover, https://go.dev/doc/go1.22

### GO-DEFER-03 — **MUST** — Never use panic for ordinary errors; confine recover to boundaries

- from A-DEFER-03, A-DEFER-04
- Rule: Return errors; reserve panic for unrecoverable invariant violations. `recover()` only in deferred functions at API edges (parsers translating to error returns); re-panic unknown values; never recover to "keep serving" past corruption.
- Why: Panics unwind across package boundaries and cascade into fleet-wide crashes; distant recovery resumes execution with unknown held locks and half-written state.
- Enforce: review-only
- Sources: https://go.dev/wiki/CodeReviewComments, https://github.com/uber-go/guide

---

## Logging

### GO-LOG-01 — **MUST** — Log structured events with log/slog; all diagnostics to stderr

- from A-LOGGING-01, B-LOG-01, D-OBS-01
- Rule: Emit `slog.Info("started", "port", port)` through `TextHandler`/`JSONHandler` to **stderr**; never format key-values by hand; no log call may write to stdout. Level from `--log-level` (default info), `--debug` forces debug.
- Why: Unstructured strings defeat filtering at scale; stdout is the agent's API (GO-CLI-01) — one log line on stdout breaks machine parsing.
- Enforce: `lint:sloglint,forbidigo` + `test` (capture stdout/stderr separately; assert stdout purity)
- Since: Go 1.21
- Verified: yes (exp-obs; `skills/go-cli-hardening/snippets/slog_stderr/`)
- Sources: https://go.dev/blog/slog

### GO-LOG-02 — **SHOULD** — Prefer Logger.With for repeated attrs; use LogAttrs on hot paths

- from A-LOGGING-02
- Rule: Hoist stable attributes into `logger.With(...)`; build per-event attrs with typed constructors and `LogAttrs` where allocation matters.
- Why: `With` pre-formats once instead of per call; alternating k/v syntax is easy to mis-pair.
- Enforce: `vet` (slog analyzer flags non-string keys and missing values)
- Sources: https://go.dev/blog/slog, https://go.dev/doc/go1.22

### GO-LOG-03 — **MUST** — Never format a non-constant string with Printf

- from A-LOGGING-03
- Rule: `fmt.Print(s)` / `logger.Info(msg)` for dynamic text; reserve `Printf`-family for constant formats; name Printf-style helpers `...f`.
- Why: `%` in user data corrupts output or panics on missing verbs.
- Enforce: `vet:printf` (flags `fmt.Printf(s)` since Go 1.24)
- Since: Go 1.24
- Sources: https://go.dev/doc/go1.24

### GO-LOG-04 — **MUST** — Keep secrets and PII out of logs; pick levels deliberately

- from A-LOGGING-04, D-FS-04
- Rule: Wrap tokens in a `slog.LogValue` type rendering `[REDACTED]`; run composed strings (URLs, headers, command lines) through a `scrub` replacer before they reach logs/errors/outputs. Never log raw `cmd.Env`, request headers, or `Authorization` values. ERROR means actionable.
- Why: Log sinks are rarely access-controlled like databases; call-site discipline alone always misses one path.
- Enforce: `test` (secret-canary strings through every log path) + review-only
- Since: Go 1.21 (`log/slog`, `LogValuer`)
- Verified: yes (exp-redact; `skills/go-cli-hardening/snippets/redact_secrets/`, `go test` green)
- Sources: https://pkg.go.dev/log/slog#LogValuer

---

## Testing

### GO-TEST-01 — **SHOULD** — Write table tests with give/want fields and subtests

- from A-TESTING-01, B-TEST-01
- Rule: `tests := []struct{ give ...; want ... }{...}` + `t.Run` per case; keep bodies branch-free. Drive cases through struct slices; assert sentinels with `require.ErrorIs`, messages with `ErrorContains`.
- Why: Tables make behavior deltas comparable and cases cheap to add; conditional test bodies hide which assertions actually ran; `ErrorIs` survives rewording, unlike string equality.
- Enforce: `test`; `lint:testifylint,thelper,tparallel`
- Sources: https://github.com/uber-go/guide, https://go.dev/blog/subtests

### GO-TEST-02 — **SHOULD** — Fail with actual-vs-expected, inputs, and the helper marked

- from A-TESTING-02
- Rule: `t.Errorf("F(%q) = %d; want %d", in, got, want)`; call `t.Helper()` in helpers; register teardown with `t.Cleanup`, not deferred chains in the test body.
- Why: The debugger is usually a stranger; helper frames without `Helper()` point at the wrong line and orphaned cleanup leaks across subtests.
- Enforce: `test`; `lint:thelper`
- Sources: https://go.dev/wiki/CodeReviewComments, https://pkg.go.dev/testing

### GO-TEST-03 — **CONSIDER** — Parallelize deliberately

- from A-TESTING-03
- Rule: Call `t.Parallel()` only for tests that are actually independent; remember pre-1.22 loopvar capture semantics still apply below `go 1.22` language versions.
- Why: Unintended sharing makes failures order-dependent. (Deliberately not lint-gated: `tparallel`/`paralleltest` are OFF in the final config — they punish testscript-style and intentionally serial suites; see Decisions.)
- Enforce: `test`; review-only
- Since: Go 1.22 (per-iteration loop variables)
- Sources: https://github.com/uber-go/guide, https://go.dev/doc/go1.22

### GO-TEST-04 — **SHOULD** — Use testdata/ golden files for stable outputs

- from A-TESTING-04, B-TEST-02, C-TEST-05
- Rule: Store fixtures under `testdata/` (ignored by the go tool); snapshot command/version/template output into `testdata/*.golden`; bless with an `-update` flag; review diffs like code. Refresh ONLY via the explicit flag.
- Why: Inline literals rot on large outputs; goldens make diffs reviewable and regeneration a single flag; accidental churn breaks downstream parsers.
- Enforce: `test`
- Sources: `go help packages`, https://pkg.go.dev/testing

### GO-TEST-05 — **SHOULD** — Fuzz the parsers: F.Fuzz with seed corpus via F.Add

- from A-TESTING-05, B-TEST-05, C-TEST-03
- Rule: Add `FuzzXxx(*testing.F)` targets for untrusted-input code with representative `f.Add` seeds; run short fuzz in CI (`-fuzztime=30s` per target loop), long fuzz manually/nightly.
- Why: Hand-written cases miss edge encodings; the fuzzer's coverage feedback finds panics humans never imagine.
- Enforce: `test` (`go test -fuzz`); `ci` (30 s/target smoke)
- Since: Go 1.18
- Verified: yes (15 s smoke, 3M execs, PASS)
- Sources: https://go.dev/blog/fuzz-beta

### GO-TEST-06 — **SHOULD** — Benchmark with B.Loop and compare via benchstat

- from A-TESTING-06, C-TEST-05
- Rule: `for b.Loop() { ... }` keeps setup out of the timed region; report with `-benchmem`, compare with `benchstat` (needs 6+ samples for confidence), gate regressions in CI.
- Why: `b.N` loops inline setup costs and invite dead-code elimination; numbers without statistics invite phantom "regressions".
- Enforce: `test`
- Since: Go 1.24 (`B.Loop`)
- Verified: yes (benchstat runs; honest `~` on 3-sample data as designed)
- Sources: https://go.dev/doc/go1.24, https://go.dev/blog/pgo-preview

### GO-TEST-07 — **MUST** — Name tests/fuzzers/examples so the tests analyzer passes

- from A-TESTING-07
- Rule: `TestXxx(t *testing.T)`, `FuzzXxx(f *testing.F)`, `BenchmarkXxx(b *testing.B)`, `ExampleXxx_output` with exact signatures; keep examples runnable as documentation. Note: `testableexamples` only flags the `_suffix` form (`Example_printing`), not `Examplehelper`.
- Why: Malformed declarations silently never run — the suite is green while covering nothing.
- Enforce: `vet` (tests analyzer, runs under `go test`)
- Since: Go 1.24
- Sources: https://go.dev/doc/go1.24

### GO-TEST-08 — **SHOULD** — Expose test context and working directory via testing APIs

- from A-TESTING-08, B-TEST-03, B-TEST-06
- Rule: Derive timeouts from `t.Context()`/`b.Context()` and sandbox filesystem tests with `t.Chdir`; never `os.Chdir` process-wide in tests. Express CLI integration as `testdata/*.txtar` scripts run by `testscript` (hermetic `$WORK` dirs, golden `cmp`, `-u` refresh); pin env (`HOME`, `PATH`), fake servers via `httptest`. Split unit vs integration by `//go:build integration` tags, gated separately.
- Why: Process-global `os.Chdir` races parallel tests; testscript CLI tests are Windows-compatible and reviewable where bash harnesses rot on quoting and OS differences.
- Enforce: `test` + `ci`; `lint:usetesting`
- Since: Go 1.24 (`T.Context`, `T.Chdir`)
- Verified: yes (go-internal v1.16.0 testscript PASS)
- Sources: https://go.dev/doc/go1.24, https://pkg.go.dev/github.com/rogpeppe/go-internal/testscript

---

## Docs and godoc

### GO-DOCS-01 — **SHOULD** — Document every exported name with a full sentence starting with the name

- from A-DOCS-01, A-DOCS-02
- Rule: `// Request represents a request to run a command.` — capitalized, punctuated, name-first; same for non-trivial unexported declarations. Exactly one `// Package x ...` comment adjacent to the package clause (in `doc.go` for long docs).
- Why: Godoc and IDEs surface the first sentence everywhere; detached or duplicated comments vanish or render twice.
- Enforce: `lint:godot,godoclint` (style gate; `revive` exported-rule kept at default since the style gate owns it) + review-only
- Sources: https://go.dev/wiki/CodeReviewComments, https://google.github.io/styleguide/go/decisions

### GO-DOCS-02 — **SHOULD** — Ship runnable Examples for every new package; document sharp edges

- from A-DOCS-03, A-DOCS-04
- Rule: Add `ExampleXxx` functions in `_test.go` files demonstrating the primary call sequence — they run as tests and render in godoc. Document non-obvious contracts only (not goroutine-safe? needs `Stop`? ignores `ctx` deadline?), never restating the obvious.
- Why: Examples are the only documentation guaranteed to compile; the one undocumented sharp edge is the one that pages on-call.
- Enforce: `test` (examples execute under `go test`)
- Sources: https://go.dev/wiki/CodeReviewComments, https://google.github.io/styleguide/go/best-practices

---

## API stability and versioning

### GO-API-01 — **MUST** — Treat Go 1 compatibility as source compatibility, nothing more

- from A-API-01
- Rule: Assume programs recompile; never depend on unspecified behavior, `unsafe` internals, or `syscall` evolution.
- Why: Point releases may fix spec-violating bugs or patch security holes; code leaning on accidents breaks without recourse.
- Enforce: review-only
- Sources: https://go.dev/doc/go1compat

### GO-API-02 — **MUST** — Always use keyed struct literals across package boundaries

- from A-API-02
- Rule: `csv.Reader{Comma: ',', ...}`, never positional `csv.Reader{',', ...}`; same for nested literals and interfaces holding structs.
- Why: Adding an exported field is a compatible change only for keyed literals; unkeyed literals fail to compile on upgrade.
- Enforce: review-only
- Sources: https://go.dev/doc/go1compat

### GO-API-03 — **SHOULD** — Evolve by addition: new names, never changed meanings

- from A-API-03
- Rule: Add `ParseDuration2` instead of altering `ParseDuration`; keep old behavior under the old name per semantic import versioning (`/v2` module path for breaks).
- Why: Changing meanings silently corrupts downstream builds; distinct paths let old and new majors coexist in one build.
- Enforce: review-only
- Sources: https://go.dev/doc/go1compat, https://go.dev/doc/faq

### GO-API-04 — **MUST** — Declare your floor: go directive + stdversion vet

- from A-API-04
- Rule: Set an honest `go 1.N` floor in `go.mod`; keep `go test` running the `stdversion` check so too-new stdlib symbols fail fast.
- Why: Using `reflect.TypeFor` while claiming `go 1.21` ships code that cannot compile for your own users.
- Enforce: `vet:stdversion` (runs by default under `go test` since Go 1.27)
- Since: Go 1.23 (analyzer); default-on in `go test` Go 1.27
- Sources: https://go.dev/doc/go1.23, https://go.dev/doc/go1.27

---

## Modules and dependencies

### GO-MOD-01 — **MUST** — Let the go line mean it; manage toolchains explicitly

- from A-MODULES-01, B-REL-06, C-SEC-05
- Rule: Since 1.21 the `go` line is a minimum toolchain requirement; use the `toolchain` line for pins and let `go` auto-switch (`GOTOOLCHAIN`). Pin `GOTOOLCHAIN: go1.27.3` in CI; developers use stock `auto`.
- Why: Pre-1.21 code silently miscompiled on old toolchains; floating toolchains make "works on my machine" releases.
- Enforce: `ci` env
- Since: Go 1.21
- Verified: yes (`go env`: GOTOOLCHAIN=auto, go1.27.3)
- Sources: https://go.dev/blog/toolchain

### GO-MOD-02 — **SHOULD** — Keep go.mod tidy, minimal, and pruned

- from A-MODULES-02, B-DEP-01, B-DEP-02
- Rule: Run `go mod tidy` (which since 1.27 also merges duplicate require blocks); commit `go.sum`; no `vendor/` directory (lazyGit is the known exception, not the model); release builds may set `gomod.proxy: true`. Treat each new `require` as API surface; prefer stdlib (`slices`, `maps`, `log/slog`, `net/http`).
- Why: Stale requirements bloat builds and hide which dependency a package actually comes from; vendored trees rot and balloon diffs while the proxy + sums give reproducibility without the weight.
- Enforce: `ci` (`go mod tidy -diff` gate) + `lint:gomoddirectives,modernize,exptostd`
- Since: Go 1.23 (`tidy -diff`); Go 1.27 (require-block merge)
- Sources: https://go.dev/doc/go1.23, https://go.dev/doc/go1.27

### GO-MOD-03 — **SHOULD** — Track executable tools with tool directives, not tools.go

- from A-MODULES-03, C-SEC-06
- Rule: `go get -tool sigs.k8s.io/controller-tools/cmd/controller-gen`, then `go tool controller-gen`; upgrade with `go get tool`. Never `go install @latest` in CI. golangci-lint stays a version-pinned `go run ...@v2.15.0` invocation (its dep graph is too big for the main module).
- Why: Blank-import `tools.go` hacks pollute builds; tool directives version-lock binaries like any other dependency and cache executions.
- Enforce: review-only
- Since: Go 1.24
- Sources: https://go.dev/doc/go1.24

### GO-MOD-04 — **MUST** — Isolate private modules with GOPRIVATE, then scan everything

- from A-MODULES-05, A-SECURITY-03, B-CI-02
- Rule: Set `GOPRIVATE`/`GOAUTH` for internal hosts; run `govulncheck ./...` in CI (tool directive, exit-3 gate on reachable vulns) on every PR and weekly schedule, SARIF upload. Imported-but-unreached findings are informational.
- Why: Proxy/sumdb defaults leak internal paths and fail closed; reachability analysis separates real exposure from module-graph noise.
- Enforce: `govulncheck`; `ci`
- Verified: yes (govulncheck v1.9.0, "No vulnerabilities found", exit 0 on sample)
- Sources: https://go.dev/blog/govulncheck

### GO-MOD-05 — **SHOULD** — Pin GODEBUG/deprecation escapes in go.mod, not in shells

- from A-MODULES-04
- Rule: Declare transitional behavior with the `godebug` directive (`//go:debug` lines) so the whole team builds identically; remove before the setting's removal release.
- Why: Environment-only escapes diverge between laptops and CI; recorded settings are reviewable and expire loudly.
- Enforce: review-only
- Since: Go 1.23 (`godebug` directive); removed settings accepted only at final defaults Go 1.27
- Sources: https://go.dev/doc/go1.23, https://go.dev/doc/go1.27

---

## Performance

### GO-PERF-01 — **SHOULD** — Measure first: benchmarks, benchstat, profiles

- from A-PERFORMANCE-01
- Rule: No optimization without a benchmark delta (`benchstat old.txt new.txt`) and a pprof/mutex/trace profile naming the hotspot.
- Why: Intuition about hot paths is wrong more often than right; unmeasured "wins" usually trade readability for noise.
- Enforce: `test` (`-benchmem`, `-cpuprofile`, trace FlightRecorder)
- Sources: https://go.dev/blog/pgo-preview, https://go.dev/doc/go1.25

### GO-PERF-02 — **CONSIDER** — Enable PGO with a checked-in default.pgo

- from A-PERFORMANCE-02
- Rule: Collect a production CPU profile, commit it as `default.pgo` in the main package, and rebuild; expect single-digit-percent gains compounding per release.
- Why: The compiler cannot see production shapes; PGO feeds real call graphs into inlining and devirtualization.
- Enforce: review-only (measure via GO-PERF-01)
- Since: Go 1.21
- Sources: https://go.dev/blog/pgo-preview, https://go.dev/blog/go1.21

### GO-PERF-03 — **SHOULD** — Pre-size collections and prefer strconv on hot paths

- from A-PERFORMANCE-03
- Rule: `make([]T, 0, n)` / `make(map[K]V, hint)` where n is known; `strconv` over `fmt.Sprintf` for primitive conversions in loops.
- Why: Growth reallocations and reflection-based formatting dominate profiles of encode/serve loops; capacity hints are nearly free.
- Enforce: `lint:prealloc` is OFF (FP on idiomatic append loops) — review-only; `lint:perfsprint` covers sprintf
- Sources: https://github.com/uber-go/guide

---

## Security basics

### GO-SEC-01 — **MUST** — Confine filesystem access with os.Root

- from A-SECURITY-01, D-FS-01, D-FS-03
- Rule: `root, _ := os.OpenRoot(dir)` then `root.Open/Create/Mkdir/Stat/...`; never join unsanitized input into paths, never follow agent-supplied symlinks by hand. `os.Root` already refuses escapes through symlinks (verified: `link.txt -> outside` blocked, absolute paths blocked).
- Why: Path traversal via `..` and symlink escape is the classic file-serving CVE; `Root` enforces containment in the runtime, not in your string code.
- Enforce: review-only (+ custom lint candidate: forbid `os.ReadFile`/`os.Open` with non-constant paths outside the root helper)
- Since: Go 1.24 (extended Go 1.25)
- Verified: yes (exp-osroot; `skills/go-cli-hardening/snippets/osroot_store/`)
- Sources: https://go.dev/doc/go1.24, https://go.dev/doc/go1.25

### GO-SEC-02 — **MUST** — Generate secrets with crypto/rand, never math/rand

- from A-SECURITY-02
- Rule: Keys, tokens, nonces come from `crypto/rand` (`rand.Text()` for strings); `math/rand/v2` is for simulations.
- Why: `math/rand` output is predictable from a few observations; one "temporary" key becomes a permanent backdoor.
- Enforce: `lint:depguard` (deny `math/rand` for security-sensitive use → point at gosec) + review-only
- Since: Go 1.22 (`math/rand/v2`); Go 1.24 (`crypto/rand.Text`)
- Sources: https://go.dev/wiki/CodeReviewComments, https://go.dev/doc/go1.22

### GO-SEC-03 — **MUST** — Enforce modern TLS floors and CSRF checks explicitly

- from A-SECURITY-04
- Rule: Leave TLS 1.2+ minimums and ECDHE-only suites at defaults (do not re-enable via `tls10server`/`tlsrsakex` escapes); put state-changing HTTP routes behind `http.CrossOriginProtection`.
- Why: Protocol-downgrade escapes exist for legacy interop, not as defaults; unprotected endpoints accept forged cross-site writes from any browser session.
- Enforce: review-only
- Since: Go 1.22 (TLS 1.2 minimum); Go 1.25 (`CrossOriginProtection`)
- Sources: https://go.dev/doc/go1.22, https://go.dev/doc/go1.25

### GO-SEC-04 — **MUST** — Validate and normalize all external input at the edge

- from A-SECURITY-05
- Rule: Reject malformed URLs, unescape path segments once, and bind `ServeMux` patterns (`/items/{id}` + `r.PathValue`) instead of manual splitting.
- Why: Double-parsing (mux vs handler) creates smuggling gaps; canonical routing plus strict parsing closes them in one place.
- Enforce: `vet` (hostport analyzer); `test`
- Since: Go 1.22 (enhanced ServeMux); Go 1.25 (hostport analyzer); Go 1.26 (`urlstrictcolons`)
- Sources: https://go.dev/doc/go1.22, https://go.dev/doc/go1.26

### GO-SEC-05 — **MUST** — Run gosec with the full rule set; G104 excluded

- from C-LINT-11, B-IO-04
- Rule: gosec defaults (G101–G710 incl. taint rules) minus G104 (errcheck owns that signal). Keep a `_test.go` exclusion (fixtures trip G101/G304). Set file/dir perms explicitly at creation (`0600`/`0700` for secrets, never relying on umask).
- Why: Full coverage (hardcoded creds, SSRF/path-traversal taint, SQL concat, weak crypto); umask varies across machines — tokens written world-readable are a CVE.
- Enforce: `lint:gosec` + `make sec` (`go tool gosec`)
- Verified: yes (sample: G101, G107, G202 fired)
- Sources: https://github.com/securego/gosec (v2.30.0)

---

## CLI structure and UX

### GO-CLI-01 — **MUST** — stdout is data, stderr is chatter; --json envelope is law

- from D-CLI-01, B-CLI-04, B-LOG-01, D-OBS-02
- Rule: `--json` prints exactly one JSON envelope `{version, command, ok, error?, data?}` to **stdout** and nothing else. All progress/logs go to **stderr**. `SetEscapeHTML(false)` for readable diffs/URLs. Thread an `IOStreams`-like struct (in/out/err + TTY/color detection) through every command; human formatting only on TTY.
- Why: Agents parse stdout programmatically; one stray log line breaks the pipe; pipes and `--json` break the moment color leaks to stdout; injectable streams make every command testable.
- Enforce: `test` (run with piped stdout; decoder reads exactly one value, `More()==false`) + `lint:forbidigo` (no `fmt.Print*` outside the output package)
- Verified: yes (exp-cli, exp-obs; `skills/go-cli-hardening/snippets/cli_contracts/`)
- Sources: https://pkg.go.dev/encoding/json#Encoder

### GO-CLI-02 — **SHOULD** — Fixed exit-code contract, documented in --help

- from D-CLI-02, B-CLI-03
- Rule: `0` ok · `1` usage/error · `2` verify completed with findings · `3` infrastructure failure (timeout, network, store lock). `--json` envelopes carry the same code in `error.code`. Set `SilenceUsage = true` on the root; never leak flag-usage text on runtime failures; map error types to the enum.
- Why: Agents branch on exit codes; conflating "findings" with "crashed" mis-trains downstream automation; usage spam on every runtime error teaches users to ignore output.
- Enforce: `test` (assert exit codes) + review-only
- Sources: cli/cli `pkg/cmd/root/root.go`, `internal/ghcmd/cmd.go` (pattern)

### GO-CLI-03 — **MUST** — Never prompt without a TTY; degrade to --yes or fail

- from D-CLI-03
- Rule: Gate every prompt on `term.IsTerminal(fd)` AND non-interactive env (`CI`, `<APP>_ASSUME_YES`); without a TTY, require explicit `--yes` or exit 1 with a machine-readable error. Use `golang.org/x/term` (stdlib-adjacent), not a third-party isatty wrapper.
- Why: A hung prompt blocks an agent pipeline forever.
- Enforce: `test` (piped stdin/stdout prompt test) + review-only
- Verified: yes for detection (exp-cli; piped stdout correctly reported `isatty=false`)
- Sources: https://pkg.go.dev/golang.org/x/term (v0.47.0 resolved)

### GO-CLI-04 — **SHOULD** — Respect NO_COLOR; emit color only on TTY and only to stderr

- from D-CLI-04
- Rule: Disable ANSI colors when `NO_COLOR` is set (any value, including empty) or stdio is not a TTY. Colors never touch stdout JSON.
- Why: Agent-scraped logs with escape codes corrupt parsing and diffs.
- Enforce: `test` (set `NO_COLOR=1`, assert no `\x1b[` in output)
- Sources: https://no-color.org
- Verified: no (source-only — trivial env check, not executed)

### GO-CLI-05 — **MUST** — signal.NotifyContext for graceful cancellation

- from D-CLI-05
- Rule: Derive the root context from `signal.NotifyContext(ctx, SIGINT, SIGTERM)`; on cancel, kill running children (GO-EXEC-01), abandon in-flight HTTP (GO-HTTP-01), and exit with code 3 + a JSON error envelope, not a bare stack trace.
- Why: `Ctrl-C` during a 10-minute verify run must not orphan `gh`/test processes.
- Enforce: `test` (send process SIGINT, assert cleanup + exit code)
- Since: Go 1.16
- Verified: yes (exp-cli; self-sent SIGINT cancelled the context in ~200 ms)
- Sources: https://pkg.go.dev/os/signal#NotifyContext

### GO-CLI-06 — **SHOULD** — Ship --json/--jq/--template, not just pretty tables

- from B-CLI-05
- Rule: Every list/show command gets `--json <fields>` (+ `--jq`, `--template`); share one exporter implementation across commands.
- Why: One code path serves humans and scripts; field lists keep output stable for automation.
- Enforce: review-only
- Sources: cli/cli `pkg/cmdutil/json_flags.go` (shared `AddJSONFlags` helper)

### GO-CLI-07 — **SHOULD** — Generate completions and man pages in release, not by hand

- from B-CLI-06
- Rule: Emit shell completions + man pages from the command tree during the release build (cobra `mango-cobra`/`fang`-style hooks); check into archives, never hand-edit.
- Why: Docs drift the moment they're manual; generation is free with a command tree.
- Enforce: `ci` (fail release if generated docs change unexpectedly)
- Sources: cli/cli + goreleaser `.goreleaser` hooks

### GO-CLI-08 — **SHOULD** — Default to cobra+pflag for multi-command CLIs; know when not to

- from B-CLI-01, B-CLI-02, D-CLI-06
- Rule: Multi-command CLIs use `cobra` + `pflag` (thin wrappers only, e.g. hugo's `simplecobra`); single-purpose fast tools may hand-roll `flag`; daemon CLIs may use kingpin-style builders. For verdict specifically: start with std `flag` + a tiny dispatcher per the zero-dependency decision (GO-CI-07 context), graduate to `cobra` (v1.10.2) or `kong` (v1.16.1) only when subcommand/man-page/completion needs justify a dependency. (See Decisions.)
- Why: Completions, man pages, and help templating come free and contributors already know the shape; but cobra's machinery is dead weight for `fzf`-style tools and forbidden by a zero-dep policy.
- Enforce: review-only
- Verified: no (version dates from `go list -m`: cobra v1.10.2 2025-12-03, kong v1.16.1 2026-08-09)
- Sources: https://github.com/spf13/cobra, https://github.com/alecthomas/kong

### GO-CLI-09 — **SHOULD** — Embed prompts/templates; self-describe --version

- from D-EMBED-01, D-EMBED-02, B-REL-02
- Rule: Ship review prompts, SARIF templates, policy text via `//go:embed` — the binary is self-contained and content is versioned with the code that interprets it. `var version = "dev"` stamped with `-ldflags "-X main.version=vX.Y.Z"` in release; fall back to `debug.ReadBuildInfo` (VCS stamping) for dev builds; `--version` prints version plus GOOS/GOARCH. Always pass `-trimpath`; stamp `version/commit/date/builtBy/treeState`; set `mod_timestamp` from commit time.
- Why: A missing sidecar file turns a CLI into a confusing runtime error; bug reports without versions are undebuggable; `-trimpath` removes host paths for bit-identical rebuilds.
- Enforce: `test` (release pipeline asserts `--version != dev`); `ci` (rebuild-twice diff check)
- Since: Go 1.16 (`embed`); Go 1.18 (VCS stamping)
- Verified: yes (exp-embed: 127-byte prompt embedded and read back; all three version arms; `go version -m` shows `-trimpath=true`, CGO off, no local paths)
- Sources: https://pkg.go.dev/embed, https://pkg.go.dev/runtime/debug#ReadBuildInfo

---

## Config and env precedence

### GO-CONFIG-01 — **MUST** — Enforce flags > env > file > defaults, in that order

- from B-CONFIG-01
- Rule: Every option resolves: explicit flag, then env var, then config file, then built-in default. Document the chain per option.
- Why: Containers (env), humans (flags), and persistence (file) coexist without guessing which wins.
- Enforce: `test` (precedence matrix test per option)
- Sources: golangci-lint `pkg/commands/flagsets.go` + viper; rclone `fs/config/config.go`

### GO-CONFIG-02 — **SHOULD** — Record and report the source of each secret

- from B-CONFIG-02
- Rule: When a credential can come from env, file, or keychain, remember which one won and say so in errors/status (`currently active token for X is from GH_TOKEN`).
- Why: "Token invalid" is unactionable when three token sources exist; source attribution fixes it in one line.
- Enforce: `test`
- Sources: cli/cli `internal/config/auth_config_test.go` (`t.Setenv` → source attribution)

### GO-CONFIG-03 — **SHOULD** — Keep machine-readable config forward-compatible; create dirs with least privilege

- from B-CONFIG-03, B-CONFIG-04
- Rule: Parse config files leniently (ignore unknown keys or migrate them); write them canonically; version the schema when semantics change. `MkdirAll` with restrictive perms on first run; a broken config warns on stderr and the command continues with safe defaults where possible.
- Why: Old binaries must not choke on new config keys after a rollback; first-run crashes on locked-down `$HOME` are a support sink; world-readable dirs leak tokens.
- Enforce: `test` (round-trip + unknown-field tests; corrupt-config-still-runs test)
- Sources: cli/cli `internal/ghcmd/cmd.go`, `internal/config/refreshable.go`

---

## Subprocess execution

### GO-EXEC-01 — **MUST** — Kill the whole process tree on timeout (Unix: process groups)

- from D-EXEC-01, D-EXEC-04, B-EXEC-05
- Rule: On Unix, start watchdog children with `SysProcAttr{Setpgid: true}`, override `cmd.Cancel` to `syscall.Kill(-cmd.Process.Pid, SIGKILL)`, and set `cmd.WaitDelay`. On Windows, kill the tree via Job Objects (`CreateJobObject`/`AssignProcessToJobObject` with `JOB_OBJECT_LIMIT_KILL_ON_CLOSE`, via `golang.org/x/sys/windows`) or, weaker, `taskkill /F /T /PID`. Isolate per-OS code in `*_unix.go` / `*_windows.go` file pairs behind a tiny common interface.
- Why: `exec.CommandContext` alone kills only the direct child — a backgrounded grandchild ignoring SIGTERM survives. Verified: plain timeout returned in 502 ms but the `sleep 30` grandchild lived; with the group kill, 502 ms and nothing survived.
- Enforce: `test` (spawn `sh -c 'trap "" TERM; sleep 30 & wait'`, assert no survivor) + Windows CI
- Since: Go 1.20 (`Cancel`/`WaitDelay`)
- Verified: yes Unix half (exp-subproc A/C; `skills/go-cli-hardening/snippets/exec_groupkill_unix/`); Windows Job Objects source-only
- Sources: https://pkg.go.dev/os/exec, https://pkg.go.dev/golang.org/x/sys/windows

### GO-EXEC-02 — **MUST** — Always exec with a context and WaitDelay; check ctx.Err() for timeouts

- from B-EXEC-01, D-EXEC-02, D-EXEC-05
- Rule: Build subprocesses with `exec.CommandContext`; set `cmd.WaitDelay` (2 s); treat `exec.ErrWaitDelay` with a successful `ProcessState` as success-with-warning. After `Run`, report timeouts via `ctx.Err() == context.DeadlineExceeded`, NOT via `err != nil` — a timeout kill surfaces as `*exec.ExitError` (`signal: killed`, `ExitCode() == -1`) with a **nil** Go error.
- Why: Without `WaitDelay`, `Run` can block until grandchildren holding the output pipe exit (verified: SIGKILLED at 500 ms, `Run` returned after 30.024 s). `timedOut` distinguishes "verifier hung" from "verifier failed fast".
- Enforce: `test` (assert `timedOut==true, err==nil` on a sleep/timeout pair)
- Since: Go 1.20
- Verified: yes (exp-subproc B vs C; exp-watchdog case 3; `skills/go-cli-hardening/snippets/exec_watchdog/`)
- Sources: https://pkg.go.dev/os/exec

### GO-EXEC-03 — **MUST** — Never invoke a shell for agent-supplied commands; pass argv

- from D-EXEC-03
- Rule: Build commands as `exec.Command(name, args...)`. Reserve `sh -c` for fixed, review-approved internal glue only — and prefer rewriting without a shell.
- Why: Shells add glob expansion, word splitting, metacharacter injection, and `CDPATH`/`BASH_ENV`-style env influence. Verified: `echo "*.go"` via argv printed literally `*.go`.
- Enforce: custom lint (forbid `exec.Command("sh", "-c", ...)` / `exec.Command("bash", ...)` outside an allowlisted helper) + `test`
- Verified: yes (exp-env; `skills/go-cli-hardening/snippets/exec_watchdog/`)
- Sources: https://pkg.go.dev/os/exec

### GO-EXEC-04 — **MUST** — Scrub the child environment (allowlist, not denylist)

- from B-EXEC-02, D-EXEC-06
- Rule: Set `cmd.Env` explicitly (`PATH`, `HOME=/nonexistent`, `LANG`, `TZ`, plus per-command extras). Never inherit `os.Environ()` for agent-influenced commands. Redact secrets from anything logged.
- Why: Inherited env leaks `GH_TOKEN`/`GITHUB_TOKEN`/proxy secrets and lets repos influence tools via `GIT_*`/`GH_*`/`BASH_ENV`. Verified: parent exported `GH_TOKEN=supersecret`; child saw `GH_TOKEN=unset`.
- Enforce: `test` (child prints redacted env; assert no secret names) + review-only
- Verified: yes (exp-env)
- Sources: https://pkg.go.dev/os/exec

### GO-EXEC-05 — **MUST** — Bound child output; extract exit codes with ExitError

- from B-EXEC-04, D-EXEC-07
- Rule: Cap captured stdout/stderr (e.g. 1 MiB/stream, discard overflow) and read status via `errors.As(err, &exitErr); exitErr.ExitCode()`. A non-zero exit is data (verifier found issues), not a Go error. Tee subprocess output to both logs and a buffer; attach the tail to the returned error.
- Why: Tool output is attacker-influenced; unbounded capture is an OOM vector (verified: 5 MiB of output collapsed to the 1 MiB cap); "publishing failed" without the tool's stderr is undebuggable.
- Enforce: `test` (error output contains tool stderr; `exit 3` → `code=3, err=nil`)
- Since: `ExitError.ExitCode` since Go 1.12
- Verified: yes (exp-watchdog cases 2/4)
- Sources: https://pkg.go.dev/os/exec

### GO-EXEC-06 — **SHOULD** — Resolve binaries securely (safeexec pattern)

- from B-EXEC-03
- Rule: Never trust bare `exec.LookPath` on Windows; use a lookup that defends against current-directory `.exe` planting.
- Why: Windows `LookPath` historically resolved from the working directory — a repo clone can plant `git.exe`.
- Enforce: `lint:gosec` (G204 audit) + review-only
- Sources: cli/cli `internal/ghcmd/cmd.go` (`github.com/cli/safeexec`)

---

## JSON handling

### GO-JSON-01 — **MUST** — Strict, capped decoding is the only entry point for untrusted JSON

- from D-JSON-01, D-JSON-02
- Rule: Route every untrusted document through one helper enforcing: total-size cap (reject before decode), `io.LimitReader` per value, `DisallowUnknownFields`, `UseNumber`, single-value + trailing-data check. Pin exact key spellings in tests — v1 matches object keys **case-insensitively** (`{"FILE":..}` fills `File`) and resolves **duplicate keys last-wins, silently** (`{"n":1,"n":2}` → `2`, no error).
- Why: Defaults are permissive (unknown fields ignored, `float64` numbers, unbounded reads) and the two silent-corruption pitfalls let an agent smuggle `{"Rule":..,"rule":..}` past review. Verified: unknown field rejected; 1 MiB string vs 4 KiB cap failed fast; `9007199254740993` survived only via `UseNumber`.
- Enforce: custom lint (forbid bare `json.Unmarshal`/`json.Decode` on untrusted paths) + `test` (golden reject-cases; `{"FILE":..}` and `{"n":1,"n":2}` outcomes)
- Verified: yes (exp-json; `skills/go-cli-hardening/snippets/json_untrusted/`)
- Sources: https://pkg.go.dev/encoding/json

### GO-JSON-02 — **MUST** — Preserve unknown fields with RawMessage maps, not structs

- from D-JSON-03
- Rule: For read-modify-write stores that must be forward-compatible, decode to `map[string]json.RawMessage`, extract known fields, mutate, re-marshal. Do not rely on struct decoding to round-trip.
- Why: Struct decode drops unknown members. Verified: `{"zzz":"keep"}` survived a modify cycle via the RawMessage map.
- Enforce: `test` (round-trip golden with extra keys)
- Verified: yes (exp-json case 7)
- Sources: https://pkg.go.dev/encoding/json

### GO-JSON-03 — **SHOULD** — Deterministic output: sort keys, fix escaping, avoid float64

- from D-JSON-06, A-PACKAGE-09
- Rule: Marshal canonical bytes with sorted map keys (v1 sorts `map[string]any` keys — pin it in a test), `SetEscapeHTML(false)` only deliberately, never let `float64` touch IDs/hashes. Annotate every marshaled struct with explicit field tags (`json:"price"`) so renames never silently change the wire format. Existing wire formats win: tag-style linters (tagliatelle) apply to new types only — exclude legacy wire types via path/linter exclusions (commented pattern in the adopting repo's `.golangci.yml`), or disable tagliatelle when the product's formats are mixed; never rename a stored or transmitted field to satisfy a linter.
- Why: Store diffing, hashing, and golden tests need byte-stability; untagged fields couple Go renames to every consumer's parser; renaming a frozen field breaks every existing store and bundle under an additive-only changelog.
- Enforce: `test` (golden bytes); `lint:musttag,tagalign,tagliatelle` (tagliatelle new-types-only; legacy wire paths excluded)
- Sources: https://pkg.go.dev/encoding/json
- Verified: partial (key order + HTML escaping exercised implicitly; float64 precision verified in GO-JSON-01)

### GO-JSON-04 — **CONSIDER** — Validate schemas with maintained libraries, hand-write the rest

- from D-JSON-04
- Rule: For structural validation beyond `DisallowUnknownFields`, use `github.com/santhosh-tekuri/jsonschema` (v6.0.3, 2026-06-28 — actively maintained) or hand-written validators for small fixed schemas. Avoid unmaintained schema kits.
- Why: `DisallowUnknownFields` checks names, not shapes (types, ranges, required combinations); hand-rolling full JSON-Schema semantics is a bug farm.
- Enforce: review-only
- Verified: no (source-only — maintenance checked via `go list -m`; behaviour not exercised)

### GO-JSON-05 — **MUST** — Do NOT adopt encoding/json/v2 yet (Go 1.27 status finding)

- from D-JSON-05, A-PERFORMANCE-05 (overruled for now — see Decisions)
- Rule: Stay on `encoding/json` + GO-JSON-01 until `json/v2` leaves experiment. Re-evaluate each Go release.
- Why: In go1.27.3 the entire `encoding/json/v2` and `encoding/json/jsontext` packages still carry `//go:build goexperiment.jsonv2`. The toolchain enables the experiment by default, but `GOEXPERIMENT=nojsonv2 go run .` fails with `build constraints exclude all Go files in .../encoding/json/v2` — the API is not frozen and can be compiled out from under you. v2's fixes are real (verified: case-sensitive by default, dup rejection, `omitzero`) but Go 1.27 v2 has NO unknown-field preservation tag, so GO-JSON-02 stands regardless.
- Enforce: custom lint (forbid `encoding/json/v2` and `jsontext` imports) + review-only
- Since: experiment since Go 1.25; still gated in Go 1.27
- Verified: yes (exp-json v2 cases + `GOEXPERIMENT=nojsonv2` negative control)
- Sources: https://pkg.go.dev/encoding/json/v2

---

## Atomic writes and locking

### GO-ATOMIC-01 — **MUST** — Replace stores via temp-file + fsync + rename + dir-fsync

- from D-ATOMIC-01
- Rule: Write to `os.CreateTemp` in the **same directory**, `Write`, `Sync`, `Close`, `Chmod(0600)`, `os.Rename`, then best-effort `Sync` the directory fd. Readers then see only old-or-new, never torn. Never truncate-write config/state in place.
- Why: Crash-safety demo — 200 alternating 1 MiB rewrites under a concurrent reader: direct `os.WriteFile` tore **219** reads; atomic rewrites tore **0** (1604 clean reads), mode stayed `600`.
- Enforce: `test` (reader/writer hammer asserting zero torn reads)
- Verified: yes (exp-atomic; `skills/go-cli-hardening/snippets/atomic_write/`)
- Sources: https://pkg.go.dev/os

### GO-ATOMIC-02 — **SHOULD** — Lock read-modify-write cycles (flock), keep critical sections tiny

- from D-ATOMIC-03, B-IO-02
- Rule: Atomic replace alone does not prevent lost updates — hold an advisory lock (`gofrs/flock`, v0.13.1, maintained) across read→modify→`atomicWrite`, then release. Never hold the lock across subprocess execution or network calls. Guard caches/databases/config that two processes may touch with an OS file lock (`lockedfile`-style), not just a mutex — mutexes don't cross processes.
- Why: Concurrent agents doing read-modify-write on shared JSON stores silently last-writer-wins without a lock.
- Enforce: `test` (two-process increment hammer) + review-only
- Verified: no (source-only — flock API reviewed, race demo not run)
- Sources: https://github.com/gofrs/flock, https://pkg.go.dev/github.com/rogpeppe/go-internal/lockedfile

### GO-ATOMIC-03 — **CONSIDER** — Prefer tiny vendored helpers over dormant atomic-write deps

- from D-ATOMIC-02, B-IO-01, B-IO-03
- Rule: `google/renameio` (v1.0.1, 2021-04-06 — dormant) and `natefinch/atomic` do exactly GO-ATOMIC-01. Either vendor the ~60-line pattern or depend consciously. Wrap reads/writes prone to `EINTR`/AV-scan races in a small retry helper (`robustio`-style); never blanket-retry permission errors. Remember the Windows limit: `os.Rename` cannot replace an existing destination — remove it first and document the non-atomic window.
- Why: A 5-year-dormant dep is fine for 60 lines of rename logic, but a vendored helper avoids supply-chain surface on the most security-critical primitive.
- Enforce: review-only + Windows CI
- Verified: no for the dep choice (source-only); the underlying pattern is verified in GO-ATOMIC-01
- Sources: https://github.com/google/renameio, https://pkg.go.dev/os#Rename

---

## Filesystem security

### GO-FS-01 — **SHOULD** — Treat symlinks as hostile; never follow agent-supplied links

- from D-FS-03
- Rule: Under `os.Root`, escapes through symlinks are already refused (verified: `link.txt -> outside` blocked). Outside `Root`, `EvalSymlinks` + prefix check before use, and re-check after open (TOCTOU) for high-value paths.
- Why: Symlink-swap between check and use is the classic bypass of naive prefix checks.
- Enforce: `test` (symlink corpus) + review-only
- Since: Go 1.24 (`os.Root`); `EvalSymlinks` since Go 1.0
- Verified: yes for the `os.Root` half (exp-osroot); TOCTOU re-check source-only
- Sources: https://pkg.go.dev/os#OpenRoot, https://pkg.go.dev/path/filepath#EvalSymlinks

### GO-FS-02 — **MUST** — 0600 for secrets, MkdirTemp + deferred cleanup for scratch

- from D-FS-02
- Rule: Create token/key material with `0600`; create scratch space only via `os.MkdirTemp` with `defer os.RemoveAll`. Never use predictable temp names.
- Why: Verified file mode `600` end-to-end; predictable temp paths are symlink-race primitives.
- Enforce: `test` (assert modes) + custom lint candidate (forbid `0755`/`0777` literals)
- Since: Go 1.0 (`MkdirTemp` since Go 1.17)
- Verified: yes (exp-atomic, exp-osroot)
- Sources: https://pkg.go.dev/os#MkdirTemp, https://pkg.go.dev/os#OpenFile

### GO-FS-03 — **SHOULD** — Isolate OS differences in build-tagged files, not if-runtime.GOOS

- from D-XPLAT-03, B-LAYOUT-05
- Rule: `feature_unix.go` (`//go:build unix`) vs `feature_windows.go`; shared logic stays portable. Prefer the `unix` build constraint (Go 1.19+) over enumerating `darwin || linux`.
- Why: Compile-time separation keeps uncompilable syscalls out of the other OS's build instead of failing at runtime; reviewers see the platform matrix in the file list.
- Enforce: `test` (CI matrix) + review-only
- Since: Go 1.19 (`unix`/`windows` build constraints)
- Verified: yes (exp-xplat; correct file per GOOS, all three platforms build)
- Sources: https://pkg.go.dev/cmd/go#hdr-Build_constraints

### GO-FS-04 — **MUST** — filepath for disk, path for URLs — never mix

- from D-XPLAT-01
- Rule: `path/filepath` for everything on disk; `path` only for URL/GitHub API paths. Never split paths on `/` or `\` by hand. Parse line-oriented tool output with `bufio.Scanner` (strips trailing `\r`) and compare with `\n`-normalised goldens so CRLF from gh/git on Windows is harmless.
- Why: Separator semantics differ per OS; the bug only manifests on Windows, which is why GO-XPLAT-02 exists.
- Enforce: custom lint candidate (flag `path.Join` on disk paths) + Windows CI
- Verified: yes, compiles+runs on darwin, builds for windows+linux (`skills/go-cli-hardening/snippets/xplat/`; CRLF `["l1","l2","l3"]` verified)
- Sources: https://pkg.go.dev/path/filepath, https://pkg.go.dev/bufio#Scanner

---

## HTTP client hygiene

### GO-HTTP-01 — **MUST** — Never use http.DefaultClient; ship one hardened constructor

- from D-HTTP-01, B-HTTP-01
- Rule: Single `hardenedClient()` with: total `Timeout` (~10 s), tuned `Transport` (`ProxyFromEnvironment`, `MaxIdleConns`, header/TLS/dial timeouts), and every request on a `context.WithTimeout` deadline. Expose a `WithTimeout` knob; document what the zero value means; prefer fail-fast defaults. Inject auth as a `RoundTripper` wrapper, never at each call site; keep a constructor accepting an existing `*http.Client` for tests.
- Why: `DefaultClient` has no timeouts — one stalled server hangs the whole CLI forever. Verified: stalled server + 300 ms ctx → `context deadline exceeded`; normal small body unaffected.
- Enforce: custom lint (forbid `http.DefaultClient`, `http.Get/Post`, `http.DefaultTransport` outside the constructor) + `test`
- Since: `http.Client.Timeout` since Go 1.3
- Verified: yes (exp-http; `skills/go-cli-hardening/snippets/http_client/`)
- Sources: https://pkg.go.dev/net/http

### GO-HTTP-02 — **MUST** — Cap every response body (LimitReader N+1, then judge)

- from D-HTTP-02
- Rule: `io.Copy(io.Discard, io.LimitReader(body, cap+1))`; if bytes read exceed `cap`, abort with an explicit error. Apply to API responses AND to anything piped from tool output.
- Why: Unbounded `io.ReadAll` on an attacker-influenced stream is an OOM kill. Verified: 5 MiB body vs 1 MiB cap → clean `response exceeded 1048576-byte cap` after reading exactly cap+1.
- Enforce: `test` (oversize httptest body) + custom lint candidate (forbid bare `io.ReadAll(resp.Body)`)
- Verified: yes (exp-http)
- Sources: https://pkg.go.dev/io#LimitReader

### GO-HTTP-03 — **MUST** — Retry only idempotent methods; honor Retry-After; jitter the backoff

- from D-HTTP-03, B-HTTP-02
- Rule: Auto-retry GET/HEAD/OPTIONS on 429/5xx only — never POST/PATCH (verdict posts comments/checks); parse `Retry-After` (delta-seconds and HTTP-date); `sleep = rand([0, min(base<<attempt, max)])`; make the wait cancellable via `ctx`. For secondary rate limits, stop issuing requests until reset (+ small buffer).
- Why: Blind retries double-post review comments and amplify rate limits into bans. Verified: policy unit tests (GET retryable, POST not, both Retry-After forms) and a live flaky-server run (2×429 then recovery, 3 upstream calls).
- Enforce: `test` (table + httptest flaky server; httptest 429 → assert sleep/backoff)
- Verified: yes (exp-retry; `skills/go-cli-hardening/snippets/retry_policy/`, `go test` green)
- Sources: https://pkg.go.dev/net/http, https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api

### GO-HTTP-04 — **CONSIDER** — Paginate defensively; keep proxy support on

- from D-HTTP-04, D-HTTP-05, B-HTTP-03
- Rule: Follow `Link: <...>; rel="next"` (REST) or `pageInfo` cursors (GraphQL) with a max-pages budget and a per-item size cap; de-duplicate by ID since pages can overlap under concurrent mutation. Model pagination as `ListOptions{Page, PerPage}` + `Response.NextPage` in one shared helper. Leave `Proxy: http.ProxyFromEnvironment` enabled (enterprise checkouts need it); telemetry discipline is about destinations, never dialing non-code-host URLs — not about breaking the proxy.
- Why: PRs with thousands of comments make unbounded pagination a memory sink; overlapping pages double-count findings; disabling proxy support breaks corporate users.
- Enforce: review-only + `test` with httptest paginated fixture
- Verified: no for pagination (source-only — no live API fixture); proxy field compiled but not exercised
- Sources: https://docs.github.com/en/rest/using-the-rest-api/using-pagination-in-the-rest-api

---

## Observability without telemetry

### GO-OBS-01 — **SHOULD** — Progress on stderr is structured key=value; errors are JSON-shaped

- from D-OBS-02
- Rule: Human progress as `slog` attributes (`step=`, `attempt=`); failures also surface in the `--json` envelope's `error.{code,message}` so agents never parse stderr.
- Why: Two audiences, two channels; mixing them breaks both.
- Enforce: `test` (envelope error-shape goldens)
- Verified: partial (envelope + stderr split verified in GO-CLI-01; error-shape convention is project policy)
- Sources: https://pkg.go.dev/log/slog

### GO-OBS-02 — **SHOULD** — Initialize logging after flags; never log-and-return

- from A-LOGGING-05
- Rule: No log calls before flag parsing (incl. `init`); on error, return it and let the edge log once — do not log at every layer.
- Why: Pre-flag logging ignores the user's verbosity/output config; per-layer logging multiplies one failure into pages of duplicates.
- Enforce: review-only
- Sources: https://google.github.io/styleguide/go/best-practices, https://github.com/uber-go/guide

### GO-OBS-03 — **MUST** — No network calls except to configured code hosts

- from D-OBS-03, B-LOG-03
- Rule: The only destinations are the code-host API and user-invoked tool flows. No telemetry, no update checks, no CDN fetches. Telemetry — if ever added — must be preference-gated, default-off when the preference is unknown, exempt hostile contexts, with a hard env kill-switch (gh `NoOpService` pattern). Enforce by construction (no client constructors outside GO-HTTP-01) and review the dependency tree for dialers.
- Why: A security-sensitive local tool that phones home is a trust-ending event; agent environments are often offline. Several exemplars (fzf, rclone, restic) ship zero telemetry.
- Enforce: review-only + `test` (hermetic `go test` with `HTTP_PROXY` unset must pass; optional firewall CI job)
- Verified: no (source-only — architectural rule)
- Sources: cli/cli `internal/ghcmd/cmd.go`, `internal/gh/ghtelemetry/telemetry.go` (pattern)

---

## Hashing and crypto

### GO-CRYPTO-01 — **MUST** — Stream hashes; compare secrets in constant time

- from D-CRYPTO-01, D-CRYPTO-02
- Rule: `io.Copy(hasher, file)` for SHA-256 of large checkouts/artifacts — constant memory regardless of repo size. Token/MAC comparisons via `subtle.ConstantTimeCompare` (equal-length inputs; hash-then-compare if lengths can differ, since it leaks length).
- Why: Slurping files to hash them OOMs on large repos; early-exit `==` leaks prefix information byte-by-byte over timing. Verified: byte-exact digest on fixture; both compare arms.
- Enforce: `test` (golden digest) + custom lint candidate (flag `==` on `[]byte` secrets)
- Verified: yes (exp-hash; `skills/go-cli-hardening/snippets/hash_stream/`)
- Sources: https://pkg.go.dev/crypto/sha256, https://pkg.go.dev/crypto/subtle, https://pkg.go.dev/io#Copy

### GO-CRYPTO-02 — **MUST** — No SHA-1 except confined to one wire-compat package

- from D-CRYPTO-03
- Rule: Use SHA-256 for all new hashing. SHA-1 (or any other weak hash kept for wire compatibility, e.g. verdict finding signatures at sig_version "1") is confined to exactly one named package (e.g. `internal/legacyhash` or `internal/sig`) that exists only for wire compatibility: the package carries a doc comment stating it is not a security boundary, the depguard `crypto/sha1` ban keeps firing everywhere else via a scoped file exception for that one package path (the adopting repo's `.golangci.yml` carries the commented pattern), and the FIPS stance is explicit — under `GODEBUG=fips140=only`, `sha1.New()` fails at runtime (`crypto/sha1: use of SHA-1 is not allowed in FIPS 140-only mode`, verified) while SHA-256 is unaffected, so either document FIPS-only as unsupported or version the format (e.g. sig_version 2 on SHA-256) with a migration. See README open decision O1 for the verdict call.
- Why: FIPS mode (Go 1.24+) turns a hash choice into a runtime crash on locked-down hosts; unconfined legacy hashes spread by copy-paste into new code.
- Enforce: `test` (run suite once with `GODEBUG=fips140=only`) + `lint:depguard` (deny `crypto/sha1`, one scoped wire-compat package exception) + review-only (confinement + doc comment)
- Since: Go 1.24 (`GODEBUG fips140=only`, `crypto/fips140.Enabled()`)
- Verified: yes, both modes (exp-hash); depguard exception proved file-scoped (lint validation evidence, run 6)
- Sources: https://pkg.go.dev/crypto/fips140, https://go.dev/doc/go1.24

---

## Cross-platform

### GO-XPLAT-01 — **MUST** — Test on Windows in CI, not just cross-compile

- from D-XPLAT-04
- Rule: `GOOS=windows go build` proves syntax, not semantics (rename limits, path separators, job objects, CRLF, console APIs). Run the unit suite on a Windows runner; keep OS-dependent tests hermetic (no `sleep`, `pgrep`, or signal assumptions).
- Why: Every Windows-source-only card above is a live risk until a Windows runner executes it.
- Enforce: `ci` (Windows leg in the test matrix)
- Verified: no (source-only — process recommendation)
- Sources: n/a

### GO-XPLAT-02 — **SHOULD** — Never mix path separators; normalize tool output

- from D-XPLAT-01, D-XPLAT-02
- Rule: Covered normatively by GO-FS-04 (filepath/path split) and GO-FS-03 (build tags). This card records the enforcement pairing: `path.Join`-on-disk lint + CRLF golden tests.
- Why: Separator and line-ending bugs only manifest on Windows.
- Enforce: custom lint + `test` (CRLF goldens) + Windows CI
- Verified: yes (exp-xplat)
- Sources: https://pkg.go.dev/path/filepath

### GO-XPLAT-03 — **CONSIDER** — Pin GOMAXPROCS only for pinned workloads; run go fix for modernizers

- from A-PERFORMANCE-04, A-PERFORMANCE-06
- Rule: Leave `GOMAXPROCS` unset (cgroup-aware since 1.25, refreshed live); set it explicitly only for thread-per-core workloads, and document why. Run `go fix` (revamped 1.26: dozens of modernizers + `//go:fix inline`) and review the diff instead of modernizing by hand.
- Why: Manual values over-commit in CPU-limited containers; hand modernization misses sites and introduces typos.
- Enforce: review-only (`go fix ./...` in a cleanup CL)
- Since: Go 1.25 (container-aware GOMAXPROCS); Go 1.26 (revamped go fix)
- Sources: https://go.dev/doc/go1.25, https://go.dev/doc/go1.26

---

## Lint and format config

### GO-LINT-01 — **MUST** — Start from default:none-equivalent and enable explicitly

- from B-LINT-01, B-LINT-02, C-LINT-01, C-LINT-02
- Rule: `linters.default: none`-equivalent discipline (the final config uses `default: standard` + curated `enable:` + `disable: []`; see Decisions) with every addition a deliberate, reviewable decision. The portable core every repo starts with: govet + revive + misspell + unconvert + unused; then `staticcheck`/`errcheck`, then domain rules. `default: all` (≈115 linters) is forbidden — it enables domain linters that false-positive on any CLI.
- Why: Default sets change between golangci-lint releases and break builds overnight; explicit lists are stable and self-documenting; govet is the only universal linter (11/11 repos).
- Enforce: `lint` (config discipline) + `ci`
- Verified: yes (`golangci-lint config verify` PASS v2.15.0)
- Sources: https://golangci-lint.run/docs/configuration/file/

### GO-LINT-02 — **MUST** — Format with gofumpt + gci; never goimports alongside gci

- from B-LINT-04, C-LINT-13
- Rule: `formatters.enable: [gofumpt, gci]`; gci sections `[standard, default, prefix(MODULE_PATH)]`, `custom-order: true`. (Resolves the Google-4-groups vs Uber-2-groups disagreement: 3 gci sections — stdlib, default third-party, module prefix — the prometheus-backed grouping; see Decisions.)
- Why: gofumpt is strictly-gofmt-compatible; gci owns import grouping (goimports alongside = conflicting rewrites); formatting fights end forever.
- Enforce: `lint` (formatters section) + `ci`; `make fmt`
- Verified: yes (both fired on the C sample)
- Sources: local run `golangci-lint fmt`

### GO-LINT-03 — **SHOULD** — Gate test quality, error style, and complexity together

- from B-LINT-03, B-ERR-05, C-LINT-05, C-LINT-09, C-LINT-15
- Rule: Enable `errorlint` + `nilerr` + `errcheck` + revive `error-naming/error-strings` (never revive `errorf` — errorlint owns it); `testifylint` (enable-all) + `thelper` + `usetesting`; `cyclop` (12) + `gocognit` (15); `godot` (toplevel+period) + `godoclint` (all) + `misspell` + `dupword` + `goconst` (ignore-tests); `sloglint` (no-global all, static-msg); `tagalign` + `tagliatelle` (json+yaml camel) + `musttag`. `nolintlint` requires explanation AND a specific linter, `allow-unused: false`.
- Why: `errcheck` alone still allows `%v`-swallowed chains and user-hostile strings; line/duplication linters (`dupl`, `lll`, `funlen`, `mnd`, `wsl`, `nlreturn`) punish table tests and CLI help text, so complexity gates stay at function level only.
- Enforce: `lint`
- Verified: yes (errcheck 14, errorlint 3, wrapcheck 1, nilerr 2, revive 23 on the C sample)
- Sources: https://revive.run/r/, https://github.com/tomarrell/wrapcheck

---

## CI and release

### GO-CI-01 — **MUST** — Matrix OS × Go, keep -race green, fail on new lint issues

- from B-CI-01, B-CI-03, C-CI-01, C-TEST-01
- Rule: Test linux/macos/windows on every PR: `go test -race -shuffle=on -count=1 ./...`. Lint from the repo's pinned config (`max-issues-per-linter: 0, max-same-issues: 0`); pin the linter version. `-count=1` defeats result caching that hides flakes.
- Why: File-path, permission, and locking bugs only appear on the other two OSes; unpinned linters turn green mains red overnight; zero-tolerance on new issues keeps the ratchet tight.
- Enforce: `ci`
- Sources: cli/cli `.github/workflows/go.yml`, `.golangci.yml`

### GO-CI-02 — **MUST** — govulncheck is a blocking workflow; automate dependency updates

- from B-CI-02, B-CI-04, C-SEC-04
- Rule: Run `go tool govulncheck ./...` (tool-directive pinned) on every PR and weekly; SARIF-upload results; pin the version. Enable dependabot for go modules + Actions with a scheduled triage workflow. Weekly OpenSSF Scorecard as advisory signal, never a gate.
- Why: Stdlib vulns (http, crypto, archive) are the highest-leverage fixes for a CLI; stale deps are how CVEs linger.
- Enforce: `ci`
- Sources: cli/cli `.github/workflows/govulncheck.yml`, https://github.com/ossf/scorecard-action (v2.5.0)

### GO-CI-03 — **MUST** — One workflow: lint, vet, test-matrix, vuln, fuzz, build, tag-release

- from C-CI-01, C-SEC-01, C-BLD-01
- Rule: Jobs — lint (golangci action v9.3.0 + golangci-lint v2.15.0), actionlint (v1.7.12), vet (`go vet` + `go mod verify` + tidy-diff + gofmt check), test (3-OS, race/shuffle), govulncheck (exit-3 gate), fuzz (30 s/target loop), build (GOOS/GOARCH matrix, `CGO_ENABLED=0`, `-trimpath`, ldflags), release (tag-gated, needs all). Top-level `permissions: contents: read`; release escalates to `contents:write` + `id-token:write` + `attestations:write`. Every third-party action SHA-pinned with the tag in a trailing comment. `GOFLAGS=-mod=readonly` in CI env; `go mod verify` must pass.
- Why: Every tooling section maps to exactly one job; minimal permissions per job; the release cannot run green unless everything else is green; tag refs are mutable, SHA pins make supply-chain attacks visible as diffs.
- Enforce: `ci`
- Verified: yes (actionlint v1.7.12 exit 0, no findings; `make -n check` prints the exact CI commands)
- Sources: the adopting repo's `.github/workflows/ci.yml`

### GO-CI-04 — **MUST** — Own the release matrix in goreleaser, sign everything

- from B-REL-01, B-REL-03, B-REL-05, C-BLD-02, C-BLD-04
- Rule: Declare `builds` (linux/darwin/windows × amd64/arm64), archives (+zip for windows), source tarball, `sboms:` via syft, `checksum:`, cosign keyless `sign-blob --bundle` on the checksum (Sigstore via CI OIDC), GitHub attestations with `subject-checksums: ./dist/checksums.txt`. `version: 2` config, `prerelease: auto`. Create releases as drafts only if slow artifacts (MSI, notarization) need it; otherwise publish after all artifacts land.
- Why: One tool owns archives, checksums, SBOMs, signatures; users fetch within seconds of a release event — a half-populated page ships broken installs.
- Enforce: `ci` (release job); `make snapshot`
- Verified: yes (`goreleaser check` passes v2.19.0; `release --snapshot` built all 6 platform archives + checksums.txt + artifacts.json)
- Sources: https://goreleaser.com/customization/builds/ (v2.19.0)

### GO-CI-05 — **SHOULD** — Publish to package managers from the same release

- from B-REL-04, C-BLD-03, C-BLD-05
- Rule: Generate Homebrew formulae/casks, Scoop manifests, winget packages, nfpm (deb/rpm/apk) in-release so versions never skew. Use `homebrew_casks:` (NOT `brews:` — hard-deprecated, fails `goreleaser check`); `winget:` singular (NOT `wingets:` — fails schema); `scoops:` plural. No `npms:` — npm publishing is GoReleaser PRO-only (since v2.8).
- Why: "brew is two versions behind GitHub releases" is a self-inflicted support queue.
- Enforce: `ci` (release job)
- Verified: yes (both wrong-key failures found and fixed live during validation)
- Sources: https://goreleaser.com/deprecations#brews, https://goreleaser.com/blog/goreleaser-v2.8/

### GO-CI-06 — **MUST** — Makefile mirrors CI 1:1; gopls mirrors the lint (advisory)

- from C-DEV-01, C-DEV-02, B-GOV-03
- Rule: `build lint fmt vet test race cover fuzz vuln sec bench snapshot tools check clean` — `check` = everything CI gates on except release; contributors reproduce any CI failure with one local command. Editor `vscode-settings.json` (gopls: gofumpt formatting, staticcheck + fillreturns/nonewvars/unusedparams analyses, vulncheck on imports, `-race -shuffle=on -count=1` test flags) is advisory — golangci-lint remains the gate. Keep a short machine-facing `AGENTS.md` (build/test/lint commands, repo map, contribution rules) next to human `CONTRIBUTING.md`.
- Why: Instant editor feedback on the same rules CI enforces; a 50-line pointer file beats a 5,000-line README dump into agent context.
- Enforce: `make`; editor setup (advisory)
- Verified: yes (`make -n check` validated; vscode-settings.json valid JSONC)
- Sources: cli/cli `AGENTS.md`, google/go-github `AGENTS.md` (pattern)

### GO-CI-07 — **CONSIDER** — Document threat model + incident response for distribution tooling

- from B-GOV-01, B-GOV-02
- Rule: Ship `CONTRIBUTING.md` + `SECURITY.md` + `.github/CODEOWNERS`. If releases run on user machines (package managers, install scripts, update checks), additionally publish `THREAT_MODEL.md` and `INCIDENT_RESPONSE.md`.
- Why: Missing any of the three routes contributors, reporters, or reviews into a black hole; threat docs force explicit answers to "what if the release key leaks" before it happens.
- Enforce: review-only
- Sources: goreleaser repo root (`THREAT_MODEL.md`, `INCIDENT_RESPONSE.md` present at HEAD)

---

## Decisions (conflicts resolved)

Each row: the conflict, the decision, one-line rationale.

1. **Import grouping (Google 4 groups vs Uber 2 vs gci config)** → gci with 3 sections `[standard, default, prefix(MODULE_PATH)]`, `custom-order: true`. Rationale: executable grouping both guides describe, backed by the prometheus config the survey verified.
2. **Assertion libraries vs std testing** → std `testing` primary; `testify` allowed but governed by `testifylint` (enable-all). Rationale: std-only matches the zero-dep verdict port; testifylint removes the assertion-inconsistency cost where testify is already in use.
3. **Functional options vs option structs** → functional options (`WithTimeout`-style) for public APIs with ≥2 optional knobs (GO-HTTP-01 pattern); plain structs/params below that. Rationale: matches go-github's verified `WithTimeout` seam and keeps test injection (`NewClientFromHTTP`) trivial.
4. **errcheck absent in 6/11 top repos while C enables it** → ENABLED (`check-type-assertions`, `check-blank`). Rationale: the 6/11 rely on review discipline at scale; a new security-sensitive CLI has no such reviewer bench, and the C sample proved 14 live finds.
5. **wrapcheck yes/no** → YES with defaults (`report-internal-errors: false`). Rationale: cross-package unwrapped boundaries are where verdict loses context (CLI ↔ store ↔ HTTP); same-package noise stays off.
6. **cobra vs kong vs std flag for a ~20-subcommand CLI** → std `flag` + tiny dispatcher NOW (zero third-party deps per the port spec), graduate to cobra when subcommand/man-page/completion needs justify a dependency. Rationale: B's cobra consensus assumes deps are free; the port spec's story 13 makes them costed — cobra buys familiarity, kong buys struct-tag parsing, neither buys enough today.
7. **%w everywhere (B-ERR-01) vs deliberate %w (A-ERRORS-03)** → deliberate: `%w` only with a documented/tested inspection contract, `%v` at trust/storage boundaries. Rationale: B's always-wrap is CLI-layer context-adding; A's restraint governs what crosses a package API, where wrapping is a semver commitment.
8. **tparallel/paralleltest ON (B) vs OFF (C draft)** → OFF (`testpackage`, `paralleltest`, `tparallel` all disabled). Rationale: they punish intentionally-serial suites and testscript-driven CLI tests; GO-TEST-03 keeps parallelism a deliberate per-test choice.
9. **prealloc / exhaustruct / funlen / dupl / lll / mnd / wsl ON?** → all OFF. Rationale: they punish table tests, CLI help text, and external structs (cobra.Command) — verified evaluation in C-LINT-15/21.
10. **gochecknoglobals / gochecknoinits ON?** → OFF. Rationale: version vars (`main.version` ldflags) and cobra init patterns are legitimate.
11. **nilaway as built-in?** → NO; deferred as a custom-build plugin (`.custom-gcl.yml`) until nil-panics justify tripling CI build complexity. Rationale: upstream rejects it as built-in (false-positive policy, issue #4045).
12. **shadow analyzer ON?** → OFF. Rationale: false-positives on idiomatic `if err := ...; err != nil` train engineers to ignore vet.
13. **exhaustruct / ireturn / iface / decorder / godox dogma** → OFF. Rationale: interface-prescription and file-layout bikeshed linters; boundary discipline (GO-CONTEXT-04, GO-IFACE-01) is enforced by review instead.
14. **golangci `default: standard` vs `default: none`** → `standard` + explicit `enable:` + `disable: []` (B's `none` discipline, C's forward-compatible base). Rationale: keeps future upstream default additions while the explicit enable list stays the auditable strict surface; `disable: []` must be an explicit list — a comment-only key parses as null and fails schema verify.
15. **json/v2 now (A-PERFORMANCE-05) vs not yet (D-JSON-05)** → NOT YET (GO-JSON-05). Rationale: D's `GOEXPERIMENT=nojsonv2` negative control proves the API is not frozen in 1.27; A-PERFORMANCE-05's engine claim is about stdlib `encoding/json` internals, which the KB already rides without importing the gated packages.
16. **Strict JSON vs unknown-field passthrough (verdict stores)** → BOTH, layered: strict+capped at every untrusted entry point (GO-JSON-01), RawMessage-map preservation for read-modify-write store cycles (GO-JSON-02). Rationale: entry strictness rejects adversarial shapes; store preservation keeps forward-compat across versions — no single mode serves both.
17. **testscript vs the bash eval harness** → testscript (`testdata/*.txtar`, `Params.Cmds`, `cmp` goldens, `-u` refresh) as the successor. Rationale: hermetic, Windows-compatible, reviewable; bash harnesses rot on quoting/OS differences (B-TEST-03, C-TEST-04, spec testing decision).
18. **Single vs multi-module (etcd-style)** → single module; split only for independently-versioned APIs with `replace` directives. Rationale: tailscale/rclone prove single-module scales; multi-module raises the contribution bar without verdict-scale justification.
19. **Telemetry in a dev CLI** → NONE (GO-OBS-03), with the gh preference-gated pattern on file if policy ever changes. Rationale: silent telemetry in a security-sensitive agent-run tool is trust-ending; fzf/rclone/restic prove zero-telemetry is viable.
20. **One logger vs many** → `log/slog` only; `logrus`/`apex/log`/`pkg/errors` denied via depguard; `math/rand` → `math/rand/v2`, `ioutil` → `io/os`, `sort` → `slices`, `reflect.DeepEqual` → `cmp.Equal`. Rationale: mixed loggers produce mixed formats; the ban is the only thing that holds the line (golangci/goreleaser/prometheus depguard pattern).
21. **Release signing scope** → SBOMs per artifact + cosign keyless `sign-blob --bundle` on checksums.txt + GitHub attestations. Rationale: keyless removes long-lived secrets; provenance verifies through one checksum file.
22. **Govulncheck advisory vs blocking** → BLOCKING on reachable symbols (exit 3 = fail), PR + push + weekly schedule. Rationale: reachability cuts module-graph noise so blocking is cheap; SARIF upload keeps the audit trail.
23. **SHA-1 ban vs legacy wire signatures (e.g. verdict sig_version "1")** → CONFINED, not banned-absolutely: the general depguard `crypto/sha1` ban stays for all new code; exactly one named wire-compat package gets a scoped file exception, a doc comment stating it is not a security boundary, and an explicit FIPS stance (FIPS-only unsupported, or sig_version 2 on SHA-256 with migration). Rationale: wire compatibility is a correctness constraint — recomputing old signatures with a new hash breaks every stored finding — while unconfined SHA-1 spreads by copy-paste and crashes under `fips140=only`.
24. **tagliatelle camelCase vs existing mixed wire formats** → EXISTING FORMATS WIN: tag-style linters apply to new types only; legacy wire types are excluded via path/linter exclusions (or tagliatelle is disabled when the product's formats are mixed). Rationale: renaming a stored/transmitted field to satisfy a linter breaks every existing store and bundle; additive-only changelogs forbid renames.

## Open items (NOT decided — these are the research's open questions, not repo TODOs)

- O1. SHA-1 finding signatures vs `fips140=only` (sig_version "2" on SHA-256 + migration?).
- O2. CLI framework choice for 23 verbs (std flag now vs cobra later — threshold unpinned).
- O3. Byte-identical JSON key order vs semantic equivalence for stored artifacts.
- O4. GitHub I/O: shell out to `gh` (auth model unchanged) vs native HTTP in the binary.
- O5. Skill-install binary distribution (registry per-platform optionals vs bundling vs build-from-source).

---

## Appendix: source-ID map

A: A-NAMING-01…08, A-PACKAGE-01…09, A-ERRORS-01…08, A-CONTEXT-01…04, A-CONCURRENCY-01…07,
A-INTERFACES-01…05, A-ZERO-01…04, A-GENERICS-01…05, A-SLICEMAP-01…06, A-DEFER-01…04,
A-LOGGING-01…05, A-TESTING-01…09, A-DOCS-01…04, A-API-01…06, A-MODULES-01…05,
A-PERFORMANCE-01…06, A-SECURITY-01…05.
B: B-LAYOUT-01…05, B-CLI-01…06, B-ERR-01…05, B-EXEC-01…05, B-CONFIG-01…04,
B-TEST-01…06, B-HTTP-01…04, B-IO-01…04, B-LOG-01…03, B-REL-01…06, B-CI-01…04,
B-LINT-01…04, B-DEP-01…03, B-GOV-01…03 (+ linter-frequency table).
C: C-LINT-01…18, C-SEC-01…06, C-TEST-01…06, C-BLD-01…06, C-CI-01…02, C-DEV-01…02.
D: D-EXEC-01…07, D-JSON-01…06, D-ATOMIC-01…04, D-FS-01…04, D-HTTP-01…05,
D-CLI-01…06, D-CRYPTO-01…03, D-XPLAT-01…04, D-EMBED-01…02, D-OBS-01…03.

Dropped as duplicates/filler: A-PACKAGE-07 (folded into GO-LAYOUT-05), A-LOGGING-05's per-layer
half (folded into GO-ERRORS-06/GO-OBS-02), B-EXEC-05's kill-tree half (folded into GO-EXEC-01),
B-IO-01/03 (folded into GO-ATOMIC-03), B-LINT-02/03 (folded into GO-LINT-01/03),
D-JSON-06 determinism half (folded into GO-JSON-03), D-XPLAT-01/02 (folded into GO-FS-04/GO-XPLAT-02).
