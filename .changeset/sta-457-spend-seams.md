---
"@stainless-code/verdict": minor
---

Add phase usage attribution to `cost.json`: each phase names its execution, finder and launch. USD and token counts explicitly permit unknown values. The shared projection preserves unknown phases, compares exact decimal totals and known lower bounds, and never counts a root aggregate beside its phase costs. Phase certification checks the execution and captured run binding.

`run --consent-over-budget` grants consent only to the current invocation. For a captured S5 execution, authorization precedes any launch and records the bounded topology and projection digest in the budget receipt ledger. Unknown required prices refuse as `budget-unknown` under the require-known policy, and hard billing caps remain unsupported. Ordinary single-finder launching remains unchanged.

Changed schema: `schemas/cost.schema.json` and its matching generated baseline and semantic pin. No schema version compatibility decoder was added. No goldens changed in this slice.
