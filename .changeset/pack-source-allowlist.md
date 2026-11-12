---
"@stainless-code/verdict": patch
---

`verdict pack add` accepts only the three published git source forms — `https://host/path`
with no userinfo, `ssh://user@host/path`, and `user@host:path` — and refuses every other
remote-shaped string before any git call or write. The grammar is one allowlist shared by
`pack add` and `.verdict/verdict.lock`, so the one-slash scheme, `%`-escaped passwords,
token-only and two-`@` https URLs, `http://`, `git+https://`, IPv6 literals with
userinfo, and a query or fragment (`?private_token=…`, `#…`) all refuse as
`bad-pack-source`, and a hand-written lock row carrying one refuses
as `bad-lock-source` at `pack list`, `fetch`, `post`, and `record`. No source is echoed
raw: every refusal, human line, and `pack list` row renders through one display that
prints a parsed source and `<invalid source>` for anything else. git's own failure text is
never rendered: a failed clone, rev-parse, or checkout refuses as `pack-git-failed` with
fixed wording for the phase and its exit code, because that text can carry the source URL,
its query, or the ref.
