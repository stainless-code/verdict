---
"@stainless-code/verdict": minor
---

Org-wide rule packs: `packs.org_pack` in the base-owned `.verdict/config.json` pins one central rules `repo@ref` (`ref` a 40-hex commit SHA or a semver tag, never a branch; `path` a confined relative pack root) that every repository inherits, read from the trusted base so a PR can never change or drop it. `verdict pack sync` vendors and hash-locks that pack into `.verdict/packs/` and `.verdict/verdict.lock`; reviews never fetch — `pack list`, `pack check`, and every review path verify the vendored tree against the lock and refuse `pack-lock-missing` or `pack-lock-drift` instead of falling back. The governing central pack version shows in `verdict report` (`org_pack`, human and `--json`) and in the provenance receipt as an `org_pack` input.
