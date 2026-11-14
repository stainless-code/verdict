---
"@stainless-code/verdict": minor
---

Publish a shared truncation receipt across CLI `--json` `data` envelopes (`fetch`, `report`, `next`, `show`, `trend`, `threads`, and `serve`), bundle artifacts, and MCP text/structured results, and make `next`/`show` report truncated misses honestly instead of refusing or claiming nothing exists.

Breaking (pre-1.0, within schema_version "1" by operator decision): index
blast-radius lists replace the integer `truncated` count and `depth_truncated`
with the shared boolean receipt (`truncated`, `truncation_reason`, `returned`,
`total`, `next_offset`); complete results omit the receipt instead of carrying
`truncated: false`.
