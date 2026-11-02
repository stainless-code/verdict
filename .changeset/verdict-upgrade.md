---
"@stainless-code/verdict": minor
---

Explicit, channel-aware self-updates. `verdict upgrade` resolves the running
binary through symlinks and acts without a prompt: a verified self-replace
(cosign bundle when present, SHA-256 always, `--version` smoke test, atomic
replace with rollback; Windows uses a helper that waits for the old process)
for installer installs, the owning manager's global update for
npm/pnpm/bun/yarn/brew, and a typed refusal with the exact command for
project-local, skill-copy, sudo/admin, and unidentifiable installs. Flags:
`--version <v>`, `--dry-run`, `--json`. `verdict uninstall` removes only
installer-managed files (plus the marked PATH line with `--remove-path`) and
refuses other channels with their owning removal command. There is no
background check, no nag, and no review-path call.
