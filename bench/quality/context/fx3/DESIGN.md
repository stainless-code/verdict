# Design — STA-453 S6: backtest corpus with known-defect fixtures, deterministic scorer, quality metrics

**Status:** design only, for operator review. No product code changed in this task.
**Branch:** `agent/sta-453-s6` from `origin/main` at `05f131e3d88848dd61d3232280f47b2031633a50`.
**Source of truth:** Linear STA-453 (parent STA-443, decision 11 of "Spec — evidence-checked review handoff (STA-443)"), the shipped `verdict backtest` code, and the read-only factory harness at `~/.t3/worktrees/verdict/<worktree>/.scratch/factory/bench-abc/`.

---

## 1. How backtest works today, and where this fits

### 1.1 The shipped verb

`verdict backtest` is a CLI-only subcommand (no MCP, no SDK — `internal/archtest/surfaces_test.go:37,56`). Its adapter is `internal/backtest/backtest.go`: one `Run` dispatch (`:67`) over five modes — `mine`, `reverts`, `replay`, `score`, and a bare composed form (`:31-35`, `:77-86`). `checkMode` (`:274`) whitelists them; `wanted` (`:57-63`) and `cmd/verdict/usage.go:387-435` publish their flags.

| Mode        | Where it lives                                     | Reads                                            | Writes                                                       | Spend                    |
| ----------- | -------------------------------------------------- | ------------------------------------------------ | ------------------------------------------------------------ | ------------------------ |
| `mine`      | `internal/core/backtest/service.go:210`            | local git objects (SZZ)                          | `.verdict/backtest/<run>/{run.json,mine.json}`               | none                     |
| `reverts`   | `internal/core/backtest/service.go` (`RunReverts`) | local git                                        | `.verdict/reverts.jsonl`, `.verdict/reverts.md`              | none                     |
| `replay`    | `internal/core/backtest/replay.go:237`             | `mine.json` + agent binaries                     | `prs/<NNN>/replay.json`, `.verdict/state/local-bt-<n>-<run>` | **yes** — agent launches |
| `score`     | `internal/core/backtest/score.go:129`              | `mine.json`, resume rows, bundle `findings.json` | `.verdict/backtest/<run>/report.json`                        | none                     |
| `--since …` | `internal/backtest/backtest.go:121`                | calls the three services in order                | union of the above                                           | yes                      |

`score` is a pure read. It grades each replayed PR through `corpuscore.EvaluateDir` (`internal/core/backtest/score.go:369,384`) against the bugs its SZZ candidate recorded (`scoreCase`, `:480`), then writes `report.json` and prints the human table (`humanScore`, `:507`). Its envelope data object and `report.json` are the same `ScoreReport` (`:48`), published as `schemas/backtest.schema.json`; `renderJSON` wraps it in the common envelope (`internal/backtest/backtest.go:225`). The manual describes the whole flow at `docs/manual.md:4294` and the score contract at `docs/manual.md:4629`. Generated backtest state is ignored by the tracked `.verdict/.gitignore` (`internal/core/init/service.go:387`).

### 1.2 The existing corpus format

`bench/corpus/<id>/case.json` + `full.diff`, copied into a run bundle as `corpus.json`. Types: `core.CorpusCase`, `core.CorpusProvenance`, `core.CorpusBug` (`internal/core/corpus.go:8,21,39`); schema `schemas/corpus.schema.json`; loader and validation `internal/core/corpus/corpus.go:93,138,233,247`; rules in `docs/benchmark.md:17-56`.

Matching is **path + line window + defect vocabulary**: `matchBug` (`internal/core/corpus/corpus.go:706`) requires the same path, `|finding.line − bug.line| ≤ line_tolerance` (default 1, `:475`), and a phrase naming `bug.kind` / its category / `bug.keywords` (`:775`). Each bug is consumed once; a second finding on it is a false positive (`:533`). Real cases require 40-hex base/head SHAs and a fix commit (`:186`), and `CheckDiff` (`:303`) refuses a case whose vocabulary its own diff spells.

### 1.3 Decision: sibling corpus, not an extension of `CorpusCase`

The known-defect fixtures do **not** fit the existing case shape:

- Their ground truth is a **description plus regex matchers**, not a defect `kind`, category vocabulary, and anchor line. `validateBug` (`corpus.go:247`) requires `line >= 1` and `kind`, which would be invented data.
- The scorer is **per defect: any finding matches**. `evaluate` (`corpus.go:533`) consumes one finding per bug and counts repeats as false positives. In the factory audit, six findings legitimately hit two defects after the K5 fix (`ablate/matcher-audit.md`); those must remain hits for both defects, not false positives.
- `score.py` matches over `path` + `body` (`score.py:24-36`), with no line window at all. Findings with no line are scoreable there; in the corpus matcher they are `unanchored` false positives (`corpus.go:617`).
- These fixtures are hand-curated known defects, not SZZ candidates. Their base/head are the reviewed range, not a merge-bug hypothesis; the "not citable — SZZ-derived" caveat does not apply.
- Their recorded inputs are the corpus's own minimal `findings` recordings, not `findings.json` in a `corpus-*` bundle.

