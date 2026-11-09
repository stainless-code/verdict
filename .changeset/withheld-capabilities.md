---
"@stainless-code/verdict": minor
---

`post.json` records what a host withheld, so a degraded publish stays auditable
after the envelope is gone. The new optional `withheld[]` lists one entry per
capability the host could not carry, with the capability name and the same
machine-readable reason code `fetch` publishes in `data.capabilities.unsupported`
(`no-request-changes-event`, `no-approve-verdict`, `no-inline-anchors`, …) plus
the `requested_event` the run asked for. A bound `REQUEST_CHANGES` on GitLab
therefore leaves `review.state=commented` — the state the host accepted — _and_
an entry saying `request_changes` was withheld, so it is no longer
indistinguishable from a plain comment; the key is absent when nothing was
withheld, and `schema_version` stays `"1"` (additive: no field renamed or
removed). The `inline_anchors` capability now has a gate behind its claim: a
host advertising `inline_anchors: false` is sent no anchored comment, and the
merge keeps every finding in the body (keep-biased) with a warning naming the
host, instead of writing a position the host would refuse.
