---
"@stainless-code/verdict": minor
---

`verdict skill` grows its inspection and removal half. `skill show <path>`
prints one embedded payload file byte-identically (with `--json`, one envelope
carrying `path` and the same bytes in `content`); a traversal, absolute, or
unknown path refuses `skill-path-not-inventory` and names `skill files`.
`skill status [--dir|--agent...|--detect] [--diff] [--json]` is the read-only
drift report: it writes nothing, reuses the install plan to report
create/update/keep/blocked per target with the receipt versus embedded version,
and exits 0 in sync and 1 when drifted, so CI can gate without
`install --dry-run`. `--diff` adds one unified diff per changed file, capped at
64 KiB overall with an explicit truncation marker and `truncated` in JSON.
`--agent` now repeats on `install`, `status`, and `uninstall`: targets are
deduplicated and handled in argv order, while `--dir` stays single.
`skill uninstall [--dir|--agent...] [--force] [--dry-run] [--json]` deletes
only the inventory paths the `.verdict-managed` receipt owns, then the receipt,
and leaves every foreign file in place; a target that is a symlink
(`skill-target-file`) or an inventory path that escapes the target through a
symlinked directory (`skill-foreign-conflict`) refuses before any deletion; a
target without a receipt refuses
`skill-dir-foreign` unless `--force` (which still removes only inventory
paths), an absent install refuses `skill-not-installed`, and `--dry-run`
deletes nothing. `skill install` likewise refuses a symlinked target
(`skill-target-file`) and a payload directory that escapes the target through
a symlink (`skill-foreign-conflict`) before it writes anything.
`verdict uninstall`'s skill-copy hint now names
`verdict skill uninstall` instead of `rm -rf`.
