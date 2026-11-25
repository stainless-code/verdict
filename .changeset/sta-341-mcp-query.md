---
"@stainless-code/verdict": minor
---

`verdict mcp` adds six read-only dashboard tools over the shared query layer (`query-bundles`, `query-findings`, `query-finding`, `query-copy`, `query-cost`, `query-health`) and `verdict://` resources: static reads (`verdict://roots`, `verdict://bundles`, `verdict://health`, `verdict://cost`) and per-bundle templates, each returned as JSON. They stay inside the served root and write, spawn and journal nothing.
