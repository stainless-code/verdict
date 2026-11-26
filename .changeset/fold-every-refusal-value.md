---
"@stainless-code/verdict": patch
---

Every refusal now folds the line breaks and bidi controls in a value it quotes to spaces, not only `record`, `post`, and thread refusals. A flag value, a path, a config key, an agent's file, or a host's or child process's error text carrying a newline, a CRLF, or U+2028 could end the `error:` line and start a second one that read as verdict's own, in `fetch`, `verify`, `run`, `loop`, `ingest`, `triage`, `report`, `pack`, `serve`, `mcp`, and every other verb. The value is still named, a refusal that lays out several lines of its own keeps them, and the `--json` `error.message` carries the same folded text.