**Ship a sibling**: `bench/quality/` for fixture data and recordings; `internal/core/quality/` for a pure scorer; a new `backtest quality` mode in the CLI adapter. `schemas/backtest.schema.json` and `bench/corpus/` stay untouched. Shared code is limited to the envelope (`core.RenderEnvelope`), typed refusals, `internal/jq` ordered output, `corpuscore.Rate`/`RateText` (`internal/core/corpus/corpus.go:915,933`) and `readcommon.LoadFindings` for S1-format inputs. A future `STA-325` public benchmark can choose whether to project these fixtures into `CorpusCase`, but that projection would lose the regex semantics, so it is not a prerequisite.

### 1.4 Where the factory harness stays

`fixtures.json`, `score.py`, `calibrate.sh`, `calibration.md`, `ablate/matcher-audit.md` and all A/B/C lane scripts are the _source_ of the port, not product code. They remain in `.scratch` (gitignored, `.gitignore:29`). Only the normalized corpus, scorer, metrics and docs ship, per STA-453 decision 11 and the issue's "harness scripts stay in `.scratch`".

---

## 2. The fixture format

### 2.1 On-disk layout (all tracked)

```
bench/quality/
  fixtures.json                         # catalog: 2 fixtures + calibration rows + signal regexes
  context/
    fx1/SPEC-2.md                       # 5,442 B
    fx1/RESEARCH-2.md                   # 46,568 B (one absolute file:// link sanitized before shipping)
    fx2/SPEC.md                         # 11,966 B
  fx1/recordings/
    vr-sta440b-bench.json                # minimal fixture recording: findings + gate_passed
    vr-sta440b-bench.meta.json           # driver, source bundle, source sha256, refusal, note
    ... five more ...
  fx2/recordings/
    vr-sta351a-haiku.json
    vr-sta351a-haiku.meta.json
```

Operator correction (post-approval): the seven archived reviews were converted once at authoring time into the fixture recording shape above. Each meta sidecar pins its source bundle and the sha256 of the source `review.json`. No product code reads the old `comments`/`file`/`text` shape and no conversion script ships. This replaces the earlier decision to keep a calibration-input compatibility loader.

`context/` is vendored as part of the corpus: live runs need the same spec/research bytes, and a `sha256` + byte count in the manifest detects drift. All four fixture SHAs resolve in `origin/main` history (checked), so `fetch-local --range` works offline from a normal clone.

### 2.2 Schema (new `schemas/quality.schema.json`, catalog shape shown inline)

- `schema_version: "1"`, `fixtures[]`, `calibration[]`, `review_signals`.
- Each fixture: `id`, `label`, `repo`, `base`, `head` (full 40-hex, with the short form in `label`), `context[]` of `{path, sha256, bytes}`, `known[]`, `bonus[]`.
- Each defect (`known` or `bonus`): `id` (unique within fixture), `description`, `matchers` — an array of rules, each rule an array of RE2 patterns. `all-of` within a rule, `any-of` across rules. At least one rule, at least one pattern per rule, no blank pattern. `bonus` never enters the recall denominator; it is reported as a separate union count.
- `calibration[]`: `{recording, fixture, driver, hand[], hand_label}` — the hand scores.
- `review_signals`: `praise_re`, `defect_verb_re`, copied verbatim from `fixtures.json` so the calibration table's praise column is reproducible.

Validation refuses a malformed catalog with `quality-fixture-invalid` naming the file and field; a recording whose `fixture` is not in the catalog refuses `quality-recording-invalid`. No manifest may contain an absolute path: context paths are repo-relative, recording paths are single directory names under `bench/quality/recordings` or an explicitly supplied `--recordings` root.

### 2.3 Fixture fx1 converted (STA-440 part 2, first build)

