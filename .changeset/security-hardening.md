---
"@stainless-code/verdict": patch
---

Credential and untrusted-input hardening. Every credential-bearing provider
read is bound to the configured origin: `fetch` refuses a Bitbucket pagination
`next`, a Forgejo metadata `diff_url`, or an Azure metadata `remoteUrl` whose
scheme and host differ from the API base, instead of sending the token to that
host, and Bitbucket paging stops at a page cap or a repeated cursor with a
warning that the bundle may be incomplete. Command-stderr sidecars written by
`fetch` and `post` (`.diff.err`, `.post.err`, `.recheck.err`, provider `.err`)
are now owner-only `0600`, since they can carry token-adjacent forge
diagnostics. Untrusted input is capped: `record` and `post` refuse an oversized
agent file while reading it instead of after allocating the whole document,
`verify` and the fetch runners cap captured child output (a capped proof
records a truncation marker instead of growing the process), and every store
reader rejects oversized documents while keeping its own refusal code. A review
anchor, knowledge-base counter, override threshold, adopt anchor, verify
timeout, or read number that does not fit in an integer now takes its typed
refusal instead of wrapping into a nonsense negative span or count.
