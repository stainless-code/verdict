---
"@stainless-code/verdict": minor
---

Make `data.capabilities` honest. The binding verdict splits into `approve` and
`request_changes`, because hosts differ: GitHub carries both, while GitLab
approves a merge request but has no request-changes review event — a bound
request-changes there still posts the discussions and summary note, and says in
a warning that the verdict did not bind. `review_verdicts` stays in place as
the compatibility sum `approve && request_changes`, so a consumer written before
the split reads the same truth; new readers branch on the two flags.
`thread_resolution` reads true on GitHub and GitLab, where `threads resolve`
closes a verdict-authored thread, and false on Forgejo, which has no documented
resolve endpoint, and on the fetch-only hosts. The new `unsupported` object
carries one machine-readable
reason code per absent capability (`fetch-only`, `no-request-changes-event`,
`no-approve-verdict`, `no-inline-anchors`, `no-resolve-mutation`,
`unknown-host`). The published envelope schema types `capabilities` instead of
leaving it open; only `provider` and `fetch` stay required, so an envelope
written before the split still validates while every new fetch emits the full
object.
