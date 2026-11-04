---
"@stainless-code/verdict": patch
---

`verdict mcp config --agent codex --write` reads its `config.toml` the way TOML
does, so a `verdict` table, key, or comment inside a string is text and no
longer a merge target: a file that ends inside an unterminated string, or whose
`[mcp_servers.verdict]` line opens one, refuses typed and keeps its bytes
instead of having keys appended into the literal. A table spelled inline or
dotted refuses rather than guess, the file's own CRLF or LF line endings are
kept, and an append no longer drops a trailing line of non-ASCII whitespace.
