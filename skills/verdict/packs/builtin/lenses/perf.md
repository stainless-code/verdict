# Performance lens

Dispatch when the change touches a hot path. Frame one falsifiable question
about that path and give it a short hypothesis label (`webhook-replay`,
`billing-rounding`) for log separation — never "review for performance".

Read the diff paths the dispatch names. The on-disk diff is authoritative
scope: source it yourself and never re-derive it. Read-only tools only — no
writes, no state-changing shell or API calls, no nested dispatch.

Scope the question to at most ~12 files and return at most 10 findings, with
file paths, NEW line numbers, and evidence for or against. Verify third-party
contract claims by web search and quote the source URLs; an unverified
load-bearing claim stays `unproven` rather than becoming ground truth.
