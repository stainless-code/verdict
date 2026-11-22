# Unvalidated data at a runtime boundary

TypeScript types are erased at run time. Every value that enters the program
from JSON, the network, storage, or a message channel is `any`/`unknown`
until a runtime check proves its shape.

## When to flag

- `JSON.parse(text)` result cast to an interface or type (`as User`,
  `as unknown as Config`) with no schema or guard. A cast asserts; it does
  not check.
- `as` or `any` applied to `req.body`, `req.query`, `req.params`, a
  `postMessage` payload, `localStorage.getItem`, a CSV row, or a third-party
  API response before the value reaches a decision.
- A numeric environment variable, header, or query parameter used without a
  finite check: `Number(process.env.PORT)` is `NaN` for a typo,
  `parseInt("12abc")` is `12`, and `Number("")` is `0`.
- An array from an external source used with `.map`/`.filter`/`.length`
  without an `Array.isArray` check, or a required field read without a
  presence check.
- A validator's result ignored: `schema.parse(data)` used only for its
  side-effect-free call while the unparsed `data` continues, or a
  `safeParse` result whose `success` is never checked.

## Do NOT flag

- A `JSON.parse` of a trusted, same-process constant (a bundled fixture, a
  build artifact) immediately consumed by code that owns its shape.
- Data that has already crossed a schema boundary in the same diff: a
  `zod`/`valibot`/`io-ts` parse, a hand-written `isUser` guard, or a
  `satisfies` check that is actually a runtime predicate.
- A cast used only for a TypeScript ergonomics issue after a runtime check
  (`if (!isRecord(x)) throw ...; const y = x as Record<string, string>`).
- `satisfies` and `as const` on literal values: those are compile-time
  checks, not boundary claims.
- An internal function boundary where both caller and callee are in the same
  module and the value is constructed locally, not parsed.

## Evidence

Quote the parse or cast and the sink that trusts the value. Name the field an
attacker controls and the bad value that reaches the decision.