```json
{
  "schema_version": "1",
  "fixtures": [
    {
      "id": "fx1",
      "label": "STA-440 part 2, first build (291c83ae5..98d8becd9)",
      "repo": "stainless-code/verdict",
      "base": "291c83ae55db44863af842d69619428f1834721a",
      "head": "98d8becd9ebafceadee307eaa88e491db6fb4fd2",
      "context": [
        {
          "path": "bench/quality/context/fx1/SPEC-2.md",
          "sha256": "<computed at generation>",
          "bytes": 5442
        },
        {
          "path": "bench/quality/context/fx1/RESEARCH-2.md",
          "sha256": "<computed at generation>",
          "bytes": 46568
        }
      ],
      "known": [
        {
          "id": "K1",
          "description": "internal/core/run/agents.go kode row at head has no Args; docs use kode -p",
          "matchers": [
            ["\\bkode\\b", "-p\\b|headless|print flag|non-interactive"]
          ]
        },
        {
          "id": "K2",
          "description": "internal/core/run/agents.go neovate row at head has no Args; docs use neovate -q",
          "matchers": [["\\bneovate\\b", "-q\\b|\\bquiet\\b|non-interactive"]]
        },
        {
          "id": "K3",
          "description": "internal/core/run/agents.go qoder row at head has no Args; docs use qoder -p",
          "matchers": [["\\bqoder\\b", "-p\\b|headless|print flag"]]
        },
        {
          "id": "K4",
          "description": "internal/core/run/agents.go qodercn row at head has no Args; docs use qodercn -p",
          "matchers": [["\\bqodercn\\b", "-p\\b|headless|print flag"]]
        },
        {
          "id": "K5",
          "description": "agents.go probe matches are generic: argsRun=run, argsAsk=ask, --print, --prompt, --single",
          "matchers": [
            [
              "ProbeMatch|probe match|match string|match text|argsRun|argsAsk|\"run\"|\"ask\"",
              "generic|common word|too broad|bare|substring|any help|any usage|unrelated|false match|matches .* other"
            ]
          ]
        }
      ],
      "bonus": [
        {
          "id": "B1",
          "description": "internal/core/run/repair.go repairLoop launcher literal has no Probe field",
          "matchers": [
            ["repair", "probe|launcher"],
            ["launcher", "probe"]
          ]
        },
        {
          "id": "B2",
          "description": "internal/core/run/service.go parent pctx.Err() is reported as agent-mismatch",
          "matchers": [
            [
              "cancel|parent context",
              "agent-mismatch|wrong binary|timed out|canceled"
            ]
          ]
        }
      ]
    },
    {
      "id": "fx2",
      "label": "STA-351 PR A (d69957f9c..475310826)",
      "repo": "stainless-code/verdict",
      "base": "d69957f9c4c530fc0821aae4e132b0ab4a0d7320",
      "head": "475310826fca66ee69be389789154a8cd5214915",
      "context": [
        {
          "path": "bench/quality/context/fx2/SPEC.md",
          "sha256": "<computed at generation>",
          "bytes": 11966
        }
      ],
      "known": [
        {
          "id": "D1",
          "description": "internal/core/run/service.go promptArtifact joins reviewFile, not drainFile",
          "matchers": [
            [
              "drain\\.replies\\.json|promptartifact|artifactname",
              "review\\.json|wrong artifact|reviewfile"
            ]
          ]
        },
        {
          "id": "D2",
          "description": "internal/drain/verb.go next and internal/core/run/service.go suggest --post",
          "matchers": [
            ["\\bnext\\b", "--post|post the drafts"],
            ["drain .*--post"]
          ]
        },
        {
          "id": "D3",
          "description": "internal/core/drain/drafts.go validateFields only rejects unknown keys, not missing required keys",
          "matchers": [
            [
              "validatefields",
              "required|missing|refuse|reject|unchecked|absent"
            ]
          ]
        },
        {
          "id": "D4",
          "description": "skills/verdict/schemas/drain.schema.json $defs entries set additionalProperties: true",
          "matchers": [
            [
              "drain\\.schema\\.json|\\$defs",
              "additionalproperties|closed|open"
            ]
          ]
        },
        {
          "id": "D5",
          "description": "evals/cases/drain-plan.sh gh stub matches *api graphql* instead of exact argv",
          "matchers": [["drain-plan\\.sh", "argv|stub|any argv|accept"]]
        },
        {
          "id": "D6",
          "description": "evals/cases/drain-refusals.sh gh stub matches *api graphql* instead of exact argv",
          "matchers": [["drain-refusals\\.sh", "argv|stub|any argv|accept"]]
        }
      ],
      "bonus": []
    }
  ],
  "calibration": [
    {
      "recording": "vr-sta440b-bench",
      "fixture": "fx1",
      "driver": "agy / gemini-4-hydrogen (invalid schema)",
      "hand": [],
      "hand_label": "0/5"
    },
    {
      "recording": "vr-sta440b-bench2",
      "fixture": "fx1",
      "driver": "agy / gemini-4-hydrogen",
      "hand": [],
      "hand_label": "0/5"
    },
    {
      "recording": "vr-sta440b-bench3",
      "fixture": "fx1",
      "driver": "pi / Hy4 Flash Free",
      "hand": ["K2"],
      "hand_label": "1/5"
    },
    {
      "recording": "vr-sta440b-haiku",
      "fixture": "fx1",
      "driver": "claude / haiku-5-5",
      "hand": ["K1", "K2", "K3", "K4", "K5"],
      "hand_label": "5/5"
    },
    {
      "recording": "vr-sta440b-haiku2",
      "fixture": "fx1",
      "driver": "claude / haiku-5-5 repeat",
      "hand": ["K5"],
      "hand_label": "1/5"
    },
    {
      "recording": "vr-sta440b-sonnet",
      "fixture": "fx1",
      "driver": "claude / sonnet-5-5",
      "hand": ["K5"],
      "hand_label": "1/5"
    },
    {
      "recording": "vr-sta351a-haiku",
      "fixture": "fx2",
      "driver": "claude / haiku-5-5",
      "hand": ["D2"],
      "hand_label": "1/6"
    }
  ],
  "review_signals": {
    "praise_re": "correctly|excellent|well[ -](done|integrated|documented|written)|comprehensive|informative|appropriately|nice|good job|looks good",
    "defect_verb_re": "\\b(lacks?|wrong|incorrect|fails?|breaks?|refuses?|returns?|drops?|over-?promises?|cannot|can't|does not|doesn't|unproven|weakness|omits?|bug|defect|mislabels|incomplete|unchecked|generic|bare|should|must|need[s]?|false green)\\b"
  }
}
```

