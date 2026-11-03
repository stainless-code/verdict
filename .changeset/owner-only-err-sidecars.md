---
"@stainless-code/verdict": patch
---

Write `fetch`/`post` command-stderr sidecars (`.diff.err`, `.post.err`,
`.recheck.err`, provider `.err`) owner-only (`0600`), matching `report`'s
`0600` staging: those files can carry token-adjacent forge diagnostics and
are debug residue, never published bundle content. Store files keep their
`0644` mode.
