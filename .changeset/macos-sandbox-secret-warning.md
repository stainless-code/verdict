---
"@stainless-code/verdict": patch
---

On macOS, `verify --sandbox` now warns when verdict's own environment carries secret-named variables. That covers `verify --group … --sandbox` and `verdict loop --sandbox` too. Seatbelt cannot gate the `kern.procargs2` sysctl, so a sandboxed command can read the environment of verdict's own process. The warning uses the same rule as the tail redactor: a secret-shaped name such as `GH_TOKEN` with a value of at least 8 bytes. It names the variables, never their values, and points at the Linux-VM `bwrap` recipe. It goes to the diagnostic stream and to `warnings` under `--json`. `--sandbox-network-only` carries the same warning, since it keeps the host `/proc` readable. Linux `bwrap` runs never do.
