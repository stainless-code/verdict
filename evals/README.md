# Offline contract evals

Smoke-grade graders for the doctrine and verb contracts in `SKILL.md` and
`scripts/review.sh`: exactly-one terminal write, the no-review-note rule,
`--bind` downgrade behavior, and the `--json` envelope shape
(`schema_version: "1"`). They complement calibration (which grades judgment
against live ground truth); these grade contracts against fixtures.

## Run

```sh
./evals/run.sh            # all cases; exits non-zero on any failure
./evals/run.sh --keep     # leave the temp sandbox in place for debugging
```

New cases go in `evals/cases/*.sh` (sourced helper: `evals/lib/common.sh`);
`run.sh` picks up every file there, so no registration step. Results land in
`evals/results/summary.json`, which is gitignored (`evals/.gitignore`) —
the harness asserts the run, the repo keeps no trace of it.

## Cases

| Case              | Contract                                                                                              | How it grades (all offline)                                                                                                                                                                                                                                                             |
| ----------------- | ----------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `terminal-write`  | Exactly-one terminal write; no-review-note rule                                                       | Greps the doctrine pins in `SKILL.md` / `prompts/incremental-review.md`; a degenerate body must be refused as `degenerate-body` with no findings file; a valid review via `post --dry-run --json` must emit exactly one stdout doc with a single event plus one findings file           |
| `bind-downgrade`  | Binding verdicts need `--bind` (else COMMENT)                                                         | Clean bundle: no `--bind` → `COMMENT` + downgrade warning; `--bind` → `APPROVE`; `--request-changes --bind` with an open finding → `REQUEST_CHANGES`                                                                                                                                    |
| `envelope-shape`  | `--json` envelope (`schema_version`, `verb`, `repo`, `pr`, `at`, `next`, `warnings`, `data`, `error`) | `report` and `post --dry-run` success envelopes carry exactly the documented keys with `schema_version "1"` and null error; a refusal keeps the shape with null data and a string `error.code`                                                                                          |
| `kb`              | Structured local knowledge base: learn, cite, credit, retire, recall                                  | `learn` append refuses duplicates/bad enums; mixed cites keep only the valid id; triage addressed then re-flip credits once; threshold retires with tombstone; `fetch-local` writes `kb.txt` on match, nothing on clean, nothing new without `kb.jsonl`                                 |
| `completeness`    | Opt-in HEAD-reviewed gate on the report verb                                                          | Post-record match passes; missing/mismatched/unparsable/empty post fails exit 1 with a typed check line naming the re-fetch; tier+completeness fail on either; absent flag keeps today's output/exits; dry runs never mint `post.json`                                                  |
| `skill-installer` | Copy-based skill installer: detect + preview + confirm, foreign untouched                             | Preview without `--yes` refuses typed `needs-confirm` and writes nothing; `--yes` installs `verdict/` byte-identical with receipts under detected repo+home roots; unowned `verdict/` and foreign skills stay intact; re-run reads keep exit 0; stale receipts nudge, `--yes` refreshes |

## Hermeticity (how "no network" is proved, not promised)

- Fixtures are crafted files (`PR.json` state OPEN + one-hunk diff) written
  into a `mktemp` sandbox that `run.sh` uses as the CLI cwd; the repo's own
  `.review/` is never touched.
- `run.sh` puts a network-dead `gh` stub first on `PATH`: every `gh` call the
  CLI attempts (live-SHA lookup, authorship check) fails fast into the
  script's existing warn-and-proceed path, and each interception is logged.
  The tally line reports the intercept count — zero real calls by construction.
- After the cases, `run.sh` diffs `git status --short` before/after and fails
  the run if the tree moved; fixtures are deleted unless `--keep`.

## Hygiene

- Shell is bash 3.2 compatible (macOS `/bin/bash`); keep it that way.
- `shellcheck -S warning` must be clean on everything added:

```sh
shellcheck -S warning evals/run.sh evals/lib/common.sh evals/cases/*.sh
```
