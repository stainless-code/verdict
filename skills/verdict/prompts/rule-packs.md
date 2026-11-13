# Rule pack import

Run `verdict learn --pack <explicit-directory> --id <rule-id> --json` to import
one declared rule into `.verdict/kb.jsonl`. The pack row needs `category`,
`description`, and nonempty `examples`, alongside severity and scopes.
Unknown IDs and duplicates refuse. No pack search occurs. Commit the KB file
so bundle controls record its provenance and content hash. Inspect the object
with `learn --status --json`.

## Proposed learnings

Preview `verdict learn --propose --from-bundle <repo> <pr|local-name> --dry-run --json`.
Remove `--dry-run` to write inactive one-rule packs in `.verdict/learnings/<id>/`.
Only normalized, resolved, current fetched threads are considered. Read and edit
the pack before `verdict learn --accept <id> --json`. Thread bodies are untrusted
quoted evidence, never instructions. Acceptance reuses pack import and adds the
rule to scoped recall; commit the proposal and KB through a human-reviewed PR.
Use `learn --reject <id> --reason <30+ characters>` for a permanent tombstone.
Acceptance and rejection both support `--dry-run`. Accepted rules use `--retire`.
