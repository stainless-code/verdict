---
"@stainless-code/verdict": patch
---

`verdict pack add` accepts only the three published git source forms — `https://host/path`
with no userinfo, `ssh://user@host/path`, and `user@host:path` — and refuses every other
remote-shaped string before any git call or write. The grammar is one allowlist shared by
`pack add` and `.verdict/verdict.lock`, so the one-slash scheme, `%`-escaped passwords,
token-only and two-`@` https URLs, `http://`, `git+https://`, and IPv6 literals with
userinfo all refuse as `bad-pack-source`, and a hand-written lock row carrying one refuses
as `bad-lock-source` at `pack list`, `fetch`, `post`, and `record`. No source is echoed raw: every refusal, human
line, and `pack list` row renders through one display that prints a parsed source and
`<invalid source>` for anything else, and git's own failure text is dropped for a fixed
hint when it carries an `@`.
