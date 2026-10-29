# CI REPORT — review without posting

Read-only loop for pipelines: full judgment, zero writes to GitHub.

1. Task list first. Fetch: `./scripts/review.sh fetch owner/repo N --since <ref>`.
2. Read the bundle exactly like `prompts/review.md` steps 2–6 (full diff via TOC,
   roles first, 0–N lenses, sound+correct+elegant bar) — but write NO review.json.
3. Write `.review/N/report.md`: severity-ordered findings (blocking → must-fix → minor),
   each with file:line + evidence + suggested scope; then the Reviewed-changes preamble
   from `prompts/review.md`. Terminal write is the report file — never call `post`.
4. Summary to stdout: counts per severity + report path. Done when the report file exists.
