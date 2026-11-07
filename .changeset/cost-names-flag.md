---
"@stainless-code/verdict": minor
---

`record --cost-names[=<names>]` names the `models[]` entries of a bundle's cost record and its per-chunk records, in place and without changing a count. Names come from the flag's own ordered list — synthesizer first, then lenses — or from a panel pinned in the bundle; with neither, the repair refuses rather than inventing a name from a slug.

An entry that already names its seat keeps that name. A name that sits on the wrong seat, repeats, or comes from outside the panel refuses with a code naming the entry and the seat it should hold, and an entry the panel cannot explain refuses rather than being left half-attributed. A document holding several records gives each record the whole panel, never a share of it.

The repair plans every cost file before writing any, and it runs after the record verb's other checks, so no refusal rewrites a cost record. A cost file that changed after it was planned refuses rather than replacing the newer write, and a record that grew past the reader's 8 MiB cap refuses before anything is written. A write that fails mid-pass stops with the files already written named in the refusal, and re-running finishes the repair.

This verb is how an operator repairs the array that the same release made the only shape: a record whose `models` is an object is `cost-models-not-array` here rather than converted, because converting would restore the second shape the reader just dropped. Migrate by writing `"models": [{"name": "<panel slug>", "model": "<model>", ...}]` — that migration note belongs to the array-shape change, and this verb is its repair path.