`meta.json` per recording (example):

```json
{
  "schema_version": "1",
  "fixture": "fx1",
  "driver": "agy / gemini-4-hydrogen",
  "source": "review.json",
  "gate_passed": true,
  "gate_source": "gate.json",
  "infra_error": null,
  "refusal_code": null,
  "note": "archived run; gate outcome captured at extraction time"
}
```

`gate_passed: null` means "no gate result" (the invalid-schema `bench` recording had `gate.json` with `data: null` and error `missing-findings`), not "pass". Each `meta.json` pins `source_bundle` and `source_sha256`, so a later file in the same archive cannot silently change the scored input.

### 2.4 Canonical finding and its one input shape

```go
type Finding struct {
    Anchored *bool   // line > 0 in the fixture recording
    Path     string  // recording's path
    Claim    string  // recording's text
    Severity string  // nil/"" is reported as null_severity
    Quote    string  // S4 seam; empty today
    Refusal  string  // "review-insufficient" when S4 emits it
    Line     int     // recording's line, 0 when absent
}
```

A fixture recording is `{"schema_version":"1","findings":[{"path":…,"line":…,"severity":…,"text":…}],"gate_passed":…}`. The seven calibration recordings were converted from their archived bundles once at authoring time; no product code reads the older shape, and the conversion script does not ship. A positive `line` sets `Anchored = true`, which is what `IsGatingFinding` and the S4 quote-verifier seam see. Free-text arm-A extraction (`score.py extract-a`) stays in `.scratch` — no free-text parser ships.

---

## 3. Scorer semantics in Go

### 3.1 The port

New pure package `internal/core/quality` (no git, no network, no agent launch):

- `fixture.go` — catalog/recording types, capped loader, `DisallowUnknownFields`, semantic validation, compiled-pattern cache.
- `score.go` — `Match`, `Score`; the exact `score.py` semantics.
- `metrics.go` — aggregation, `QuoteVerifier`, report types.
- `service.go` — `RunQuality`, orchestration and typed refusals.

Match rule, reproducing `score.py:24-36`:

```go
func (s *Scorer) Match(f Finding, rules [][]string) bool {
    text := strings.ToLower(f.Path + "\n" + f.Claim)
    for _, rule := range rules {            // any-of across rules
        all := true
        for _, pattern := range rule {      // all-of within a rule
            if !s.compiled[pattern].MatchString(text) { all = false; break }
        }
        if all { return true }
    }
    return false
}

func (s *Scorer) Hit(d Defect, findings []Finding) bool {
    for _, f := range findings { if s.Match(f, d.Matchers) { return true } }
    return false
}
```

Details that matter for parity:

- Patterns compile with `(?i)` and text is lowercased, matching Python's `re.IGNORECASE` plus `.lower()`. Go's `regexp` is RE2: all 13 shipped patterns use only `\b`, alternation, literals and `.*`, all in the common subset. Validate at load; an invalid or non-RE2 pattern refuses the catalog. `\b` is ASCII-only in Go, so fixtures must keep word boundaries ASCII.
- `score.py` does **not** fold whitespace. Do not reuse `corpuscore.normalize`; use exactly `strings.ToLower(path + "\n" + claim)` with `nil`/empty fields rendered as `""`.
- `path` and `claim` both participate. A praise-only comment with the word `kode` in its `file` field can hit K1's first pattern; that is the harness's behavior and is preserved.
- One finding can hit several defects. No `used[]` consumption, no one-to-one rule.
- `infra_error` on a recording sets every hit to `null` (unknown) and marks the run `scored=false`; such runs contribute no recall or false-green sample.
- `praise` = `praise_re` matches the claim **and** `defect_verb_re` does not (`score.py:38-42`); `null_severity` counts findings with no severity; `other_findings` counts findings that hit none of the matched defects and are not praise (`:147-151`). These are diagnostic columns for the calibration table, not headline metrics.

