---
"@stainless-code/verdict": minor
---

`verify` now filters the child process environment with a default-deny
allowlist instead of forwarding the full parent `os.Environ()`. Only baseline
locale, terminal, shell, XDG, and toolchain variables (`PATH`, `HOME`, `LANG`,
`LC_*`, `TERM`, `TMPDIR`, `TZ`, `CI`, `USER`, `LOGNAME`, `SHELL`, color flags,
`XDG_*_HOME`, Go/C/Rust/JS/Python toolchain cache and home variables, plus
Windows OS runtime variables on `GOOS=windows`) pass by default, and any
variable matching `envsecret.IsSecret` (such as `FOO_TOKEN`, `GITHUB_TOKEN`,
`SSH_AUTH_SOCK`, or `LC_TOKEN`) is dropped even when it matches an allowlist
prefix. Operators can opt specific variable names in via
`--env-allowlist <name[,name...]>` (also wired as `envAllowlist` on the `verify`
MCP tool and `EnvAllowlist` on `verdict.VerifyRequest`) or `.verdict/verify-env`
(one name per line, `#` comments and blank lines ignored, read from the
base-snapshotted `controls` inventory rather than the untrusted PR worktree).
Dropped variable names — never values — are reported in deterministic sorted
order in `warnings[]`.
