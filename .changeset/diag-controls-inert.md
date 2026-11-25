---
"@stainless-code/verdict": patch
---

Everything verdict writes to stderr now has its control characters (except newline and tab) folded to spaces: `warn:` and `error:` lines, `gate` lines, `fetch` split candidates, and the MCP server's progress and warnings. A spec item, diff path or directory name a stranger chose (one carrying an OSC 52 sequence, say) can no longer write your clipboard, move the cursor or clear the terminal. verdict's own colors and `upgrade`'s package-manager output are unchanged.