### 3.2 Reproducing the 7-row calibration table

`backtest quality` reads `bench/quality/fixtures.json`, loads each recording's `review.json`, extracts findings, computes hit sets, and compares them with `calibration[].hand` as **sets** — the same PASS rule as `score.py:162-200`. Expected output (byte-for-byte equivalent to `calibration.md`):

| bundle            | fixture | hand | harness | hand hits      | harness hits   | praise | null sev | status |
| ----------------- | ------- | ---- | ------- | -------------- | -------------- | -----: | -------: | ------ |
| vr-sta440b-bench  | fx1     | 0/5  | 0/5     | -              | -              |      3 |        3 | PASS   |
| vr-sta440b-bench2 | fx1     | 0/5  | 0/5     | -              | -              |      3 |        3 | PASS   |
| vr-sta440b-bench3 | fx1     | 1/5  | 1/5     | K2             | K2             |      0 |        0 | PASS   |
| vr-sta440b-haiku  | fx1     | 5/5  | 5/5     | K1,K2,K3,K4,K5 | K1,K2,K3,K4,K5 |      0 |        0 | PASS   |
| vr-sta440b-haiku2 | fx1     | 1/5  | 1/5     | K5             | K5             |      0 |        4 | PASS   |
| vr-sta440b-sonnet | fx1     | 1/5  | 1/5     | K5             | K5             |      0 |        4 | PASS   |
| vr-sta351a-haiku  | fx2     | 1/6  | 1/6     | D2             | D2             |      0 |        0 | PASS   |

`round(x, 3)` in Python is not needed for PASS: the harness compares hit sets and prints `len(hits)/len(known)`. The Go report publishes rates at the repo's four-decimal precision (`corpuscore.RateDecimals = 4`); the hit-count strings stay exact, so 7/7 PASS is unambiguous. A Go golden test asserts the table text and a `TestCalibrationReproduced` asserts `reproduced == true`; CI's `make test-race` runs both. A mutation check (tighten/loosen K5) must make at least one row FAIL before the test is trusted. A second regression pin from `ablate/matcher-audit.md`: on the 39-run/136-finding audit corpus the post-fix matchers produce six cross-hit findings, not the ten the old K5 produced.

### 3.3 Do the recorded reviews ship?

**Yes as data, no as full bundles.** Measured:

- Seven bundles total **3,572 KB**; their `full.diff` copies alone are **832 KB**; 54 files contain absolute host paths (`$HOME/...` in `launcher.json`, `run.json`, `fetch.json`, `context.json`, `run.log`, vendored context). The diffs and specs are already recoverable from the pinned commits in git history.
- The seven `review.json` files total **20,090 B**. A scan found **no** secret-shaped tokens, no `Bearer`/PAT/API-key patterns, no private keys, no email addresses, and **no absolute home paths or host-specific data inside them**.
- Therefore ship the raw `review.json` bytes plus a small `meta.json` per recording (expected total ≈ 25 KB). Do **not** ship `launcher.json`, `run.json`, `fetch.json`, `gate.json`, `context/`, `full.diff`, `run.log` or any other machine-specific artifact. Gate outcome and the one infra error are transcribed into `meta.json`; they are the only facts extracted from `gate.json`.
- Context files ship separately, sanitized: `RESEARCH-2.md:4` has one `file://$HOME/.../RESEARCH.md` link, rewritten to a repo-relative link; recompute and pin the sha256 after sanitizing. Re-run the secret/host scan over everything under `bench/quality/` before commit.

---

## 4. Metrics and output schema

### 4.1 Definitions

For a fixture with known defects `K` (count `n`) and scored runs `r ∈ R` (infra-error runs excluded from every denominator):

