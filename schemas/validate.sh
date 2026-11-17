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
# conformance tests, and this script share one keyword subset. The list below is
# checked against that package's Subset(), which is also the set the checker
# enforces: a keyword outside it fails loudly, and the annotations in it are
# accepted and ignored.
#
#KEYWORDS: $ref $defs type const enum properties propertyNames required items
#KEYWORDS: minItems maxItems additionalProperties minimum maximum exclusiveMinimum
#KEYWORDS: minLength maxLength pattern not anyOf format
#KEYWORDS: $schema $id title description default examples $comment
#
# This shim resolves the binary exactly like every other skill path
# (VERDICT_BIN, the skill's bin/verdict, then PATH) and needs no jq.
set -eu
HERE=$(cd -- "$(dirname -- "$0")" && pwd -P)
BIN="$HERE/../scripts/verdict"
[ -x "$BIN" ] || BIN="$HERE/../skills/verdict/scripts/verdict"
exec "$BIN" __validate-schema "$@"
