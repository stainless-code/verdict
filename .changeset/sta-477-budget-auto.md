---
"@stainless-code/verdict": minor
---

`verdict run --budget auto|light|standard|deep` and the repository defaults `defaults.budget` and `defaults.sensitive_paths` let a run pick its review depth from the diff before any agent launches. The choice is a deterministic function of the bundle, with no model or network, and the tier and the signals behind it are recorded in `launcher.json` and `data.budget`.

`light` runs one finder; `standard` and `deep` run two finders with cross-checks, and `deep` is not lowered by a repository-wide `defaults.finders: 1`. An explicit `--finders N` always wins. The default sensitive globs (`**/auth/**`, `**/security/**`, `**/migrations/**`, `**/billing/**`, `.github/workflows/**`) escalate a diff to `deep`; `defaults.sensitive_paths` adds to them and cannot remove any. Both keys are global only: a scope that sets either refuses `bad-scope`. `--budget` means review depth, not spend.
