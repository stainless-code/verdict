---
"@stainless-code/verdict": minor
---

Spec checks now tell a missing requirement from a violated one. A `spec_checks[]` row may be `"status": "unmet"`, with a `note` saying what is missing and no `evidence`. `unmet` withholds approval (`unmet spec check: <item>`) and fails the `spec` gate dimension.

**Behavior change:** a spec source that enumerates nothing now owes exactly one check, the goal it states (its first heading or prose line). Before, a prose-only issue passed every gate while asking for nothing. A change the spec does not ask for is reported as a finding with `category: "accidental-commit"`.

`n/a` and `unmet` rows carry no evidence (otherwise `spec-evidence-mismatch`) and need a one-line `note`. A missing note is `spec-note-missing` on `unmet` and the `spec-na-unjustified` warning on `n/a` (an error under `--strict`). The `Spec checks` block adds the `unmet` tally. The schema change is additive.
