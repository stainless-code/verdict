# Glossary

The vocabulary Verdict's help, prompts, and reports use. Terms are grouped by
where you meet them; each definition matches how the verbs behave today.

## Review shape

**Bundle.** The directory one fetch produces: `.review/<pr>/` for a fetch, or
`.review/local-<name>/` for `fetch-local`. It holds the diff, TOC, roles, and
normalized metadata; every later verb reads it and never re-fetches on its own.

**Lens.** A read-only subagent dispatched to answer one falsifiable question
whose answer could change the review's disposition. Lenses are staffed by
domain (billing, auth, schema migration), not by file list; a review with no
disposition-changing question dispatches none. The synthesizing agent still
owns the read, validation, and the written review — never delegate
understanding.

**Panel.** The pinned model roster a review runs with, from `REVIEW_PANEL` or
the typed `panel` default in `.verdict/config.json` (or the legacy `.review/panel`,
one slug per line). `fetch` resolves it into `.review/<pr>/panel`;
the first entry is the synthesizer, the rest staff lenses, used exactly in
order and never substituted. No panel means the operator picks per run.

## Evidence

**Evidence rung.** The proof behind a finding: `said`, `pointed`, `walked`,
`ran`, or `reproduced` (default `pointed`). The rung decides how far a finding
travels; `said`-only findings ship labeled `unproven` or die in triage. Nothing
posts as fact that was never shown.

**Severity tier.** A finding's next-action rank — `critical`, `important`, or
`informational` — stored at write time, never parsed from prose. `report
--check <tier>` gates on open findings at or above that tier; untagged findings
never meet a tier.

**Sig.** A finding's stable, line-number-free signature (with `sig_version`
`"1"`). Sigs survive rebases and re-slices, dedupe incremental reviews, group
threads, and are the join key for triage, calibration, and external policy
tools. Commands accept a unique short prefix.

## Doctrines

**Keep-biased.** The default when the tool cannot prove one way: anchor
validation keeps an ambiguous finding and moves only provably-outside anchors
into the body; one bad anchor never fails a post. The machinery is skeptical of
its own doubts and strict about its claims. Its sibling doctrines:

**Fail-closed.** When the safe answer is unavailable, refuse with a typed code
and a `try:` hint instead of silently proceeding — corrupt stores, missing
prompt files, and unvalidated inputs refuse rather than guess.

**Never-forced.** Unknowns stay unknown: an absent model, severity, counter, or
grade is `null`/unknown, never invented or coerced to zero; a judgment the
evidence does not support is not reported.

**Additive-only.** Store changes add optional keys within `schema_version`
`"1"`; history is append-only; the calibration probe writes only its one
`kb-grades.jsonl` file. Renames and tighter shapes require a version bump.