- `per_run[r].recall = |hits(r,K)| / n`.
- `recall.mean = Σ per_run.recall / |R|`; `null` when `R` is empty.
- `recall.union = |⋃_r hits(r,K)| / n` — coverage over N runs, **not** an expected single-run score; it grows with N and is labelled as such in human output.
- `hits`, when a single run is selected, is the per-defect boolean map; `union_hits` is the OR over runs. Bonus defects use the same three shapes but a separate `bonus_union` count and are never in `recall`.
- `false_green.rate = |{r : gate_passed(r) == true}| / |{r : gate_passed(r) != null}|` on a fixture with at least one known blocker (both fixtures qualify). Runs with no gate result are counted in `unknown`, never as passes. Calibration yields `3/5` on fx1 (bench2, haiku2, sonnet) and `0/1` on fx2; `bench` is `unknown`. These numbers are historical measurements printed for context, **not** a CI acceptance gate — CI asserts only `calibration.reproduced`.
- `hallucination.rate` = gating findings whose quote fails verification / gating findings with an explicit `matched` or `mismatch` result. A finding gates when its severity is `critical` or `important`, it is inline/anchored, and S4 has not marked it `unverified`. `no-quote`, `unsupported` and unknown results are `unverifiable`: never checked, never clean. `quote_verifier: null` (stub) ⇒ rate `null`, `status: "stubbed"`, all gating findings unverifiable. Never print `0` for an unimplemented check.
- `review_insufficient` = number of runs whose recorded outcome is a typed `review-insufficient` refusal (code matched literally). No remapping from `missing-findings`, invalid schema, or agent timeout. It is `0` for all current recordings because S4 has not landed; the field is exact, not a projection.

S4 seam:

```go
type QuoteVerifier interface {
    Verify(ctx context.Context, f Finding, head []byte) QuoteResult
}
type QuoteResult struct {
    Status  string // "matched" | "mismatch" | "no-quote" | "unsupported"
    Reason  string
}
```

S6 ships `StubVerifier` returning `unsupported` and reports `null` with every gating finding counted as `unverifiable`. The verifier receives the fixture's pinned context bytes, not a nil head. S4 implements the same interface over its whitespace-normalized `path:line` quote check and one repair round; when it lands, flip the default to that implementation and the rate starts measuring without changing the schema. The verifier is called only for gating findings; no model is in this path.

### 4.2 JSON envelope

`data` is also written to `.verdict/backtest/quality/report.json` (ignored state), so one schema — `schemas/quality.schema.json` — describes both, exactly like `backtest.schema.json` (`internal/schemagen/model.go:36-44`). Key order is published via `jq.NewObj`.

```json
{
  "schema_version": "1",
  "verb": "backtest",
  "at": "2026-11-18T00:00:00Z",
  "next": "inspect .verdict/backtest/quality/report.json; rates describe recorded reviews and fixture defects",
  "warnings": [],
  "data": {
    "schema_version": "1",
    "mode": "quality",
    "recordings": 7,
    "scored": 7,
    "fixtures": [
      {
        "id": "fx1",
        "label": "STA-440 part 2, first build (291c83ae5..98d8becd9)",
        "runs": 6,
        "scored": 6,
        "known": 5,
        "bonus": 2,
        "recall": {
          "per_run": [0.0, 0.0, 0.2, 1.0, 0.2, 0.2],
          "mean": 0.2667,
          "union": 1.0,
          "union_hits": ["K1", "K2", "K3", "K4", "K5"]
        },
        "bonus_union": { "count": 0, "hits": [] },
        "false_green": {
          "passes": 3,
          "measured": 5,
          "unknown": 1,
          "rate": 0.6
        },
        "hallucination": {
          "rate": null,
          "status": "stubbed",
          "gating_findings": 5,
          "checked": 0
        },
        "review_insufficient": 0
      },
      {
        "id": "fx2",
        "label": "STA-351 PR A (d69957f9c..475310826)",
        "runs": 1,
        "scored": 1,
        "known": 6,
        "bonus": 0,
        "recall": {
          "per_run": [0.1667],
          "mean": 0.1667,
          "union": 0.1667,
          "union_hits": ["D2"]
        },
        "bonus_union": { "count": 0, "hits": [] },
        "false_green": {
          "passes": 0,
          "measured": 1,
          "unknown": 0,
          "rate": 0.0
        },
        "hallucination": {
          "rate": null,
          "status": "stubbed",
          "gating_findings": 1,
          "checked": 0
        },
        "review_insufficient": 0
      }
    ],
    "calibration": { "rows": 7, "reproduced": true, "mismatches": [] },
    "diagnostics": [
      {
        "recording": "vr-sta440b-haiku",
        "fixture": "fx1",
        "hits": ["K1", "K2", "K3", "K4", "K5"],
        "praise": 0,
        "null_severity": 0,
        "other_findings": 0,
        "gate_passed": false,
        "infra_error": null
      }
    ]
  }
}
```

Zero denominators render `null` (never `0`); every rate is rounded to four decimals. The example numbers above are illustrative of the definitions; the golden test asserts the real computed values. `diagnostics[]` carries the calibration columns and is what makes the 7-row table reproducible from JSON.

