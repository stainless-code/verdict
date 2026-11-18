# Known-defect fixtures

This directory is the `backtest fixtures` corpus: two reviewed ranges with hand-known defects, their context files, and seven converted recordings that pin the scorer. It is a sibling of [`../corpus/`](../corpus/), not a set of `case.json` manifests: the ground truth here is a defect description plus regex matchers, and scoring is per defect over `path` + `claim`, with no line window.

## Files

- `fixtures.json` — the catalog: fixtures, known and bonus defects with their matchers, the seven hand scores, and the praise/defect-verb regexes used by the calibration table.
- `context/<fixture>/` — the spec and research files the live review was handed. Their paths, byte counts and sha256 values are pinned in the catalog.
- `<fixture>/recordings/<bundle>.json` — the corpus's own minimal recording shape: `schema_version`, `findings` with `path`, `line`, `severity` and `text`, plus `gate_passed`. The scorer reads only this shape; there is no reader for an older review format.
- `<fixture>/recordings/<bundle>.meta.json` — driver, provenance, infra error and refusal code. `source_bundle` and `source_sha256` name the archived bundle the recording was converted from; the conversion was a one-time authoring step and its script is not shipped. A nil `gate_passed` means there was no gate result, not a pass.

## Matcher semantics

A defect's `matchers` is a list of rules. Each rule is a list of RE2 patterns; all patterns in one rule must match, and any rule may match. Patterns are compiled case-insensitively and applied to `strings.ToLower(path + "\n" + claim)` with no whitespace folding. One finding can match several defects; a defect is hit when any finding matches it. `bonus` defects are reported separately and never enter recall.

## Provenance and regeneration

The fixtures come from STA-440 part 2 and STA-351 PR A. The four reviewed commits are in this repository's history, so an operator can rebuild either range offline with `verdict fetch-local … --range <base>..<head> --context <fixture context> --worktree`. The A/B/C arm scripts, drivers, judge and all raw run logs stay in the operator's `.scratch`, outside the shipped corpus.

The source reviews were checked for secret-shaped tokens, email addresses and absolute host paths before shipping; none was found. `RESEARCH-2.md` carried two absolute `file:///` links to sibling research notes, rewritten here to relative names, and the catalog pins the sha256 of the sanitized bytes. The gate outcome was transcribed into each converted recording; no other run artifact ships.
