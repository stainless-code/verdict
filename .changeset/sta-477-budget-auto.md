---
"@stainless-code/verdict": minor
---

`verdict run` gains `--budget auto|light|standard|deep` and the repository
defaults `defaults.budget` and `defaults.sensitive_paths`, so a run can pick
its own review depth from the captured diff before any agent launches. The
classifier is a pure, deterministic function of bundle measurements — no model,
no host, no network — and it records the tier plus the signals that selected it
in `launcher.json` and `--json` `data.budget`; `run.json`'s `inputs_sha256` seal
does not move.

Three tier names map onto two launch shapes on purpose. `light` runs one
finder; `standard` and `deep` run two finders with opposite-finder
cross-checks. A third finder is schema-pinned out of reach, and a two-finder run
cannot drop its cross-check without making every gated finding unapprovable, so
`deep` escalates the recorded reason and cannot be under-powered by a
repository-wide `defaults.finders: 1` rather than launching a wider panel. An
explicit `--finders N` always wins and records `overridden_by: "finders"`.

The default sensitive globs (`**/auth/**`, `**/security/**`, `**/migrations/**`,
`**/billing/**`, `.github/workflows/**`) escalate a diff to `deep`;
`defaults.sensitive_paths` extends that list and can never remove an entry. Both
new defaults keys are global-only: a scope that sets either refuses `bad-scope`.
`--budget` means review depth, not spend: `budget_usd` and `defaults.effort`
keep their existing meanings.