### 4.3 Human output

```
backtest quality  (2 fixtures, 7 recorded reviews, 7 scored)
  fx1  STA-440 part 2, first build (291c83ae5..98d8becd9)
    recall: per-run 0.0000 0.0000 0.2000 1.0000 0.2000 0.2000, mean 0.2667, union 1.0000 (N=6, 5 known)
    bonus: 0/2 union
    false-green: 3/5 passes (1 run without a gate result)
    hallucination: n/a — quote verification stubbed until S4 (5 gating findings, 0 checked, 5 unverifiable)
    review-insufficient: 0
  fx2  STA-351 PR A (d69957f9c..475310826)
    recall: per-run 0.1667, mean 0.1667, union 0.1667 (N=1, 6 known)
    bonus: 0/0
    false-green: 0/1 passes
    hallucination: n/a — quote verification stubbed until S4 (1 gating finding, 0 checked, 1 unverifiable)
    review-insufficient: 0
  calibration: 7/7 rows reproduced
known defects from fixture manifests; rates describe recorded reviews, not a model claim
```

`union` is named next to `N` on purpose: a reader must not mistake coverage over runs for per-run quality. No "not citable — SZZ-derived" footer — these defects are hand-known, and the honest caveat is the one shown.

### 4.4 Public surface additions

`schemas/quality.schema.json` generated from new aliases in `internal/schemagen/model.go`, a `documents()` entry in `internal/schemagen/build.go` (next to `backtest` at `:171` and `corpus` at `:226`), prose in `internal/schemagen/overlay.json`, and a mention in `schemas/README.md` and `docs/manual.md:4720` (published-schemas list). `make schemas-check` must pass. `--json` stays on the existing verb list (`cmd/verdict/usage.go:578`).

---

## 5. The opt-in live-run story

Live runs are an explicit operator action; the scorer mode never launches an agent and CI never runs one.

1. **Prepare once, in `.scratch`.** A clone containing the four pinned commits; copy or symlink `bench/quality/context/<fx>/*` into `.scratch/factory/sta-453/runs/<label>/context/`. The A/B/C arm scripts, lane drivers and judge stay in `.scratch/factory/bench-abc/`.
2. **Build the bundle with the existing verb** (no new launcher):

   ```sh
   make build
   verdict fetch-local stainless-code/verdict bt-fx1-<arm>-<n> \
     --range 291c83ae5..98d8becd9 \
     --context .scratch/factory/sta-453/runs/fx1/context \
     --worktree
   ```

   `--worktree` checks the historical head out in isolation; the operator's checkout is untouched. `fetch-local` is local git only (`cmd/verdict/usage.go:33-45`).

3. **Review and record with existing verbs:**

   ```sh
   verdict run stainless-code/verdict bt-fx1-<arm>-<n> --agent <name> -- --model <id>
   verdict record stainless-code/verdict bt-fx1-<arm>-<n>
   verdict report stainless-code/verdict bt-fx1-<arm>-<n> --check important --live skip
   ```

   Do **not** call `post` (local bundles refuse it anyway; the house rule is absolute). After S1/S2 the agent writes `review.json` first; `record` merges it into `findings.json`, applies S4's quote verifier and may emit `review-insufficient`.

4. **Record the outcome** in `.scratch/factory/sta-453/runs/<label>/meta.json` with `fixture`, `driver`, `source: "findings.json"`, `gate_passed` from the `report --check` exit code, and `refusal_code` when one was returned. Keep the bundle's `review.json`/`findings.json` beside it; no host binary paths or absolute paths go into the recording.
5. **Score explicitly:**

   ```sh
   verdict backtest quality --fixture fx1 \
     --recordings .scratch/factory/sta-453/runs --json
   ```

   `--recordings` is opt-in and never globbed by default; with no flag the mode reads only tracked `bench/quality/<fixture>/recordings/`. CI step 1 is just `verdict backtest quality --json` and asserts `data.calibration.reproduced == true`. Nothing writes into `bench/quality/`; the only write is the ignored `.verdict/backtest/quality/report.json`.

`backtest replay` is deliberately **not** reused: it mines SZZ candidates and spends on agents, while fixtures are fixed base/head ranges with hand-known defects. The two remain separate lanes under the same verb.

---

## 6. Ordered commit plan and blast radius

