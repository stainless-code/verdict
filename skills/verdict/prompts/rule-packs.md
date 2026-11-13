# Rule pack import

Run `verdict learn --pack <explicit-directory> --id <rule-id> --json` to import
one declared rule into `.verdict/kb.jsonl`. The pack row needs `category`,
`description`, and nonempty `examples`, alongside severity and scopes.
Unknown IDs and duplicates refuse. No pack search occurs. Commit the KB file
so bundle controls record its provenance and content hash. Inspect the object
with `learn --status --json`.
