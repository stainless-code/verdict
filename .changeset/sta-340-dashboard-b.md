---
"@stainless-code/verdict": minor
---

`verdict dashboard` gains five more tabs: **Detail** (`3`: one bundle's gate, counts and findings with severity, status, rigor and quote check), **Diff** (`4`: lanes in risk order with anchored findings marked; `u` toggles side by side), **Compare** (`5`: findings against a baseline picked with `b`), **Cost** (`6`) and **Precision** (`7`). The view refreshes as bundles change (`--interval 2s`, `--sweep 60s`, `--no-watch` to stop), without blocking keys. Keys: `j`/`k`, `g`/`G`, Ctrl-D/Ctrl-U, `Enter`, `Esc`, `/` to search, `r` to refresh, `y` to copy the finding's fix prompt through OSC 52, and `?` for help. `dashboard <owner/repo>` narrows to one repository, `--check` sets the gate tier, and `--once --tab <name>` prints any tab as text.
