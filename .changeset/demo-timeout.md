---
"@stainless-code/verdict": minor
---

`verdict demo` now accepts `--timeout <secs>` in both spellings, defaulting to the same 300 s the `run` verb gives a launch. The `agent-timeout` refusal and its `try:` line tell the operator to raise `--timeout`, but `demo` hard-coded the budget and took no flag, so the advice named something the verb did not have. The value rides to the run service's existing validator, so a non-positive or non-numeric value refuses `bad-timeout` the same way `run --timeout` does.
