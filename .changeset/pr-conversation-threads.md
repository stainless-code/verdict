---
"@stainless-code/verdict": minor
---

The PR conversation is now a local, schema'd contract. Every provider bundle
writes `.review/<pr>/threads.json` (`schemas/threads.schema.json`): each
thread's host id, diff anchor, `resolved`/`outdated` read from the host's own
fields, root author kind, optional `finding_sig`, and the comments with
authors, timestamps, and 8 KiB-capped bodies. GitHub comes from GraphQL
`reviewThreads` (REST carries no resolution); GitLab discussions, Azure thread
status, Forgejo resolvers, Bitbucket resolution, and GitCafe's flags fill the
same shape, and every read pages with the shared walker instead of silently
truncating. `verdict threads <owner/repo> <pr> [--provider …] [--json]`
re-reads just the thread surface and rewrites only that file, so re-review,
address-reviews, readiness, and calibration can join prior feedback on any of
the six hosts. Posted inline comments now carry a hidden final-line
`<!-- verdict-finding sig=… -->` marker (backward compatible: older comments
read `finding_sig: null`), and the `threads` MCP tool exposes the refresh in
the read-only surface.
