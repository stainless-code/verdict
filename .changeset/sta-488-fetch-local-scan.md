---
"@stainless-code/verdict": minor
---

`verdict fetch-local --scan <path>` reviews files instead of a change. A file operand selects that file, a directory walks every regular file below it, and the bundle carries them as added-file diffs, so a clean tree still yields a reviewable bundle. Paths must stay inside the repository. `.git` and `.verdict` are never read at any depth (case variants included), symlinks are neither followed nor selected, binary files contribute no content, and a path with a tab or newline is skipped with a warning.

It refuses `bad-scan` for a path outside the root, a symlink, a managed directory, an unreadable file, or a scan over the bundle cap, and `bad-flags` when combined with a diff source, `--worktree` or `-- paths`. Secret, ignore, privacy and size exclusions apply as on a diff run, and `--scan <path> --preview` writes nothing. The result is an ordinary local bundle (`source.json` `kind: "scan"`) that `record`, `report`, `gate` and `serve` read unchanged, and it gates on the coverage it recorded. The flag reaches the Go SDK (`FetchLocalRequest.Scan`) and the MCP `fetch-local` tool (`flags.scan`).
