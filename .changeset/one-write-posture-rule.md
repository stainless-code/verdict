---
"@stainless-code/verdict": minor
---

**Breaking:** `verdict mcp`, `verdict serve` and `verdict lsp` share one write-posture rule, and `--read-only` is the only switch.

- **stdio (`mcp`, `lsp`) has CLI parity.** Local writes (triage, KB promotion, sandboxed `verify`, `record`, `adopt`, `fix`) and host writes (`post`, resolving threads) are on by default. `--read-only` removes every write. `lsp` has no write path.
- **`serve` writes only on loopback.** Local writes are on by default behind its token, session-cookie, CSRF, Host and Origin checks. Host posting needs `--allow-post`, with its preview, nonce and digest flow. `--read-only` removes every write and refuses `--allow-post`.
- **A non-loopback `--host` serves read-only,** warns why, and refuses `--allow-post`. No flag turns remote writes on, because the token would cross plain HTTP.
- A wildcard bind no longer allowlists its own spelling (`0.0.0.0`, `::`) as a Host or Origin, and prints a start URL on `127.0.0.1` or `::1`.

The removed flags are unknown-flag usage errors. `mcp config` no longer treats a `["mcp", "--operator"]` entry as its own (`mcp-config-foreign`): edit or delete it, then re-run `--write`. An agent's MCP server that must not call `triage` should start with `--read-only`.

| Before                                | Now                                                |
| ------------------------------------- | -------------------------------------------------- |
| `mcp --operator`                      | `mcp` (add `--read-only` to remove every write)    |
| `mcp config --operator`               | `mcp config`                                       |
| `serve --interactive`                 | `serve`                                            |
| `serve --interactive --allow-post`    | `serve --allow-post`                               |
| `serve` (read-only)                   | `serve --read-only`                                |
| `serve --interactive --host <lan-ip>` | read-only, no replacement; bind loopback to triage |
