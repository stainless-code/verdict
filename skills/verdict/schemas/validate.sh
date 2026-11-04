#!/bin/sh
# schemas/validate.sh — offline JSON-Schema checker for schemas/*.json.
#
# Usage: schemas/validate.sh <schema.json[#/pointer]> <doc.json>
#   Exits 0 when the document validates, 1 with FAIL lines on stderr otherwise,
#   2 on a bad invocation. The optional #/pointer selects a named subschema
#   (e.g. an envelope data profile under $defs) as the validation root.
#
# The checker is the Go implementation in internal/schemacheck, reached
# through the hidden `__validate-schema` verb, so the published schemas, the
# conformance tests, and this script share one keyword subset: $ref (same file
# or a sibling file), type, const, enum, properties, required, items,
# additionalProperties (bool or schema), minimum, maximum, minLength, maxLength,
# pattern, anyOf, format (date-time), $defs. Any other keyword fails loudly.
# This shim resolves the binary exactly like every other skill path
# (VERDICT_BIN, the skill's bin/verdict, then PATH) and needs no jq.
set -eu
HERE=$(cd -- "$(dirname -- "$0")" && pwd -P)
exec "$HERE/../scripts/verdict" __validate-schema "$@"
