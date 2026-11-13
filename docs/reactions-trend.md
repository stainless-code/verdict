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
