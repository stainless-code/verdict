---
"@stainless-code/verdict": minor
---

Refuse a bundle name that is not one path element. Every verb that names a
bundle — adopt, calibrate, config explain/preview, fetch, fix, gate, index
blast-radius, next, post, ready, record, report, run, show, threads, triage,
verify, and the MCP tools that take a `pr` — resolves the name through one
shared guard before it joins `.verdict/state`, so `../other`, `a/b`, `a\b`,
`.`, `..`, and the empty name refuse `bad-flags` with one line instead of
reading outside the bundle home. The same guard refuses, on every OS, the
spellings Windows re-reads as another path or as a device: any `:`, a trailing
`.` or space, and a DOS device basename (`CON`, `PRN`, `AUX`, `NUL`, `COM1`-
`COM9`, `LPT1`-`LPT9`). `index blast-radius` reported the same input under its
own message and now renders the shared one. A PR number, a `local-` slug, and a
`corpus-` name still resolve as before.

The MCP `next` tool answers a findings store it cannot read with the typed
`corrupt-store` refusal instead of `{"item": null}`, so a caller no longer
reads a broken store as "nothing to act on".
