---
"@stainless-code/verdict": patch
---

`verdict upgrade` quotes the project-local `run: cd <root>` hint with the
POSIX single-quote rule, so a project path with a space or `'` pastes
resolvably; `verdict uninstall`'s sibling skill-copy refusal now reads as a
sentence (`remove it with the tool that installed the skill copy at <dir>`)
instead of rendering prose in the `remove it with:` command slot.
