---
"@stainless-code/verdict": minor
---

`fetch` now brings a PR's linked issue into the bundle as a spec source. On GitHub, a closing keyword in the title or body (`Fixes #12`, `Closes acme/app#7`) reads the linked issues, and each lands in `context.json` with `role: spec`, its URL as `source`, and its title and body as the document.

`--issue <ref>` (repeatable, `fetch` only) adds an issue explicitly: `owner/repo#12` or an issue URL through `gh`, or a bare `#12` on GitHub. Issues in other trackers (Linear, Jira, …) go in through `--spec <file>`.

An unreadable issue is a non-fatal warning (`issue-unavailable`, `issue-truncated`, `issue-over-cap`), never a silent skip or a failed fetch; a malformed operand refuses `issue-invalid`. Issues count toward the spec caps: an explicit `--issue` over the cap refuses `spec-too-large`, while a linked issue over it is demoted to `role: context`, so a PR body cannot break a fetch.
