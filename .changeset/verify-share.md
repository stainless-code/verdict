---
"@stainless-code/verdict": minor
---

`verify` can now reuse installed dependencies instead of installing them on every run. List gitignored directories in `.verdict/config.json` as `"verify": {"share": ["node_modules"]}`. Each directory from your checkout appears in the clean snapshot at the same path. A PR bundle reads the list at its base commit; a local bundle reads it at its mode base (`HEAD` for `fetch-local`).

- **Read-only under `--sandbox` only.** On Linux, bwrap binds read-only the directory verify checked, held open from the check (`--ro-bind-fd`), so renaming it or swapping in a link afterwards cannot change what is bound. On macOS, Seatbelt denies writes to its real path. Under `--sandbox-network-only` or without a sandbox, the snapshot links to the directory and writes reach your checkout.
- **Refused entries.** An entry that is tracked (in any letter case), not gitignored, uses `..`, is absolute, passes through a symbolic link, or sits under `.git` or `.verdict` refuses `bad-config` and nothing runs. A directory the checkout does not have yet is skipped with a warning.
- **Recorded.** The verify receipt, the group validation receipt, and the `--json` envelope list every shared path in `shared`, sorted. The field is absent when the snapshot was clean.
- **Proposed by `init`.** `verdict init` adds `node_modules` when it finds `package-lock.json`, `pnpm-lock.yaml`, or `yarn.lock`, and adds `.venv` for `uv.lock` or `poetry.lock`, but only when git already ignores the directory and tracks nothing in it.

Sharing trades the clean-snapshot guarantee for speed: a stub planted in a shared directory could make a command pass. Leave the list empty to keep the clean snapshot.
