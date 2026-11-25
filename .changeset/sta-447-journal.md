---
"@stainless-code/verdict": minor
---

verdict now keeps an append-only journal per bundle under `<state>/verdict/journal/` (`$XDG_STATE_HOME`, else `~/.local/state`, or `%LOCALAPPDATA%` on Windows), outside your repository. Every bundle-writing CLI and MCP call records a start and end line: the repository, redacted argv, before and after bundle digests, the exact paths written or removed with their digests, the exit and refusal codes, and the `next` hint. The journal is capped at 1 MiB and trimmed by whole lines. The new read-only `verdict journal <owner/repo> <pr|local-name> [--json]` verb (also in MCP, the Go SDK and the TypeScript client) prints the folded history.
