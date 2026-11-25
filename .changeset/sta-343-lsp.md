---
"@stainless-code/verdict": minor
---

New `verdict lsp` (experimental): a stdio language server (LSP 3.17) that shows a review bundle's open findings as editor diagnostics in any LSP client. Diagnostics update on open, save, a bundle change (`record`, `fetch-local`, `triage`) and the `verdict.refresh` command, never while you type. `critical`, `important`, `informational` and untagged map to Error, Warning, Information and Hint; addressed, triaged and policy-withheld findings never show. It serves the editor's workspace folders, or each `--root` you pass (every root must be inside a git checkout). `--bundle` pins a bundle; by default it uses the newest `local-` bundle, then the newest PR bundle. `--no-watch` turns the watcher off. It makes no model calls and reads no workspace file.
