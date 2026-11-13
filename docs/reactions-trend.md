# `calibrate --reactions` and `trend --rules`

Use `calibrate OWNER/REPO PR --reactions --json` to inspect helpful signals on
posted GitHub review comments. Add `--reaction-reason TEXT` only after a human
supplies a reason for a false-positive proposal. Review `data.proposals` — a
vote is evidence, never an instruction. Submit accepted `claim` objects through
the existing review triage claims or `triage` with the same rationale. No finding
or KB counter changes during the reaction read. GitLab is explicitly unsupported.

Use `trend OWNER/REPO --rules --json` for sorted per-rule finding outcomes.
`accepted` counts addressed findings; `false_hits` counts false-positive findings.
Both rates divide by `judged`, their sum — zero judged produces null rates.
Other finding statuses count toward `cited` only. Duplicate citations in a finding
count once.

Reaction proposals exclude accounts with GitHub `user.type == "Bot"`, logins
ending in `[bot]` (case insensitive), and the account that authored the posted
review comment. Bot classification takes precedence over self classification.
`excluded.bot` and `excluded.self` report the dropped reaction counts. PR-author
reactions remain eligible. Every proposal records `reactor` (the login) and
`role`: `pr-author`, `reviewer` for a known review-comment author, or `other`.
Roles describe the available evidence — they do not grant approval. Identity
reads are paginated; failed reads refuse instead of falling back to totals.
