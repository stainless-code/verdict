---
"@stainless-code/verdict": patch
---

The shipped `schemas/validate.sh` now hands its arguments to the hidden
`verdict __validate-schema` verb, so it needs no `jq` and exits with the
checker's own status. The checker also grew `pattern`, `minLength`/`maxLength`,
schema-valued `additionalProperties`, and cross-file `$ref`. Same CLI
(`<schema[#/pointer]> <doc.json>`), same exit codes (0 valid, 1 rejected, 2 bad
usage), same `FAIL` lines.
