---
"@stainless-code/verdict": minor
---

`verify` can now reuse installed dependencies instead of installing them on every run. List gitignored directories in `.verdict/config.json` as `"verify": {"share": ["node_modules"]}`, and each one appears in the clean snapshot at the same path. A PR bundle reads the list at its base commit; a local bundle reads it at its mode base (`HEAD` for `fetch-local`).

- **Read-only only under `--sandbox`.** On Linux, bwrap binds the checked directory read-only from an open handle, so renaming it or swapping in a link afterwards changes nothing. On macOS, Seatbelt denies writes to its real path. Under `--sandbox-network-only` or with no sandbox, the snapshot links to the directory and writes reach your checkout.
- **Refused entries.** An entry that is tracked, not gitignored, uses `..`, is absolute, passes through a symlink, or sits under `.git` or `.verdict` refuses `bad-config`, and nothing runs. A directory the checkout lacks is skipped with a warning.
- **Recorded and shown.** The verify receipt, the group receipt and the `--json` envelope list every shared path in `shared`. `verify` prints the shared directories and whether they were read-only, and `gate`'s evidence detail notes a VERIFIED claim that rests on such a receipt, without failing it.
- **Proposed by `init`.** `verdict init` adds `node_modules` when it finds `package-lock.json`, `pnpm-lock.yaml` or `yarn.lock`, and `.venv` for `uv.lock` or `poetry.lock`, but only when git already ignores the directory and tracks nothing in it.

Sharing trades the clean-snapshot guarantee for speed: a stub planted in a shared directory could make a command pass. Leave the list empty to keep the snapshot clean.
