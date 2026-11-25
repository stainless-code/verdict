---
"@stainless-code/verdict": minor
---

`verify` can now reuse installed dependencies instead of installing them on every run. List gitignored directories in `.verdict/config.json` as `"verify": {"share": ["node_modules"]}`. The list is read at the PR base, and each directory from your checkout appears in the clean snapshot at the same path.

- **Read-only under `--sandbox`.** On Linux, bwrap binds the directory read-only. On macOS, Seatbelt denies writes to its real path. Without `--sandbox`, the snapshot links to the directory and writes reach your checkout.
- **Refused entries.** An entry that is tracked, not gitignored, uses `..`, is absolute, or resolves outside the checkout through a link refuses `bad-config` and nothing runs. A directory the checkout does not have yet is skipped with a warning.
- **Recorded.** The verify receipt, the group validation receipt, and the `--json` envelope list every shared path in `shared`, sorted. The field is absent when the snapshot was clean.
- **Proposed by `init`.** `verdict init` adds `node_modules` when it finds `package-lock.json`, `pnpm-lock.yaml`, or `yarn.lock`, and adds `.venv` for `uv.lock` or `poetry.lock`.

Sharing trades the clean-snapshot guarantee for speed: a stub planted in a shared directory could make a command pass. Leave the list empty to keep the clean snapshot.
