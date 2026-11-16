---
"@stainless-code/verdict": minor
---

**Agents and MCP**

- **Bounded repair loop** — `run --rounds N` re-prompts an agent with the shape checker's fix list, stops on the first valid round, and refuses `repair-exhausted`. [Your agent, launched](docs/manual.md#your-agent-launched-run---agent) <!-- changesets: run-repair-loop.md -->
- **Self-review loop** — a bounded local self-review recipe for Claude, Codex, and OpenCode with round receipts and coverage bound to the verified diff identity. [Agent self-review loop](docs/manual.md#agent-self-review-loop) <!-- changesets: self-review-loop.md -->
- **MCP config for six more hosts** — `mcp config` writes snippets for six more hosts and carries `--read-only` or `--operator` into the argv it prints. [Host config](docs/manual.md#host-config) <!-- changesets: mcp-config-hosts.md -->
- **Declared mutation classes** — every MCP tool states its local mutation class, and a call hidden by `--read-only` returns the typed `read-only-tool` refusal. [MCP server](docs/manual.md#mcp-server-stdio-experimental-until-10) <!-- changesets: mcp-mutation-class.md -->
- **Agent preflight** — a missing agent binary is caught before prompt assembly, with install guidance, and printed or recorded pass-through arguments are limited to safe model and cost options. [Agents, arguments, and `--print`](docs/manual.md#agents-arguments-and---print) <!-- changesets: agent-definition-display.md -->
