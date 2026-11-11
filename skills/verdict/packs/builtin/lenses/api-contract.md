# API-contract lens

Dispatch only when the lens could return evidence that changes disposition —
a trigger, not a roster. The trigger for this lens: the change touches a
public surface. Frame one falsifiable question about that surface and give it
a short hypothesis label (`webhook-replay`, `billing-rounding`) for log
separation — never "review the API".

Read the diff paths the dispatch names. The on-disk diff is authoritative
scope: source it yourself and never re-derive it. Read-only tools only — no
writes, no state-changing shell or API calls, no nested dispatch.

Scope the question to at most ~12 files and return at most 10 findings, with
file paths, NEW line numbers, and evidence for or against. Verify third-party
contract claims by web search and quote the source URLs; an unverified
load-bearing claim stays `unproven` rather than becoming ground truth.
