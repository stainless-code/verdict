---
"@stainless-code/verdict": minor
---

`verify --sandbox` runs the verify command inside an enforced bubblewrap
sandbox on Linux: no network, a private PID namespace with its own `/proc`
(so no host PID or `/proc/<pid>/environ` is readable), an empty `/run` (so
`docker.sock`, the session bus, and agent sockets have no unix-socket route
out), a fresh `/dev`, a private `/tmp`, `--new-session`, and
`--die-with-parent`. `~/.aws`, `~/.ssh`, `~/.config/gh`, `~/.docker`,
`~/.kube`, `~/.config/gcloud`, `~/.azure`, and `~/.gnupg` are mounted empty;
`~/.netrc`, `~/.git-credentials`, `~/.npmrc`, and `~/.pypirc` are bound to
`/dev/null`; the reviewed root stays writable. It probes the same flags with
a no-op before anything executes and refuses `sandbox-unavailable` — running
nothing and leaving `patch.json` and `.verify.out` untouched — on every other
OS, when `bwrap` is missing, or when namespaces are restricted; it never
silently falls back to an unsandboxed run. The opt-in reaches the MCP tool
(`sandbox`), the Go facade (`Sandbox`), and the TypeScript client
(`--sandbox` argv); the patch record, deep-group receipt, and `--json` data
carry `sandboxed` when it was enforced. Runs without the flag are
byte-identical to before.
