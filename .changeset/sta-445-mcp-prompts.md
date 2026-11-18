---
"@stainless-code/verdict": minor
---

MCP hosts can now load the Procedure straight from the binary. `prompts/list` and `prompts/get` serve five task prompts (`address-reviews`, `fix-ci`, `incremental-review`, `review`, `validate`), and `resources/list` and `resources/read` serve every payload file at `verdict://skill/<path>`, byte-identical to `verdict skill show`. Prompts and resources are read-only and available in every server mode, including `--read-only` and `--operator`. `initialize` advertises both capabilities and points hosts at the `review` prompt or `verdict://skill/PROCEDURE.md`.
