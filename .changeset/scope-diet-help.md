---
"@stainless-code/verdict": patch
---

`verdict --help` now states the per-verb provider scope next to each verb:
`record`/`report`/`triage`/`show`/`next` run on any provider bundle while
`post`/`calibrate`/`adopt`/`ready` need a GitHub bundle and refuse the rest
typed (`provider-unimplemented`). The `fetch` entry records the dated
exception that incremental re-review (`--base-sha`/`--since`) is GitHub-only.
No refusal code or gating behavior changed.
