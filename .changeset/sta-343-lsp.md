---
"@stainless-code/verdict": minor
---

New `verdict lsp` (experimental): a stdio Language Server (LSP 3.17, zero dependencies) that publishes a review bundle's open findings as editor diagnostics in any LSP client. Diagnostics move on `didOpen`, `didSave`, a bundle watch change (an out-of-band `record`, `fetch-local`, or `triage`), and the `verdict.refresh` command — never on `didChange`. Severity maps `critical`/`important`/`informational`/untagged to Error/Warning/Information/Hint; addressed, triaged, and policy-withheld findings never publish. `--root` repeats for multi-root workspaces, `--bundle` pins a bundle (else the newest `local-` bundle, then the newest PR bundle), and `--no-watch` turns the watcher off. The server makes no model calls, writes nothing, and reads no workspace file. Hover and code actions follow in a later release.
