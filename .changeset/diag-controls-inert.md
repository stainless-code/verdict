---
"@stainless-code/verdict": patch
---

Every verb's `warn:` and `error:` lines on stderr now fold control characters (other than newline and tab) to spaces. A warning or refusal that quotes a path or host message a stranger chose (a directory named with an OSC 52 sequence under `serve --root`, say) can no longer write the clipboard, move the cursor, or clear the operator's terminal. Line breaks the message means to carry are kept.
