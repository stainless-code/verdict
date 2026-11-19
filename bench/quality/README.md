# Known-defect fixtures

This directory is the `backtest fixtures` corpus: five reviewed ranges with hand-known defects, their context files, each fixture's reviewed `full.diff`, and seven converted recordings on the first two ranges that pin the scorer. The three CodeRabbit-known-defect ranges exist for live runs; the only recording among them is one synthetic quote probe on fx4, which pins the verifier's rate without a model in the loop. It is a sibling of [`../corpus/`](../corpus/), not a set of `case.json` manifests: the ground truth here is a defect description plus regex matchers, and scoring is per defect over `path` + `claim`, with no line window.

## Files

- `fixtures.json` — the catalog: fixtures, known and bonus defects with their matchers, the eight hand scores, and the praise/defect-verb regexes used by the calibration table.
- `context/<fixture>/` — the spec and research files the live review was handed plus the fixture's pinned `full.diff`. Their paths, byte counts and sha256 values are pinned in the catalog.
- `<fixture>/recordings/<bundle>.json` — the corpus's own minimal recording shape: `schema_version`, `findings` with `path`, `line`, `severity`, `text`, and optional `quote`/`start_line`/`side`, plus `gate_passed`. The scorer reads only this shape; there is no reader for an older review format. `fx4/recordings/synthetic-quotes.json` is a deterministic two-finding probe (one matched quote, one fabricated quote) that proves the real quote verifier measures `rate: 0.5`.
- `<fixture>/recordings/<bundle>.meta.json` — driver, provenance, infra error and refusal code. `source_bundle` and `source_sha256` name the archived bundle the recording was converted from; the conversion was a one-time authoring step and its script is not shipped. A nil `gate_passed` means there was no gate result, not a pass. A recording with no archived bundle to convert, such as the synthetic probe, carries the all-zero digest (`000…0`) and says so in `note`.

## Matcher semantics

A defect's `matchers` is a list of rules. Each rule is a list of RE2 patterns; all patterns in one rule must match, and any rule may match. Patterns are compiled case-insensitively and applied to `strings.ToLower(path + "\n" + claim)` with no whitespace folding. One finding can match several defects; a defect is hit when any finding matches it. `bonus` defects are reported separately and never enter recall.

## Provenance and regeneration

The fx1 and fx2 fixtures come from STA-440 part 2 and STA-351 PR A. fx3, fx4 and fx5 encode the defects CodeRabbit caught on PRs #187, #186 and #183 before their fix commits; their matchers are scored only when a live recording is supplied. The four reviewed commits are in this repository's history, so an operator can rebuild either range offline with `verdict fetch-local … --range <base>..<head> --context <fixture context> --worktree`. The A/B/C arm scripts, drivers, judge and all raw run logs stay in the operator's `.scratch`, outside the shipped corpus.

The source reviews were checked for secret-shaped tokens, email addresses and absolute host paths before shipping; none was found. `RESEARCH-2.md` carried two absolute `file:///` links to sibling research notes, rewritten here to relative names, and the catalog pins the sha256 of the sanitized bytes. The gate outcome was transcribed into each converted recording; no other run artifact ships.
