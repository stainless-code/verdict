#!/usr/bin/env bash
# schemas/validate.sh — minimal offline JSON-Schema checker for schemas/*.json.
#
# Usage: schemas/validate.sh <schema.json[#/pointer]> <doc.json>
#   Exits 0 when the document validates, 1 with FAIL lines on stderr otherwise.
#   The optional #/pointer selects a named subschema (e.g. an envelope data
#   profile under $defs) as the validation root.
#
# Why not a full validator: no network at test time (schemas must validate
# hermetically in evals) and the toolchain stays bash/jq, so the checker
# implements only the subset of draft 2020-12 the published schemas use:
#   $ref (same-file "#/..." pointer or sibling-file "name.json#/..." pointer),
#   type (single or array, incl. integer), const, enum, properties, required,
#   items (single-schema form), additionalProperties (bool, or a schema),
#   minimum/maximum, minLength/maxLength, anyOf, format (date-time only),
#   $defs. Annotation keywords ($schema, $id, title, description, default,
#   examples, $comment) are ignored. Any other keyword fails loudly as
#   "unsupported", so a schema that outgrows the checker breaks the eval
#   instead of silently passing.
#
# Bash 3.2 compatible (macOS /bin/bash); needs jq on PATH.
set -uo pipefail

KNOWN_KEYS='$ref type const enum properties required items additionalProperties minimum maximum minLength maxLength anyOf $defs $schema $id title description default examples $comment format'

err() { # $1 = path, $2 = message
  printf 'FAIL %s: %s\n' "$1" "$2" >> "$V_ERRORS"
}

ptr_get() { # $1 = root JSON, $2 = pointer ("" or "/a/b"); prints the target
  local root="$1" ptr="$2"
  if [ -z "$ptr" ]; then printf '%s' "$root"; return 0; fi
  printf '%s' "$root" | jq -c --arg p "$ptr" '
    ($p | split("/") | .[1:] | map(gsub("~1"; "/") | gsub("~0"; "~"))) as $segs
    | . as $root
    | (reduce $segs[] as $s ($root;
        if type == "object" then .[$s]
        elif type == "array" and ($s | test("^[0-9]+$")) then .[$s | tonumber]
        else null end)) // "__verdict_unresolved__"'
}

type_ok() { # $1 = type name, $2 = doc JSON; exit 0 when it matches
  case "$1" in
    object) printf '%s' "$2" | jq -e 'type == "object"' >/dev/null 2>&1;;
    array) printf '%s' "$2" | jq -e 'type == "array"' >/dev/null 2>&1;;
    string) printf '%s' "$2" | jq -e 'type == "string"' >/dev/null 2>&1;;
    boolean) printf '%s' "$2" | jq -e 'type == "boolean"' >/dev/null 2>&1;;
    null) printf '%s' "$2" | jq -e '. == null' >/dev/null 2>&1;;
    number) printf '%s' "$2" | jq -e 'type == "number"' >/dev/null 2>&1;;
    integer) printf '%s' "$2" | jq -e 'type == "number" and . == floor' >/dev/null 2>&1;;
    *) return 1;;
  esac
}

