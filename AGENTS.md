# House rules for the verdict repository

The shipped prompts stay repo-agnostic so reviews of other projects carry no
verdict conventions. Repo-specific rules live here, where any agent working in
this checkout reads them.

**Budget:** 70 lines, hard ceiling. Anything longer than a rule belongs in
`docs/` — Go code rules in `docs/go-rules.md`, the operator contract in
`docs/manual.md`.

## Verification ladder

Iterate on the one rung you touched, then climb the whole ladder before
claiming done. Each rung is a command CI runs.

1. **Build** — `make build` writes `bin/verdict`, the binary the evals invoke.
2. **Vet** — `make vet` (`go vet ./...`, then `go mod verify`).
3. **Unit** — `go test ./internal/<pkg>/...` while iterating; `make test-race`
   (`go test -race -count=1 ./...`) before done.
4. **Goldens** — files under `testdata/` are the contract. Regenerate with
   `go test ./<pkg>/ -update` (`internal/golden` and `internal/core/verify`
   register the flag), then read the diff: a golden you cannot explain is a
   red flag, not a green gate.
5. **Evals** — `make evals` builds `bin/verdict` and runs `evals/run.sh`;
   `./evals/one.sh <case>` grades one case.
6. **Schemas** — `make schemas-check` (`cmd/schemagen` drift; part of
   `make ci-go`).

## Go rules

`docs/go-rules.md` carries the adopted Go rules: one line per rule with its
`GO-<TOPIC>-<NN>` KB ID and an honest enforcement marker (gated versus
review-only), plus the accepted deviations. Read it before writing or
reviewing Go here.

## Hazards

- **Never run a real `post`.** It submits a review to GitHub. Exercise the
  path with `post --dry-run` and a stub `gh` first on `PATH`.
- **No network.** Unit tests and evals must pass offline; the eval suite runs
  a network-dead `gh` stub and proves zero real calls (`evals/README.md`).
- **No `/tmp` test dirs.** Use `t.TempDir()` for files and `internal/testenv`
  for child processes; a hardcoded path leaks across parallel runs.
- **`skills/verdict/SKILL.md` and `prompts/` are hashed into goldens.** `record` stores a
  `prompts_hash`, so regenerating after an edit there must move only those
  lines — prove it in the diff.

## Git and commits

- Never push to protected branches (`main`, `master`, `production`, `develop`,
  `staging`); work on `agent/<issue>-<kebab>`.
- Never add `Co-authored-by` trailers.
- One commit per change; leave unrelated files untouched.

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
