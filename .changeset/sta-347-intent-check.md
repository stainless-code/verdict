---
"@stainless-code/verdict": minor
---

Spec checks now distinguish a requirement nothing implements from one the code
violates. A `spec_checks[]` row may carry `"status": "unmet"` with a `note`
saying what is missing: it cites no `evidence`, because nothing in the diff
answers it. `unmet` withholds approval with `unmet spec check: <item>`, fails
the always-on `spec` gate dimension, and records as a review outcome rather
than an evidence gap. A `violated` row keeps its wording, so an existing
violations-only review decides and renders byte-identically.

A spec source that enumerates nothing now owes exactly one check: the goal it
states, taken from its first heading or its first prose line, capped at 300
runes. An ordinary prose issue was previously a requirement-free source that
satisfied every gate while asking for nothing, and it now yields one item an
agent must grade. A change the shipped spec prompt does not ask for is reported
as a finding with `category: "accidental-commit"` instead of being stretched
into a spec row.

`n/a` and `unmet` both carry no evidence — a row citing a path, a line, or a
quote there is `spec-evidence-mismatch` — and both want a one-line `note`
(at most 500 characters, never echoed into a problem message). An `unmet` row
without one is `spec-note-missing` (error); an `n/a` row without one is
`spec-na-unjustified`, a warning that `--strict` or `strict_input` promotes, so
rows already in the wild still re-record. The rendered `**Spec checks**` block
adds the `unmet` tally and names the spec sources it graded, and the schema
change is additive: an optional `note`, `unmet` added to the status enum, and
`evidence` no longer required on the row.

`docs/manual.md` also corrects a false claim: `defaults.spec_coverage_min` never
relaxed the per-item requirement, which refuses `spec-check-missing` at any
coverage level, and the manual now says so.
