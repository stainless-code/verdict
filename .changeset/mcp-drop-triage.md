---
"@stainless-code/verdict": patch
---

Drop `triage` from the MCP tool list. `SKILL.md` already tells agents never to
call it (status changes are proposed through `triage[]` in `review.json`), so
the stdio server now exposes 17 tools and leaves `verdict triage` as the
operator's CLI verb. The `--read-only` set is unchanged.
