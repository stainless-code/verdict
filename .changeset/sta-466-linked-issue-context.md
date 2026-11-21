---
"@stainless-code/verdict": minor
---

`fetch` now brings a PR's linked issue into the bundle as a spec source. On
GitHub, a title or body carrying a closing keyword (`Fixes #12`, `Fixes: #12`,
`Closes acme/app#7`) reads the `closingIssuesReferences` connection, unions it
with the keyword references, and writes each issue as a `context.json` row with
`role: spec`, the canonical issue URL as its `source`, and the document
`"# <title>\n\n<body>"` under `context/<NNN>.md` and in `context.txt`.

`--issue <ref>` (repeatable, `fetch` only) adds one issue explicitly:
`owner/repo#12` or a GitHub issue URL through `gh` on any provider, or a bare
`#12` on GitHub. Reads stay forge-native: an issue in another tracker (Linear,
Jira, Shortcut, ...) reaches the review as a `--spec <file>` source, not as a
second credential and API in the core.

An unreadable issue is a typed, non-fatal warning (`issue-unavailable`,
`issue-truncated`, `issue-over-cap`), never a silent skip and never a failed
fetch; a malformed operand refuses the new `issue-invalid` code. Issues count
toward the existing spec caps: an explicit `--issue` refuses `spec-too-large`
like `--spec`, while an auto-discovered issue over the cap is demoted to
`role: context`, so a PR body cannot break a fetch. A PR body cannot steer a
host read either: a closing keyword whose repository is not a slug (`Fixes
../..#12`) is not an issue link and costs no call, and an issue the run already
carries is never read or warned about twice.
