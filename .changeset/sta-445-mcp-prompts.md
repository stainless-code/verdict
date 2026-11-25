---
"@stainless-code/verdict": minor
---

MCP hosts can now load the Procedure straight from the binary. `prompts/list` and `prompts/get` serve the task prompts (`address-reviews`, `fix-ci`, `incremental-review`, `review`, `validate`), and `resources/list` and `resources/read` serve every Procedure file at `verdict://skill/<path>`, identical to `verdict skill show`. Prompts and resources are read-only and available in every mode, `--read-only` included. `initialize` points hosts at the `review` prompt or `verdict://skill/PROCEDURE.md`.
