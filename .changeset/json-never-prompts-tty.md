---
"@stainless-code/verdict": patch
---

Machine mode never prompts, even on a TTY. `init --json` and `install --json` without `--yes`/`--force` now refuse typed `needs-confirm` (exit 1, `try:` hint, nothing written) exactly like the headless path, instead of printing an interactive `Proceed?` prompt when stdin is a terminal. Human mode on a TTY still prompts. New pty legs in the `never-prompts` eval prove it hermetically.
