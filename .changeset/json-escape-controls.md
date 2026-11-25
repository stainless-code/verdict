---
"@stainless-code/verdict": patch
---

Every JSON verdict writes now escapes the C1 controls (U+0080–U+009F) and the bidi controls (U+202A–U+202E, U+2066–U+2069) as lowercase `\uXXXX`: `--json` envelopes, MCP, ACP and LSP responses, `serve` responses, and the receipts and state files a consumer reads. Go's encoder wrote those runes raw, so a consumer printing an envelope string to a terminal could run a CSI such as U+009B or see reordered text. Envelope bytes change only for strings that carry those characters; the decoded values are unchanged.
