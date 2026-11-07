---
"@stainless-code/verdict": minor
---

`record --cost-names[=<names>]` names the `models[]` entries of a bundle's cost record and its per-chunk records, in place and without changing a count. Names come from the flag's own ordered list — synthesizer first, then lenses — or from a panel pinned in the bundle; with neither, the repair refuses rather than inventing a name from a slug.

An entry that already names a model keeps that name. A name that contradicts panel order refuses with a code naming the entry, and an entry the panel cannot explain refuses rather than being left half-attributed. Every refusal writes nothing, so a failed repair never leaves a bundle partly named.

This verb is how an operator repairs the array that the same release made the only shape: a record whose `models` is an object is `cost-models-not-array` here rather than converted, because converting would restore the second shape the reader just dropped. Migrate by writing `"models": [{"name": "<panel slug>", "model": "<model>", ...}]` — that migration note belongs to the array-shape change, and this verb is its repair path.
