---
"@stainless-code/verdict": patch
---

`verify --sandbox` now enforces the sandbox on macOS as well as Linux.
macOS runs the command under the built-in `sandbox-exec` with
`(version 1)(allow default)(deny network*)`: every `bind()` and `connect()`
is denied, loopback and unix sockets included, so a command that
listens or reaches a listener fails. macOS has no filesystem namespace, so
the mount shape stays Linux-only — credential paths, `/proc`, `/run`, and a
private `/tmp` are still hidden by `bubblewrap` on Linux, and on macOS
filesystem isolation needs a Linux VM or container running that recipe. The same probe runs before anything
executes, and a host with no mechanism (Windows included), a missing runner,
or a runner that cannot build its sandbox still refuses `sandbox-unavailable`
and runs nothing. The manual and `docs/troubleshooting.md` now call the
static gate a tripwire rather than a boundary and name what isolates on each
platform.
