---
"@stainless-code/verdict": minor
---

The TypeScript client's verb methods now return their own `data` profile
instead of `Envelope<unknown>`: `ready` resolves `ReadyData`, `fetch` resolves
`FetchData`, `trend` resolves `TrendData`, and so on for all eighteen methods.
`DataFor` exposes the same verb → profile map, so code that passes `data`
around can name the profile without restating it.

Every profile is generated as a `<verb>Data` `$defs` entry in
`envelope.schema.json` (with the nested `$defs` it references), so non-TypeScript
clients get the same shapes from the published schema. Additions only: `data`
stays an open object that allows additional properties, no existing property,
required key, or type changed, and `client.run()` still returns
`Envelope<unknown>` for verbs without a profile.
