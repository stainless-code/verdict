# Glossary

The vocabulary Verdict's help, prompts, and reports use. Terms are grouped by
where you meet them; each definition matches how the verbs behave today.

## Review shape

**Bundle.** The directory one fetch produces: `.verdict/state/<pr>/` for a fetch, or
`.verdict/state/local-<name>/` for `fetch-local`. It holds the diff, TOC, roles, and
normalized metadata; every later verb reads it and never re-fetches on its own.

**Discovery registry.** The local, 24-hour cache of repository paths found below a
`serve --root` directory. It stores no review data and is revalidated against each
repository's `.verdict/` directory at startup. `--rediscover` bypasses the cache.

**Discovery snapshot.** The bounded set of `.verdict/` repositories found when
`serve` starts. Symlink entries and the documented ignored directories are not
traversed; each encountered symlink entry is counted without following it.
Either walk cap marks the result `truncated`.

**Lens.** A read-only subagent dispatched to answer one falsifiable question
whose answer could change the review's disposition. Lenses are staffed by
domain (billing, auth, schema migration), not by file list; a review with no
disposition-changing question dispatches none. The synthesizing agent still
owns the read, validation, and the written review — never delegate
understanding.

**Lens pack.** A directory holding `pack.json`, `lenses/<id>.md`, and
`rules/<id>.md`. Packs layer built-in < org < repo; `verdict pack list`
reports each pack's layer, source, and canonical tree hash. `verdict pack add`
vendors a directory or a hermetic git clone into the repo layer and pins it in
`.verdict/verdict.lock`; a higher pack overrides a lower id only when it names
that lower pack in `replace`, so an override is always a declaration rather
than an accident.

`verdict pack test` runs the pack's offline cases from `evals/` against a diff
and checks that findings cite the expected rules.

**Panel.** The pinned model roster a review runs with, from `REVIEW_PANEL` or
the typed `panel` default in `.verdict/config.json`. `fetch` resolves it into `.verdict/state/<pr>/panel`;
the first entry is the synthesizer, the rest staff lenses, used exactly in
order and never substituted. No panel means the operator picks per run.
The root `panel` file is the shared default roster: the `--show-prompts`
dump's `plan.txt` section additionally exports each group's actually resolved
roster (`panel=[...]`, `panel_from`) and accounting `effort`, and the shipped
dispatcher uses those per-group values for that group's files.

**Code index.** The local code index under `.verdict/index/`, keyed by tree
SHA: Go built from `go/parser`, TypeScript, JavaScript, and Python from two
lexical scanners. `index callers` labels each row `exact`
(within the declaring package) or `lexical` (an exported name reached through
an import edge); everything type-dependent stays counted as unresolved.
`index tests-for` labels each candidate test `same-package`, `references`,
`imports-module`, or `transitive-import` — candidates to read, never coverage
claims — and names the import hops that reached the test, bounded by `--depth`.
`index co-changed` counts the paths that moved in the same bounded history
window and echoes that window. `index owners` answers the CODEOWNERS rule at the
tree (GitHub file order, last match wins), with a null owner list when no rule
matches rather than an owner named nobody.

**Impact hand-off.** The blast-radius section a bundle gains when the operator
names an index: `fetch` or `fetch-local --index <ref|dir>` writes
`.verdict/state/<bundle>/impact.json` and appends a capped `## impact` section
to `toc.txt`. It is a hand-off, never a discovery: nothing builds, refreshes, or
crawls an index, and a fetch without the flag writes no section. `impact.json`
carries `sources: ["index:<tree>"]` naming the tree that answered.

## Evidence

**Invalidation.** A live `serve` event that names bundles whose views changed
or disappeared. It carries no view data; clients fetch the view again, and a
truncated event means refetch the inbox.

**Sweep.** The slower `serve` pass that fingerprints directory entries to
catch in-place edits the faster directory-stamp tick cannot see.

**Evidence rung.** The proof behind a finding: `said`, `pointed`, `walked`,
`ran`, or `reproduced` (default `pointed`). The rung decides how far a finding
travels; `said`-only findings ship labeled `unproven` or die in triage. Nothing
posts as fact that was never shown.

**Severity tier.** A finding's next-action rank — `critical`, `important`, or
`informational` — stored at write time, never parsed from prose. `report
--check <tier>` gates on open findings at or above that tier; untagged findings
never meet a tier.

**Stored precision.** The `serve` view that joins findings to `threads.json`.
It counts open, resolved-unfixed, and unknown outcomes; without changed-file
evidence, fixed counts remain `null`, and no rate is published.

**Cost week.** A `serve` cost bucket keyed by the UTC ISO week of the bundle's
`post.json` `posted_at` receipt. Cost records do not carry their own timestamp.

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
