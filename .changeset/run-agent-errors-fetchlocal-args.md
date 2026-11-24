---
"@stainless-code/verdict": patch
---

`verdict run` no longer collapses agent failures into `review-insufficient`. When every finder fails, the run refuses with the agent's own code (`agent-failed` with its exit status, or `no-review-artifact`), and the message carries a bounded, credential-scrubbed tail of the agent's output, so an HTTP 402 or a refused write is visible. A partial two-finder run keeps `review-insufficient` but names the failed finder's cause. The `claude` permission hint now prints for the default two-finder launch, as it already did for one finder. `fetch-local` now refuses a bare word before `--` as a usage error instead of treating it as a path filter; path filters stay `-- <paths>`. The skill-install tests no longer read the host's `CLAUDE_CONFIG_DIR`.
