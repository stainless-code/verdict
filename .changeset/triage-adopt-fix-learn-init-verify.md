---
"@stainless-code/verdict": minor
---

The review loop runs in the `verdict` binary: `triage` sets a finding's status
(`open|addressed|wont-fix|false-positive|deferred`) with a rationale, offline;
`adopt` carries a local bundle's findings into a fresh PR fetch, re-anchoring
them against the PR hunks; `fix` makes one bounded pass over the top open
finding, stopping after three passes per sig or as soon as a file outside the
anchor is touched; `learn` owns the `.review/kb.jsonl` rule loop — append, list
with counters and precision, retire and restore with a reason; `init` writes
the offline control files for a `chill|standard|strict` preset; `verify` runs a
finding's `verifyCmd` under a watchdog in its own process group, redacting known
secrets from the recorded tail, and refuses destructive, networked,
credential-reading, and repo-escaping commands typed before they run.
