# House rules for the verdict repository

The shipped prompts stay repo-agnostic so reviews of other projects carry no
verdict conventions. Repo-specific rules live here, where any agent working in
this checkout reads them.

**Budget:** 85 lines, hard ceiling. Anything longer than a rule belongs in
`docs/`: Go rules in `docs/go-rules.md`, the operator contract in `docs/manual.md`.

## Verification ladder

Iterate on the one rung you touched, then climb the whole ladder before
claiming done. Each rung is a command CI runs.

1. **Build** — `make build` writes `bin/verdict`, the binary the evals invoke.
2. **Vet** — `make vet` (`go vet ./...`, then `go mod verify`), `make cross` for
   every shipped `GOOS/GOARCH` (`release/contract.json`), and `make size`: the
   byte ratchet (`scripts/size-baseline.txt`) moves with the growth.
3. **Unit** — `go test ./internal/<pkg>/...` while iterating; `make test-race`
   (`go test -race -shuffle=on -count=1 ./...`) before done.
4. **Goldens** — files under `testdata/` are the contract. Regenerate with
   `go test ./<pkg>/ -update` (`internal/golden` and `internal/core/verify`
   register the flag), then read the diff: an unexplained golden is a red flag.
5. **Evals** — `make evals` builds `bin/verdict` and runs `evals/run.sh`;
   `./evals/one.sh <case>` grades one case.
6. **Schemas** — `make schemas-check` (`cmd/schemagen` drift; part of
   `make ci-go`).

## Go rules

`docs/go-rules.md` carries the adopted Go rules: one line per rule with its
`GO-<TOPIC>-<NN>` KB ID and an honest enforcement marker (gated versus
review-only), plus the accepted deviations; `docs/ci-and-tests.md` holds the
skip-token ban and test determinism. Read both before writing or reviewing Go.

## Hazards

- **Never run a real `post`.** It submits to GitHub or GitLab, so stub that
  host's CLI (`gh`, or `glab` for GitLab) and use `post --dry-run`.
- **No network.** Unit tests and evals must pass offline. The eval suite is
  GitHub-only: it stubs `gh` network-dead and proves zero real calls
  (`evals/README.md`). A GitLab `post --dry-run` is manual — stub `glab` by hand.
- **No `/tmp` test dirs.** Use `t.TempDir()` for files and `internal/testenv`
  for child processes; a hardcoded path leaks across parallel runs.
- **Exactly four skill files are hashed into goldens.** `skills/verdict/SKILL.md`,
  `prompts/system.md`, `prompts/review.md`, `prompts/diagrams.md` (the rest of
  `prompts/` is not). `record` stores a `prompts_hash`, so an edit there must
  move only those lines — prove it in the diff.

## Git and commits

- Never push to protected branches (`main`, `master`, `production`, `develop`,
  `staging`); work on `agent/<issue>-<kebab>`.
- Never add `Co-authored-by` trailers.
- One commit per change; leave unrelated files untouched.

## Tests

- A test that cannot fail is worse than no test: it costs a reader's trust and
  passes forever. Delete it, or compare against a literal, then prove the
  assertion fires by mutating the code under it — observed failing, not argued.
- An expectation rebuilt from the code under test is not an expectation.
  `Files()` plus flatten to check `Parse`, `filepath.Join` over the same
  constants to check a path getter, a `want` from the same call as the `got`:
  a defect reproduces on both sides.
- Prefer a literal for anything a user or another tool consumes — paths, row
  text, argv — over deriving it from the constants the implementation reads.
- A guard on a count or flag that no reachable path can move is decoration. If
  the property is structural (a function takes no executor, a getter has one
  body), the signature is the guard; say so in prose instead of asserting it.

## Formatting

- Before commit or push, run `bun fmt --check` on changed files when Bun is
  available; fix only non-example hunks.
- Never reflow `skills/verdict/prompts/` examples — see `CONTRIBUTING.md` § Formatting.
- `gofmt -l .` must be empty, and shell files gate on `shellcheck -S warning`
  (see `hooks/pre-commit`).
- `golangci-lint run ./...` must report 0 issues under `.golangci.yml`; no new
  `//nolint` without a specific reason.

## Prompt style

- Use emdashes for sentence breaks.
- Use backticks liberally for inline code, even in headers.