| #   | Commit                                                                    | Contents                                                                                                                                                                                                                                                                           | Blast radius / verification                                                                                                                                                                                                          |
| --- | ------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| 1   | `feat(bench): add STA-440/351 known-defect fixtures and recorded reviews` | `bench/quality/**` (~85 KB), generation note with the sanitization and scan steps, `docs/benchmark.md` fixture-format subsection linked from `docs/manual.md:4294`.                                                                                                                | Data only, no Go. Verify sha256 pins, no absolute paths/secrets, `git ls-files bench/quality`. No golden changes.                                                                                                                    |
| 2   | `feat(quality): deterministic fixture scorer`                             | `internal/core/quality/{fixture,score}.go` + tests: 7-row calibration golden, K5 audit/cross-hit regression, refusal tests, RE2 load guard, mutation-proof note. Add `FuzzMatchFixtureRule` to the package and `Makefile:47` `FUZZ_TARGETS`.                                       | New package only; imports `internal/core` + `internal/jq` (+ `corpuscore.Rate` if reused). `go test ./internal/core/quality/...`, `make fuzz` once, `gofmt`, `golangci-lint`.                                                        |
| 3   | `feat(quality): metrics, quote-verifier seam and published schema`        | `metrics.go`, `service.go` report types, `internal/schemagen/model.go` aliases, `build.go` document, `overlay.json`, regenerated `schemas/quality.schema.json`, `schemas/README.md`, manual published-schemas line.                                                                | New schema file + schemagen drift. `make schemas-check`. Run `make size`; if a 10 kB line is crossed, raise only that line and put the measurement in the commit message (AGENTS.md verification ladder rung 2).                     |
| 4   | `feat(backtest): quality mode`                                            | `internal/backtest/quality.go`, wire `Run`/`checkMode`/`wanted`/flag table (`internal/backtest/backtest.go:67,57,274,346`), usage text (`cmd/verdict/usage.go:387`), manual score-section successor (`docs/manual.md:4629`), human golden under `internal/core/quality/testdata/`. | Changes the `backtest` help surface and one golden; no new CLI verb, no MCP/SDK/archtest change. `make build`, `go test ./internal/backtest/... ./internal/core/quality/...`, `./evals/one.sh backtest-quality` (new case) if added. |
| 5   | `test(evals): pin the quality mode through the binary`                    | `evals/cases/backtest-quality.sh`: run `backtest quality --json` against the shipped catalog, assert 7/7 calibration and stubbed hallucination, assert no host CLI was touched (`EVAL_STUBLOG`).                                                                                   | One eval case, offline. `make evals`, `make test-race`, `make vet`, `make cross`, `make ci-go` before claiming done.                                                                                                                 |

No changes to `bench/corpus/`, `internal/core/backtest/`, `schemas/backtest.schema.json`, `internal/core/corpus/`, or the Procedure prompts — so `prompts_hash` and the six hashed Procedure files do not move. One commit per change, no `Co-authored-by`, no push (AGENTS.md).

---

## 7. Open questions

1. **Mode name.** Design uses `backtest quality`. Alternatives: `fixtures`, `known-defects`. Operator call needed because help text and docs become contract.
2. **Context shipping.** Recommendation is to vendor the three sanitized files (~64 KB). If the operator prefers them to stay in `.scratch`, the manifest instead records `sha256`/`bytes` pins against external paths and live runs cannot be reproduced from the checkout alone.
3. **False-green denominator.** Recommendation: exclude `gate_passed: null` runs and publish `unknown`. The alternative — treating a missing gate result as a pass — would inflate the rate from 3/5 to 4/6 on fx1 and is misleading.
4. **Hallucination threshold.** "Gating finding" is defined here as `critical|important`, inline/anchored, not already `unverified`. Confirm with the S4 owner that this matches the gate's own threshold after S1, including the default `post_min_severity`.
5. **`review-insufficient` before S4.** The field is a literal count of that code and is `0` today. If the operator wants a visible "not yet produced" state, add `status: "awaiting-s4"` beside the count; the design currently does not, to keep the count exact.
6. **Rate precision.** Go publishes four decimals repo-wide; Python's `score.py` rounds to three. PASS is defined on hit sets and exact `n/m` strings, so the tables agree. Confirm that no consumer depends on Python's 3-decimal `recall` number itself.
7. **Calibration data freshness — resolved.** The seven recordings are converted once at authoring time into the fixture recording shape; the old shape is not a runtime input and there is no migration/compat code. Later recordings use the same shape (or the findings store when S1 lands). The seven stay the immutable calibration.
8. **Bonus defects.** Design keeps them out of recall and reports `bonus_union` only. Confirm that matches the operator's meaning of "bonus".
9. **Size ratchet.** Adding a schema description and regex scorer may cross a 10 kB line in `scripts/size-baseline.txt`. Measure in commit 3; raise only the crossed line.
10. **Audit corpus.** `ablate/matcher-audit.md` cites 39 runs/136 findings that are not among the 7 shipped recordings. If the six-cross-hit regression test must be data-driven rather than a small in-test table, decide whether to ship a redacted audit corpus too; recommendation is a compact literal test table, not 39 more reviews.
