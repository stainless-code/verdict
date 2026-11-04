#!/usr/bin/env sh
# Regenerate models.py from the published envelope schema. The generator is
# pinned; models.py is committed so the example is readable offline.
#
#   ./examples/python-client/generate_models.sh           # rewrite models.py
#   ./examples/python-client/generate_models.sh --check   # drift gate
set -eu

VERSION=0.88.0
ROOT=$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd -P)
OUT="$ROOT/examples/python-client/models.py"

if ! command -v datamodel-codegen >/dev/null 2>&1; then
  echo "generate_models: datamodel-codegen not found (pip install -r examples/python-client/requirements-dev.txt)" >&2
  exit 1
fi

HAVE=$(datamodel-codegen --version | awk '{print $2}')
if [ "$HAVE" != "$VERSION" ]; then
  echo "generate_models: datamodel-codegen $HAVE, want $VERSION" >&2
  exit 1
fi

gen() {
  datamodel-codegen \
    --input "$ROOT/skills/verdict/schemas/envelope.schema.json" \
    --input-file-type jsonschema \
    --output "$1" \
    --output-model-type dataclasses.dataclass \
    --target-python-version 3.10 \
    --use-standard-collections \
    --use-union-operator \
    --formatters builtin \
    --disable-timestamp
}

if [ "${1:-}" = "--check" ]; then
  TMP=$(mktemp "${TMPDIR:-/tmp}/verdict-models.XXXXXX")
  trap 'rm -f "$TMP"' EXIT HUP INT TERM
  gen "$TMP"
  diff -u "$OUT" "$TMP"
  echo "models.py matches datamodel-code-generator $VERSION"
else
  gen "$OUT"
  echo "wrote $OUT (datamodel-code-generator $VERSION)"
fi
