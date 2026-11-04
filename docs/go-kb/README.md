# Go KB — `knowledge-base.md`

`knowledge-base.md` is the canonical Go knowledge base: 131 tiered cards (`GO-<TOPIC>-<NN>`,
MUST/SHOULD/CONSIDER) with Rule / Why / Enforce / Since / Sources / Verified, the resolved
decisions, and the `from A-…` source-ID map. Every ID that `docs/go-rules.md` cites exists here.

The four `go-*` skills embed a copy at `references/knowledge-base.md`, in both locations the
harnesses read: `~/.claude/skills/{go-write,go-test,go-review,go-cli-hardening}` (Claude Code) and
`~/.agents/skills/{…}` (Pi/agents). The copies are byte-identical to this file
(`md5 docs/go-kb/knowledge-base.md ~/.agents/skills/go-write/references/knowledge-base.md`)
— edit here first, then copy into each skill's `references/`; a stale copy anywhere is a bug,
not a variant.

`skills/`, `research/`, `rules/`, `ci/` and `lint/*.yml` are deleted: the repo's `.golangci.yml` and
`.github/workflows/ci.yml` are the truth for what is enforced, and they have diverged since.
