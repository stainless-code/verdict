---
"@stainless-code/verdict": minor
---

verdict now renders the review body itself, from the structured findings, bundle metadata, evidence receipts, spec checks and approval decision. Agents no longer follow body-formatting rules.

The body opens with a verdict lead (`✅ No new issues found.`, `ℹ️ Informational findings only.`, `⚠️ Issues found`, or `⛔ Evidence checks insufficient`) that `summary` cannot override. A `Decision: withheld — <reason>` line follows when approval is withheld, then a labeled `Basis:` line matching `verdict report`, and `Safety:` and `Risk:` lines when set. `summary` renders as one sanitized plain-text paragraph. Findings are ranked and show severity, rigor and category, `[path:line]` permalinks pinned to the reviewed commit on GitHub, a label on unverified quotes, and the `brief` collapsed under `Technical details`. Unanchored findings render once under `Unanchored observations`, and withheld findings are left out. Clean reviews add `What we checked`, insufficient ones `Evidence gaps`, and graded specs `Spec checks`. A review metadata comment is always appended. Behavioral mermaid diagrams are retired from agent reviews; structural diagrams in `report --overview` are unchanged.

**Breaking:** the `degenerate-body` refusal is removed, since verdict owns the body end to end. When `post` splits a body over 65,536 bytes, each comment carries its own `[ev-N]` link definitions.
