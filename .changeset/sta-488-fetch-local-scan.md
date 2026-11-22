---
"@stainless-code/verdict": minor
---

`verdict fetch-local --scan <path>` reviews files instead of a git change: a
file operand selects that file, a directory operand walks every regular file
beneath it, and the bundle carries them as synthesized added-file diffs, so a
clean tree with nothing uncommitted still produces a reviewable bundle. A
relative path resolves from the current directory and must stay inside the
repository root. A `.git` or `.verdict` entry is never read at any depth and
whatever its mode — a linked worktree's `.git` is a regular file — symlinks are
neither followed nor selected, binary files contribute git's marker line and no
content, and a path carrying a tab or newline is skipped with a warning.

The selector is fail-closed. Each operand spelling is validated component by
component through confined, no-follow handles, so a `..` never collapses away a
link it crossed, and a case variant that aliases `.git` or `.verdict` on a
case-insensitive volume refuses. It refuses `bad-scan` for a path outside the
root, a symlink at all, a link between the root and the operand, a managed
directory, an unreadable file, or a tree whose synthesized diff would exceed
the bundle read cap, and
`bad-flags` when combined with a diff source
(`--staged`/`--range`/`--base`/`--stash`), `--worktree`, or `-- paths`. The
un-overridable `default:secret`/`secret-builtin` floor, `.verdict/ignore`,
`--ignore`, generated and attribute exclusions, personal privacy exclusions,
and the byte cap and `--chunk` gates apply exactly as on a diff run, and
`--scan <path> --preview` writes no bundle directory or file while returning
the included files, the withheld ones with their rule, the byte totals, and the
review budget tier.

The bundle is an ordinary local bundle: `source.json` records `kind: "scan"`,
`head_sha: null`, and the checked-out commit as `base_sha` (`null` in a
repository with no commit yet, which a scan needs: the controls snapshot records
the same empty base and only that headless bundle can verify against it), with
every standard
artifact (`full.diff`, `hunks.tsv`, `toc.txt`, `roles.txt`, `excluded.json`,
`run.json`, `provenance.json`, `identity.json`, `lenses.txt`), so `record`,
`report` (including `--sarif`), `gate`, and `serve` read it unchanged. A local
bundle with no head now keys its coverage receipt to its own verified diff
digest, so a scan gates on the coverage it recorded instead of failing unknown.

The flag reaches the Go SDK (`FetchLocalRequest.Scan`), the MCP `fetch-local`
tool (`flags.scan`), shell completion, and the usage block; `bad-scan` joins
the refusal-code registry and the generated TypeScript union.
