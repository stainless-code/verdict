---
"@stainless-code/verdict": minor
---

`verdict mcp`, `verdict serve`, and `verdict lsp` now share one write-posture rule, and `--read-only` is its only knob. The line is the transport. The stdio surfaces (`mcp`, `lsp`) get CLI parity: local writes (triage, KB promotion, sandboxed `verify`, `record`, `adopt`, `fix`) and host writes (`post`, the `threads` resolve mode) are on by default, and `--read-only` removes every write. `serve` is reachable by any local process, so it serves local writes by default behind its token, session-cookie, CSRF, Host, and Origin guards, while host posting stays opt-in through `--allow-post` (the preview, nonce, and digest flow is unchanged); `--read-only` removes every write and refuses with `--allow-post`. `lsp` accepts `--read-only` and has no write path. `serve` no longer allowlists a wildcard bind's own spelling (`0.0.0.0`, `::`) as a Host or Origin, as the manual already promised.

**Breaking, pre-1.0, no aliases.** The retired flags are now unknown-flag usage errors, and `mcp config` no longer recognizes a `["mcp", "--operator"]` entry as its own (it refuses `mcp-config-foreign`; edit the entry or delete it, then re-run `--write`).

| Before                                                                                    | Now                                                                                    |
| ----------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------- |
| `mcp --operator` (to get `triage` or `threads` resolve)                                   | `mcp` — both are on by default; add `--read-only` to remove them and every other write |
| `mcp config --operator`                                                                   | `mcp config`                                                                           |
| `serve --interactive` (to get triage, KB promotion, and the verify and post-preview jobs) | `serve` — on by default; `--read-only` gives the old GET-only surface                  |
| `serve --interactive --allow-post`                                                        | `serve --allow-post`                                                                   |
| `serve` (read-only by default)                                                            | `serve --read-only`                                                                    |
| `mcp --read-only --operator` (usage error)                                                | `mcp --read-only`                                                                      |

An agent's MCP server that must not call `triage` should start with `--read-only`.
