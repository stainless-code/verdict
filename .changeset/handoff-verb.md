---
"@stainless-code/verdict": minor
---

`verdict handoff <owner/repo> <pr> [--min-severity <s>] [--paths <glob>]...
[--format md] [--json]` prints one markdown projection of the open inline
findings `fix` would take, for a human or a different agent to pick up where
`fix` stops. It selects through the same selector `fix --all` uses, so the two
verbs cannot pick different work, and each row carries the full sig, the
`path:line` locator, the severity and rigor rung, the fix text and code
suggestion, the cited KB rule ids, the verify command the bundle records, and
the `brief` block `fix` prints — the identical bytes, because both verbs render
it from one function.

`--json` carries the same rows and the same `brief_text`; the markdown and the
envelope also carry one caution line, before any quoted prose, that finding text
quotes PR content an attacker controls. `--min-severity` defaults to
`important` (`off` for no floor) and repeatable portable `--paths` globs narrow
the projection. Nothing open at the floor refuses `no-open-findings`, naming the
floor and the globs. `--format md` — the default — also beats a
`REVIEW_OUTPUT_FORMAT=json` exported for the whole job, because flags outrank
env; passing both `--json` and `--format md` is a usage error rather than a
silent winner.

The verb is read-only: no ledger, no patch, no store write, no host call. The
MCP `handoff` tool serves the same `data` and is declared non-mutating; the Go
facade gains `Client.Handoff` and the TypeScript client a `handoff` method.
