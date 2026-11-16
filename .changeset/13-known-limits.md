---
"@stainless-code/verdict": patch
---

**Known limits**

- **The macOS sandbox covers the network half only** — `verify --sandbox` denies `bind()` and `connect()` on macOS but has no filesystem namespace, so credential paths, `/proc`, `/run`, and a private `/tmp` are hidden only on Linux under `bubblewrap`; filesystem isolation on macOS needs a Linux VM or a container running that recipe. [Enforced sandbox](docs/manual.md#enforced-sandbox-verify---sandbox) <!-- changesets: verify-sandbox-macos.md -->
- **The string gate is a tripwire, not a boundary** — `sh "$V"`, a script named by a variable, still passes the pre-exec scan, which is why `verify --sandbox` is the boundary and a spelling outside the gate's reach is a gate gap to arm rather than a vulnerability. [Known residuals](docs/manual.md#known-residuals-string-scan-ceiling) <!-- changesets: verify-static-gate-bypasses.md, verify-indirection.md -->
- **A backslash before a CRLF line ending is not joined as a continuation** — the gate refuses that shape rather than passing it, so a CRLF continuation fails safe. [Known residuals](docs/manual.md#known-residuals-string-scan-ceiling) <!-- changesets: verify-indirection.md -->
