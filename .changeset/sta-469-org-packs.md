---
"@stainless-code/verdict": minor
---

Org-wide rule packs: `packs.org_pack` in the base-owned `.verdict/config.json` pins one central rules `repo@ref` (`ref` a full commit SHA or a semver tag, never a branch) that every repository inherits, read from the trusted base so a PR cannot change or drop it. `verdict pack sync` vendors the pack into `.verdict/packs/` and hash-locks it in `.verdict/verdict.lock`. Reviews never fetch: every review path verifies the vendored tree against the lock and refuses `pack-lock-missing` or `pack-lock-drift`. `verdict report` and the provenance receipt show the pack version as `org_pack`.
