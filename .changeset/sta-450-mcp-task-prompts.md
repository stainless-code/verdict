---
"@stainless-code/verdict": patch
---

The MCP server now serves `build`, `ci`, and `grade` as task prompts, one per task prompt the
Procedure's mode router names, so `prompts/list` offers eight instead of five. Each body is the
same assembly the other task prompts use — `PROCEDURE.md`, `prompts/system.md`, and that task's
own prompt — and an unknown name still refuses `-32602` and lists the valid tasks.
