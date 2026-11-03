---
"@stainless-code/verdict": patch
---

The JSON Schema checker is one implementation again. The validator moved out of
`schemagen`'s tests into `internal/schemacheck` — now with `pattern`,
`minLength`/`maxLength`, schema-valued `additionalProperties`, and cross-file
`$ref` — and the shipped `schemas/validate.sh` is a shim that hands its
arguments to the hidden `verdict __validate-schema` verb, so it needs no `jq`
and exits with the checker's own status. Same CLI (`<schema[#/pointer]>
<doc.json>`), same exit codes (0 valid, 1 rejected, 2 bad usage), same `FAIL`
lines; the Go conformance tests now reject an envelope with
`at: "not-a-date"` and a usage record with `usd: -1`.
