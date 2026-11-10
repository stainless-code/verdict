---
"@stainless-code/verdict": minor
---

`verify --sandbox` runs the verify command inside an enforced bubblewrap
sandbox on Linux: no network, `~/.aws`, `~/.ssh`, and `~/.config/gh` mounted
empty, the reviewed root writable, and a private `/tmp`. It probes the same
flags with a no-op before anything executes and refuses
`sandbox-unavailable` — running nothing and leaving `patch.json` and
`.verify.out` untouched — on every other OS, when `bwrap` is missing, or when
namespaces are restricted; it never silently falls back to an unsandboxed
run. The opt-in reaches the MCP tool (`sandbox`), the Go facade (`Sandbox`),
and the TypeScript client (`--sandbox` argv); the patch record, deep-group
receipt, and `--json` data carry `sandboxed` when it was enforced. Runs
without the flag are byte-identical to before.