# v_node <schema JSON> <doc JSON> <path>
# Globals read: V_ERRORS (accumulator file). Locals shadow per level:
#   V_FILE (current file root for same-file refs), V_ROOT is only the
#   validation start node; V_DIR (current schema dir).
v_node() {
  local schema="$1" doc="$2" path="$3"
  local V_FILE="$V_FILE" V_DIR="$V_DIR"
  local kind ref target fptr sub i len missing k props extras ok tmp_old

  kind=$(printf '%s' "$schema" | jq -r 'type' 2>/dev/null || echo "BROKEN")
  case "$kind" in
    boolean)
      if [ "$schema" = "false" ]; then err "$path" "false schema rejects everything"; return 1; fi
      return 0;;
    object) ;;
    *) err "$path" "schema node is not an object"; return 1;;
  esac

  if printf '%s' "$schema" | jq -e 'has("$ref")' >/dev/null 2>&1; then
    ref=$(printf '%s' "$schema" | jq -r '."$ref"')
    case "$ref" in
      *\#*) fptr="${ref%%\#*}"; ptr="#${ref#*\#}";;
      *) fptr="$ref"; ptr="";;
    esac
    case "$ptr" in "" | \#) ptr="";; *) ptr="${ptr#\#}";; esac
    if [ -n "$fptr" ]; then
      if [ ! -f "$V_DIR/$fptr" ]; then err "$path" "unresolvable \$ref '$ref'"; return 1; fi
      V_FILE=$(cat "$V_DIR/$fptr")
      V_DIR=$(cd "$V_DIR" && dirname "$fptr" 2>/dev/null || printf '%s' "$V_DIR")
      [ "$V_DIR" = "." ] && V_DIR="$SCHEMA_DIR"
    fi
    target=$(ptr_get "$V_FILE" "$ptr")
    if [ -z "$target" ] || [ "$target" = '"__verdict_unresolved__"' ]; then err "$path" "unresolvable \$ref '$ref'"; return 1; fi
    v_node "$target" "$doc" "$path"
    return $?
  fi

  missing=$(printf '%s' "$schema" | jq -r --argjson known "$(printf '%s' "$KNOWN_KEYS" | jq -R 'split(" ")')" '
    [keys_unsorted[] | select(. as $k | $known | index($k) | not)] | join(" ")')
  if [ -n "$missing" ]; then
    err "$path" "unsupported keyword(s) for this checker: $missing"
  fi

  if printf '%s' "$schema" | jq -e 'has("type")' >/dev/null 2>&1; then
    if printf '%s' "$schema" | jq -e '.type | type == "array"' >/dev/null 2>&1; then
      ok="false"
      len=$(printf '%s' "$schema" | jq -r '.type | length')
      i=0
      while [ "$i" -lt "$len" ]; do
        if type_ok "$(printf '%s' "$schema" | jq -r ".type[$i]")" "$doc"; then ok="true"; break; fi
        i=$((i + 1))
      done
      if [ "$ok" != "true" ]; then
        err "$path" "want type $(printf '%s' "$schema" | jq -c '.type'), got $(printf '%s' "$doc" | jq -r 'type' 2>/dev/null || echo unreadable)"
        return 1
      fi
    else
      if ! type_ok "$(printf '%s' "$schema" | jq -r '.type')" "$doc"; then
        err "$path" "want type $(printf '%s' "$schema" | jq -r '.type'), got $(printf '%s' "$doc" | jq -r 'type' 2>/dev/null || echo unreadable)"
        return 1
      fi
    fi
  fi

  if printf '%s' "$schema" | jq -e 'has("const")' >/dev/null 2>&1; then
    if ! printf '%s' "$doc" | jq -e --argjson c "$(printf '%s' "$schema" | jq -c '.const')" '. == $c' >/dev/null 2>&1; then
      err "$path" "want const $(printf '%s' "$schema" | jq -c '.const')"
    fi
  fi

  if printf '%s' "$schema" | jq -e 'has("enum")' >/dev/null 2>&1; then
    if ! jq -e -n --argjson d "$doc" --argjson e "$(printf '%s' "$schema" | jq -c '.enum')" '$e | index($d) != null' >/dev/null 2>&1; then
      err "$path" "not one of $(printf '%s' "$schema" | jq -c '.enum')"
    fi
  fi

  if printf '%s' "$doc" | jq -e 'type == "object"' >/dev/null 2>&1; then
    if printf '%s' "$schema" | jq -e 'has("required")' >/dev/null 2>&1; then
      missing=$(jq -r -n --argjson d "$doc" --argjson req "$(printf '%s' "$schema" | jq -c '.required')" '
        [$req[] | select(. as $k | $d | has($k) | not)] | join(" ")')
      if [ -n "$missing" ]; then err "$path" "missing required key(s): $missing"; fi
    fi
    if printf '%s' "$schema" | jq -e 'has("properties")' >/dev/null 2>&1; then
      props=$(printf '%s' "$schema" | jq -r '.properties | keys_unsorted[]' 2>/dev/null || true)
      while IFS= read -r k; do
        [ -n "$k" ] || continue
        if printf '%s' "$doc" | jq -e --arg k "$k" 'has($k)' >/dev/null 2>&1; then
          sub=$(printf '%s' "$schema" | jq -c --arg k "$k" '.properties[$k]')
          v_node "$sub" "$(printf '%s' "$doc" | jq -c --arg k "$k" '.[$k]')" "$path/$k"
        fi
      done <<<"$props"
    fi
    if printf '%s' "$schema" | jq -e 'has("additionalProperties")' >/dev/null 2>&1; then
      if printf '%s' "$schema" | jq -e '.additionalProperties == false' >/dev/null 2>&1; then
        extras=$(printf '%s' "$doc" | jq -r --argjson known "$(printf '%s' "$schema" | jq -c '(.properties // {}) | keys')" '
          [keys_unsorted[] | select(. as $k | $known | index($k) | not)] | join(" ")')
        if [ -n "$extras" ]; then err "$path" "additional properties not allowed: $extras"; fi
      elif printf '%s' "$schema" | jq -e '.additionalProperties | type == "object"' >/dev/null 2>&1; then
        sub=$(printf '%s' "$schema" | jq -c '.additionalProperties')
        extras=$(printf '%s' "$doc" | jq -r --argjson known "$(printf '%s' "$schema" | jq -c '(.properties // {}) | keys')" '
          keys_unsorted[] | select(. as $k | $known | index($k) | not)')
        while IFS= read -r k; do
          [ -n "$k" ] || continue
          v_node "$sub" "$(printf '%s' "$doc" | jq -c --arg k "$k" '.[$k]')" "$path/$k"
        done <<<"$extras"
      fi
    fi
  else
    if printf '%s' "$schema" | jq -e 'has("required")' >/dev/null 2>&1; then
      if ! printf '%s' "$doc" | jq -e 'type == "object"' >/dev/null 2>&1; then
        err "$path" "required keys but value is not an object"
      fi
    fi
  fi

  if printf '%s' "$schema" | jq -e 'has("items")' >/dev/null 2>&1; then
    if printf '%s' "$doc" | jq -e 'type == "array"' >/dev/null 2>&1; then
      if printf '%s' "$schema" | jq -e '.items | type == "object"' >/dev/null 2>&1; then
        sub=$(printf '%s' "$schema" | jq -c '.items')
        len=$(printf '%s' "$doc" | jq 'length')
        i=0
        while [ "$i" -lt "$len" ]; do
          v_node "$sub" "$(printf '%s' "$doc" | jq -c --argjson i "$i" '.[$i]')" "$path/$i"
          i=$((i + 1))
        done
      else
        err "$path" "unsupported items form (want a single schema)"
      fi
    fi
  fi

  if printf '%s' "$schema" | jq -e 'has("minimum")' >/dev/null 2>&1; then
    if printf '%s' "$doc" | jq -e 'type == "number"' >/dev/null 2>&1; then
      if ! printf '%s' "$doc" | jq -e --argjson m "$(printf '%s' "$schema" | jq '.minimum')" '. >= $m' >/dev/null 2>&1; then
        err "$path" "below minimum $(printf '%s' "$schema" | jq '.minimum')"
      fi
    fi
  fi
  if printf '%s' "$schema" | jq -e 'has("maximum")' >/dev/null 2>&1; then
    if printf '%s' "$doc" | jq -e 'type == "number"' >/dev/null 2>&1; then
      if ! printf '%s' "$doc" | jq -e --argjson m "$(printf '%s' "$schema" | jq '.maximum')" '. <= $m' >/dev/null 2>&1; then
        err "$path" "above maximum $(printf '%s' "$schema" | jq '.maximum')"
      fi
    fi
  fi
  if printf '%s' "$schema" | jq -e 'has("minLength")' >/dev/null 2>&1; then
    if printf '%s' "$doc" | jq -e 'type == "string"' >/dev/null 2>&1; then
      if ! printf '%s' "$doc" | jq -e --argjson m "$(printf '%s' "$schema" | jq '.minLength')" 'length >= $m' >/dev/null 2>&1; then
        err "$path" "shorter than minLength $(printf '%s' "$schema" | jq '.minLength')"
      fi
    fi
  fi
  if printf '%s' "$schema" | jq -e 'has("maxLength")' >/dev/null 2>&1; then
    if printf '%s' "$doc" | jq -e 'type == "string"' >/dev/null 2>&1; then
      if ! printf '%s' "$doc" | jq -e --argjson m "$(printf '%s' "$schema" | jq '.maxLength')" 'length <= $m' >/dev/null 2>&1; then
        err "$path" "longer than maxLength $(printf '%s' "$schema" | jq '.maxLength')"
      fi
    fi
  fi

  if printf '%s' "$schema" | jq -e 'has("format")' >/dev/null 2>&1; then
    if [ "$(printf '%s' "$schema" | jq -r '.format')" = "date-time" ]; then
      if printf '%s' "$doc" | jq -e 'type == "string"' >/dev/null 2>&1; then
        if ! printf '%s' "$doc" | jq -e -r '.' | grep -qE '^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$'; then
          err "$path" "not a date-time (want YYYY-MM-DDTHH:MM:SSZ)"
        fi
      fi
    else
      err "$path" "unsupported format '$(printf '%s' "$schema" | jq -r '.format')' (checker knows date-time only)"
    fi
  fi

  if printf '%s' "$schema" | jq -e 'has("anyOf")' >/dev/null 2>&1; then
    ok="false"
    len=$(printf '%s' "$schema" | jq '.anyOf | length')
    i=0
    while [ "$i" -lt "$len" ]; do
      sub=$(printf '%s' "$schema" | jq -c ".anyOf[$i]")
      tmp_old="$V_ERRORS"
      V_ERRORS=$(mktemp "${TMPDIR:-/tmp}/verdict-schema-anyof.XXXXXX")
      if v_node "$sub" "$doc" "$path"; then
        if [ ! -s "$V_ERRORS" ]; then ok="true"; fi
      fi
      rm -f "$V_ERRORS"
      V_ERRORS="$tmp_old"
      [ "$ok" = "true" ] && break
      i=$((i + 1))
    done
    if [ "$ok" != "true" ]; then err "$path" "matches no anyOf branch"; fi
  fi
  return 0
}

main() {
  if [ "$#" -ne 2 ]; then echo "usage: schemas/validate.sh <schema.json[#/pointer]> <doc.json>" >&2; exit 2; fi
  local schema_file="$1" doc_file="$2"
  local frag=""
  case "$schema_file" in
    *\#*) frag="${schema_file#*\#}"; schema_file="${schema_file%%\#*}";;
  esac
  case "$frag" in "" | /*) ;; *) echo "FAIL: bad sub-pointer (want #/...): $frag" >&2; exit 1;; esac
  command -v jq >/dev/null 2>&1 || { echo "FAIL: jq not on PATH (schemas/validate.sh needs jq)" >&2; exit 1; }
  [ -f "$schema_file" ] || { echo "FAIL: schema not found: $schema_file" >&2; exit 1; }
  [ -f "$doc_file" ] || { echo "FAIL: doc not found: $doc_file" >&2; exit 1; }
  jq empty "$schema_file" >/dev/null 2>&1 || { echo "FAIL: schema is not JSON: $schema_file" >&2; exit 1; }
  jq empty "$doc_file" >/dev/null 2>&1 || { echo "FAIL: doc is not JSON: $doc_file" >&2; exit 1; }
  SCHEMA_DIR=$(cd "$(dirname "$schema_file")" && pwd)
  V_DIR="$SCHEMA_DIR"
  V_FILE=$(cat "$schema_file")
  V_ROOT="$V_FILE"
  if [ -n "$frag" ]; then
    V_ROOT=$(ptr_get "$V_FILE" "$frag")
    if [ "$V_ROOT" = '"__verdict_unresolved__"' ]; then
      echo "FAIL: unresolvable sub-pointer $frag in $schema_file" >&2; exit 1
    fi
  fi
  V_ERRORS=$(mktemp "${TMPDIR:-/tmp}/verdict-schema.XXXXXX")
  v_node "$V_ROOT" "$(cat "$doc_file")" "$doc_file"
  if [ -s "$V_ERRORS" ]; then
    cat "$V_ERRORS" >&2
    rm -f "$V_ERRORS"
    exit 1
  fi
  rm -f "$V_ERRORS"
  exit 0
}

main "$@"
