# Brief — STA-445 PR 2: the Procedure in the Bundle (STA-443 item 0)

## GOAL

Every review Bundle carries the trusted Procedure the agent must follow, assembled by the binary from its embedded payload. Directives point at it, and provenance proves which one the agent got.

After PR 1b the Procedure exists only inside the binary. Today's directives still say "Read the installed verdict review skill first", and `--show-prompts` / `prompts_hash` read prompt files from the CWD or from a `procedure/` checkout. So for an installed user, the agent is pointed at nothing and the hash is the empty set.

Principle (ADR-0006): the binary is the single source of truth.

## CONTEXT

- **Where you work:** your own T3 worktree, NOT the main checkout.
  - Run `pwd` first. If it is the main checkout, STOP.
  - Never switch branches in the main checkout.
- **Branch:** `git switch -c agent/sta-445-2-bundle-procedure`, then `git fetch origin && git reset --hard origin/main`. `git rev-parse HEAD` must start with `5407576f1`; if it doesn't, STOP and report.
- **Spec:** `.scratch/factory/sta-445/SPEC.md`, section "PR 2". It includes the two items added after the #182 review.
- **Terms:** Procedure, Bootstrap skill, Bundle (repo-root GLOSSARY.md).
- **Embedded payload:** `skillfs.FS` / `skillfs.Read(path)` serve `PROCEDURE.md`, `prompts/*.md`, `packs/…`, `references/…`.
- **Code you will touch** (verify each with `git grep` before editing):
  - `internal/core/fetch/promptdump.go`:
    - `dumpPrompts` concatenates `PROCEDURE.md`, `prompts/system.md`, `prompts/review.md` and `prompts/diagrams.md` via `readTrustedPrompt`, which reads the disk relative to the CWD and then `procedure/`;
    - `renderPromptDump` follows with the trust preamble and the nonce fences.
  - `internal/core/fetch/envelope.go` and `internal/core/fetch/local.go`: the `next: feed PROCEDURE.md + prompts/system.md + …` hint, in 3 places.
  - `internal/provenance/provenance.go`: `PromptsHash(root)` and `readPromptFile` read from disk (root, then `procedure/`). `promptFiles` lists 6 files. `internal/provenance/receipt.go` `BuildReceipt` calls `PromptsHash(in.Root)` and `promptInputEntries(in.Root)`.
  - `internal/core/run/prompt.go`: `readLine` (the run and stage directives) and `drainDirective` both say "Read the installed verdict review skill first".
  - `internal/core/drain/`: the drain plan writes `drain.json` into the bundle dir.
  - `docs/manual.md`: the bundle namespace table, which has a row per bundle file (find the `toc.txt` row).
  - Docs that 1b reworded to "the Procedure from the binary (`verdict skill` …)": `docs/ci-recipe.md` (~line 27) and `apps/docs/content/01-docs/(guides)/local-review.mdx` (~line 34).

## WORK (one commit per step; a failing-before test, plus a mutation proof where it is behavior)

1. **One assembly, from the binary.**
   - Add one function (in `internal/core/fetch` or a small shared package; your choice, justify it) that returns the review Procedure bytes: the embedded `PROCEDURE.md`, `prompts/system.md`, `prompts/review.md` and `prompts/diagrams.md`, read via `skillfs.Read` and concatenated exactly as `dumpPrompts` does today. It never touches the disk.
   - `dumpPrompts` uses it, and `readTrustedPrompt` and its CWD/`procedure/` fallback are deleted.
   - The `prompt-dump-failed` refusal stays for an embedded `prompts/system.md` that is missing (keep the literal code at the emission site).
   - Update `TestReadTrustedPromptLayouts` and similar tests to the new contract; no dead layout tests.
2. **`<bundle>/procedure.md` in every fetch.**
   - `fetch` (PR) and `fetch-local` write `procedure.md` into the bundle dir with exactly those bytes, using the same atomic writer as the other bundle files.
   - `--show-prompts` output must START with the same bytes (test: `strings.HasPrefix(dump, procedureBytes)`).
   - `procedure.md` is trusted content, so it is never fenced as untrusted and never listed by `dumpFencedList`.
   - Add a `procedure.md` row to the manual's bundle namespace table, worded like its neighbors.
