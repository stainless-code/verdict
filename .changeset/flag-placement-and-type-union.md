---
"@stainless-code/verdict": patch
"@stainless-code/verdict-ts": patch
---

The global flags no longer have to come before the verb. `--color <mode>`,
`--compact`/`--no-compact` and `--no-json` are recognized before the verb,
between the verb and the repo, and after the pr, and a bare `--` ends positional
parsing so every later token is a positional: `verdict fetch-local acme/app --
--json` is a usage error instead of a flag.

The TypeScript client's type generator handled `$ref`, `const`, `enum` and
`type` but not `anyOf`, so `fix.verify_hint` — an anyOf of null and
`$defs/verifyHint` — rendered as `unknown` and orphaned `VerifyHint`. Variants
now join into a union, and a null variant collapses to a trailing `| null`
exactly as a `type: [..., "null"]` array does; `types-check.ts` pins the result
so a regression to `unknown` or `any` fails the typecheck.

The MCP surface stops advertising two writing tools as read-only. `report`
writes `--output` and `--sarif` artifacts, so it reports `readOnlyHint: false`
and refuses its writing arguments as unknown on the read-only surface, while
`threads` — which rewrites `.review/<pr>/threads.json` — is no longer served
there at all: seven tools remain.

`prompts/review.md` and `prompts/diagrams.md` lose the corrupt copyable
skeletons an agent would emit verbatim: a 4-backtick fence opened mid-line with
its body flattened onto it, three orphan fence/closer pairs, and an unclosed
mermaid wrapper. `TestPromptBlocksBalanced` fails on the old bytes, passes on
the new ones, and covers all four hashed prompts.
