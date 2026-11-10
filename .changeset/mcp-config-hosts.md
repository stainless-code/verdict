---
"@stainless-code/verdict": minor
---

`verdict mcp config` now covers six more hosts and the server's posture flags.
`--read-only` and/or `--operator` put the matching argv after `mcp` in the
printed, `--json`, and `--write` snippets. A `verdict` entry whose argv is one
of verdict's own shapes (`["mcp"]` plus any combination of the two flags) is
updated in place on `--write` with the usual backup; any other argv still
refuses typed (`mcp-config-foreign`).

New JSON `mcpServers` hosts merge with `--write`: `gemini`
(`~/.gemini/settings.json`), `qwen` (`~/.qwen/settings.json`), and `windsurf`
(`~/.codeium/windsurf/mcp_config.json`). `opencode` (`opencode.json`),
`copilot` (`.vscode/mcp.json`), and `goose` (`~/.config/goose/config.yaml`)
are print-only: the snippet renders each host's own shape and `--write`
refuses typed naming the host.
