---
"@stainless-code/verdict": minor
---

The binary serves and installs its own skill. `verdict skill` prints
`SKILL.md`, `verdict skill files` lists the embedded payload, and
`verdict skill install [--dir <skills-dir>|--agent <name>] [--force]
[--dry-run] [--json]` writes `SKILL.md`, `prompts/`, `schemas/`, and the
POSIX `scripts/verdict` shim atomically with a `.verdict-managed` receipt
(create/update/keep/blocked preview, typed foreign-dir refusal, no prompts,
no network). The payload is embedded with `go:embed`, so the served skill can
never skew from the binary. Skill copies resolve `VERDICT_BIN`, then
`<skill>/bin/verdict`, then `verdict` on `PATH`; otherwise
`scripts/verdict bootstrap` fetches the pinned release's verified installer
once and never fetches during a review. README and `SKILL.md` now point
`npx skills` users at the global binary as the primary path and the
shim/bootstrap as the fallback.
