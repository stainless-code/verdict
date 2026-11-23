---
"@stainless-code/verdict": patch
---

`examples/typescript-client/` now also runs `gate` and `fix` end to end against the locally built binary, and `pkg/verdict` gains checked `ExampleClient_Post` (dry-run), `ExampleClient_Fetch`, `ExampleClient_Gate`, and `ExampleClient_Fix` functions. The TS client gate executes every example, so the new coverage fails CI instead of rotting in the tree.
