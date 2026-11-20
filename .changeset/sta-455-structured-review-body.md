---
"@stainless-code/verdict": minor
---

Verdict now renders the review body deterministically from structured findings, bundle metadata, evidence receipts, spec checks, and the derived approval decision instead of relying on agent-written body-formatting rules.

### User-visible review body changes

1. **Lead callout ownership:** Verdict always emits the opening verdict lead (`> ✅ No new issues found.`, `> ℹ️ Informational findings only.`, `> ⚠️ Issues found — see inline comments.`, or `> ⛔ Evidence checks insufficient — no approval.`). `summary` can no longer override or suppress the lead.
2. **Decision line:** Withheld and insufficient reviews add `> Decision: withheld — <reason>` (including blocking clauses when present) directly under the lead.
3. **Labeled `Basis:` line:** Counts render in labeled form (`> Basis: 0 reproduced · 0 ran · 1 walked · 2 pointed · 0 said · 1 rules cited · 0 verified fixes`), matching `verdict report`.
4. **`Safety:` and `Risk:` lead lines:** Rendered in the opening blockquote whenever `security_summary` or `risk` is set in `review.json`.
5. **Sanitized single-paragraph `summary`:** Rendered as one plain-text paragraph with line breaks collapsed and HTML tags/comments, fences, links, and `@mentions` neutralized.
6. **Deterministic review metadata comment:** Always rendered as `<!-- Review metadata ... -->` from `PR.json` (or `source.json` for local bundles), `post.json`, and `incremental.diff`.
7. **Deterministic finding ordering and presentation:** Findings are ranked via `readcommon.Rank`, carry severity emoji (`🚨`/`⚠️`/`ℹ️`), rigor/category/KB metadata, reference-style `[path:line][ev-N]` permalinks pinned to the reviewed commit on GitHub PRs (plain `` `path:line` `` on non-GitHub/local bundles, unindexed paths, and `LEFT`-side deleted-line anchors), live `(unverified — not counted)` labels for unverified quotes, and collapsed `<details><summary>Technical details</summary>` blocks rendered from `finding.brief`.
8. **Unanchored and withheld findings:** Moved findings render only under `### ℹ️ Unanchored observations` (no duplication in the numbered list, and any moved or fence-demoted `suggestion` renders inside an indented dynamic code fence); withheld findings (suppressed, excluded, below severity floor, or validation-rejected) are completely omitted from the body.
9. **Spec checks, receipts, and evidence gaps:** Renders `**Spec checks**` (violated visible, met/n/a collapsed) when `--spec` items were graded, `**What we checked**` (up to 7 files with role and `+/-`, plus hunk-receipt tally) on clean reviews, and `**Evidence gaps**` (quote/receipt/spec tallies plus up to 5 gaps) on insufficient reviews.
10. **`body.marker` telemetry:** Reports `"verdict"` on all four rendered leads, including blocking (`⚠️`) and insufficient (`⛔`) reviews.
11. **Behavioral mermaid diagrams retired from agent reviews:** Removed body-formatting and behavioral-diagram instructions from `prompts/review.md`, `prompts/incremental-review.md`, and agent handoff refs (`refs/diagrams.md`). Structural diagrams in `report --overview` are unchanged. (CLI/config behavioral-diagram flags remain accepted for now and are deferred to a follow-up cleanup.)

**Breaking changes**

- Removed the unreachable `degenerate-body` refusal code (`CheckDegenerate`) and `freshSections` body-section extraction; verdict owns the rendered body end to end.
- When `post` splits an oversized body above 65,536 bytes, each split comment now carries its own self-contained `[ev-N]` reference definitions.