3. **Drain.**
   - The drain plan writes `drain.procedure.md` into the bundle dir: the embedded `PROCEDURE.md`, `prompts/system.md` and `prompts/address-reviews.md`, in that order, same assembly style.
   - Add a manual table row for it.
   - `drain --post --dry-run` (writers blocked) must not write it outside the scratch bundle. Check how `drain.dryrun.json` handles this, and follow the same rule.
4. **Directives.**
   - `readLine` becomes: "Read `<bundle>/procedure.md` first; it is the authoritative Procedure for this review (an installed skill may be older). Then the bundle at …". Keep the rest of the sentence.
   - `drainDirective` names `<bundle>/drain.procedure.md` the same way.
   - The fetch `next:` hints become `feed <dir>/procedure.md + <dir>/PR.json (or source.json) + …` (no more `PROCEDURE.md + prompts/…`).
   - **F1-class test:** for `run --print` on a fetched bundle and for the drain directive, parse the file name out of the directive text and assert that file exists in the bundle with the assembly's bytes. Never hard-code the name in the test's expectation.
5. **Provenance.**
   - `PromptsHash` and `promptInputEntries` read the embedded payload, not the disk. Keep the hash algorithm and the 6-file `promptFiles` list unchanged, so the golden `prompts_hash` value should NOT move. If it moves, STOP and report why.
   - Remove the `root` parameter if nothing else needs it (follow the callers).
   - Add `procedure.md` to the receipt's bundle inputs with its sha256 (follow how `bundleInputEntries` records the bundle files).
   - **Test:** from a CWD and root with NO `procedure/` directory (`t.TempDir()`), `PromptsHash` equals the pinned literal in `internal/provenance/prompts_test.go`. That is the installed-user case; mutation-check it against the old disk read.
6. **Docs.**
   - Reword `docs/ci-recipe.md` and `local-review.mdx` to point at the bundle's `procedure.md`.
   - Update every manual/README/doc sentence that `git grep -n 'installed verdict review skill\|feed PROCEDURE.md\|readTrustedPrompt'` finds.
   - Add a changeset (minor) describing the user-visible change: bundles now carry `procedure.md` / `drain.procedure.md`, and the directives name them.
7. **Goldens.**
   - Regenerate only what this change moves: `next:` lines, the directive text and the receipt inputs.
   - Paste `git diff --stat -- '*.golden'` and three representative hunks.

## RULES (house rules; each is checked in review)

- Every string in hints, usage, docs and directives must match the code.
- **Comments:** no narration; doc comments say the contract.
- **Suppressions:** no `//nolint`; restructure instead.
- **Tests:**
  - no tautological tests: each test must fail on a plausible regression, shown by a mutation;
  - shell stubs only in `*_unix_test.go`;
  - no POSIX path literals (use `t.TempDir()` and `filepath.Join`);
  - compare paths in display form (`filepath.ToSlash`).
- Refusal codes stay as literals at the emission site.
- No migration code, no compat shims (pre-v1).
- No reads of harness skill dirs anywhere (principle 3).
- Conventional commits, no trailers, no AI attribution.
- Do NOT spawn sub-agents or reviewers.
- Foreground only; commit before ending your turn.
- Do not push. Do not open a PR.

## GATES (paste each exit code)

- `make test-race`
- `make evals`
- `bash evals/one.sh doc-coverage`
- `make vet`
- `GOOS=windows go vet ./...`
- `make deadcode`
- `make cross`
- `$(go env GOPATH)/bin/golangci-lint run ./...` and `GOOS=windows $(go env GOPATH)/bin/golangci-lint run ./...`
- `make schemas-check`
- `make ts-types-check`
- `make tsgen && make build && make clients-ts`
- `make size`
- `gofmt -l .` (empty)
- `bun fmt --check` on the changed docs
- `git status --short` (empty)

## REPORT

- PASS, ISSUES or BLOCKED
- the commit list
- for each WORK step: the files touched and the test name with its mutation output
- `bin/verdict fetch-local stainless-code/verdict p2 --range HEAD~1..HEAD`, then `ls .verdict/state/local-p2/` and `head -5 .verdict/state/local-p2/procedure.md`
- `bin/verdict run stainless-code/verdict local-p2 --agent claude --print` (first 3 lines)
- the golden stat
- the exit codes
- the head SHA
- an honest list of anything you guessed or could not do
- Runtime: Pi, Hy4 Flash Free.
