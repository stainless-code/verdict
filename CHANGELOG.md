# @stainless-code/verdict

## 0.4.0

### Minor Changes

- 21ac180: SHA-finalizer and APPROVE-path correctness. A second `post` on the same reviewed SHA is refused naming the exact `fetch --base-sha` advance; post-time drift and the `report --check` gate name the same advance, and a binding APPROVE on a known-stale fetch downgrades to COMMENT. `report --check` gains a live-thread dimension: unknown-to-store open threads fail the gate, unreachable lookups warn and stay neutral.
- 21ac180: Chunked split-range reviews. Over-cap diffs machine-slice on file boundaries (`--chunk` / `.review/chunk` opt-in) with a versioned manifest; the Review contract reads per-chunk and synthesizes one verdict with a summed Basis line; `post`/`record` refuse attested-but-partial coverage and warn loudly on unattested chunked reviews. Same flow on `fetch-local`.
- 21ac180: Verify hardening. `verify` refuses destructive, networked, credential-reading, and repo-escaping commands with typed errors before execution (fail-closed, over 40 regression shapes); `doctor` warns on excess token scope; the manual gains an opt-in hostile-PR sandbox recipe and documents the string-scan ceiling residuals.

### Patch Changes

- 21ac180: Untrusted-data preamble in the fix flow, verified abort-if-not-introduced pin, machine-modes-never-prompt doctrine, and a combined `report --check --completeness` pre-push probe recipe in the manual.

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
