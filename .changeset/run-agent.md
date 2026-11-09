---
"@stainless-code/verdict": minor
---

Launch your own agent. `verdict run <owner/repo> <pr|local-name> --agent <name>`
runs the operator's agent CLI against an existing bundle — one process, one
bundle, one shot — then asserts exactly one fresh `review.json` and hands off to
the unchanged `record`/`post` validation. It never retries, re-prompts, repairs,
or reads the model's prose, and it never posts.

Agents are data: the shipped table names `claude`, `codex`, `cursor`, and
`opencode` with their binaries and non-interactive argument shapes, so a new
agent is a new row. Everything after a bare `--` passes to the agent unchanged,
`--print` renders the exact command (POSIX-quoted) and exits zero without
spawning, and `--timeout` sets the shared watchdog budget.

Every failure is typed with a hint: `agent-missing`, `agent-spawn-failed`,
`agent-timeout`, `agent-failed` (whose exit code becomes the process exit code so
CI sees the real failure), `no-review-artifact`, `stale-review-artifact`,
`unknown-agent`, and `agent-record-failed`. The launch is recorded in
`.verdict/state/<bundle>/launcher.json` — agent, binary, argv shape with secrets
redacted, timings, exit code — with the child's full output in `.run.out`.

The verb is CLI-only: spawning a local agent is not an MCP tool, a facade method,
or a TypeScript client method. Verdict invokes your agent only when you ask: no
daemon, queue, scheduler, webhook, model selection, or interpretation of model
output, and `run` itself makes no network calls.
