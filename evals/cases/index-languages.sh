#!/usr/bin/env bash
#
# index languages: TypeScript, JavaScript, and Python files are scanned
# alongside Go, every file publishes its language, and a lexical language
# resolves only inside its own directory. No network.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

export GIT_CONFIG_NOSYSTEM=1
export GIT_CONFIG_GLOBAL=/dev/null
export GIT_CONFIG_COUNT=1
export GIT_CONFIG_KEY_0=commit.gpgsign
export GIT_CONFIG_VALUE_0=false
export GIT_AUTHOR_NAME=verdict-test
export GIT_AUTHOR_EMAIL=test@verdict.invalid
export GIT_COMMITTER_NAME=verdict-test
export GIT_COMMITTER_EMAIL=test@verdict.invalid
export HOME="$EVAL_SANDBOX/home"
mkdir -p "$HOME"

(
  cd "$EVAL_SANDBOX" || exit 99
  git init -q -b main
  printf 'export function target(a: number) { return a; }\n' >util.ts
  printf "import { target } from './util';\nimport * as util from './util';\n\nconst v = target(1);\nutil.target(2);\nobj.target(3);\n" >app.ts
  printf "const fs = require('fs');\n\nfs.readFile(1);\n" >app.js
  mkdir -p pkg
  printf 'def helper(x):\n    return x\n' >pkg/mod.py
  printf 'from .mod import helper\n\nhelper(1)\n' >pkg/use.py
  printf 'from .mod import helper\n\nhelper(2)\n' >pkg/test_mod.py
  mkdir -p other
  printf 'export function use() {\n  return target();\n}\n' >other/pkg.ts
  printf 'export function target(a: number) { return a; }\n' >other/decoy.ts
  printf 'target()\n' >NOTES.md
  git add -A
  GIT_AUTHOR_DATE=2024-05-01T00:00:00Z GIT_COMMITTER_DATE=2024-05-01T00:00:00Z \
    git commit -qm 'scanned fixture'
)

stub_before="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"

run_review index build --json
assert_exit "index build exits 0" 0
assert_envelope "index build envelope"
assert_json_field "eight source files indexed" 8 '.data.files_indexed'
assert_json_field "nothing skipped" 0 '.data.skipped'
assert_json_field "the markdown file is not scanned" "0" \
  '[.data.languages[] | select(.language == "go")] | length'
assert_json_field "the language breakdown" "javascript 1,python 3,typescript 4" \
  '[.data.languages[] | "\(.language) \(.files)"] | join(",")'

run_review index callers target --json
assert_exit "index callers exits 0" 0
assert_envelope "callers envelope"
assert_json_field "three call sites: two in the declaring directory, one in the second" 3 '.data.counts.callers'
assert_json_field "the first row is lexical inside the directory" "lexical" '.data.rows[0].resolution'
assert_json_field "the first row names its language" "typescript" '.data.rows[0].language'
assert_json_field "the first row path and line" "app.ts:4" '.data.rows[0] | "\(.path):\(.line)"'
assert_json_field "the second row is an imported namespace attribute" "app.ts:5 method" '.data.rows[1] | "\(.path):\(.line) \(.kind)"'
assert_json_field "the same name declared elsewhere is its own target" "other/pkg.ts:2" \
  '[.data.rows[] | select(.path | startswith("other/")) | "\(.path):\(.line)"] | join(",")'

run_review index callers target
assert_exit "human callers exits 0" 0
assert_file_grep "human output names the call site" "$EVAL_OUT" '^app.ts:4 call (lexical)$'
assert_file_grep "human output names the imported namespace attribute" "$EVAL_OUT" '^app.ts:5 method (lexical)$'

run_review index callers pkg.helper --json
assert_exit "python callers exits 0" 0
assert_json_field "two python call sites" 2 '.data.counts.callers'
assert_json_field "the python row names its language" "python" '.data.rows[0].language'
assert_json_field "the python rows" "pkg/test_mod.py:3,pkg/use.py:3" \
  '[.data.rows[] | "\(.path):\(.line)"] | join(",")'

run_review index tests-for pkg/mod.py --json
assert_exit "python tests-for exits 0" 0
assert_json_field "the same-package python test is a candidate" "pkg/test_mod.py same-package" \
  '[.data.rows[] | "\(.path) \(.via)"] | join(",")'

run_review index tests-for util.ts --json
assert_exit "typescript tests-for exits 0" 0
assert_json_field "a typescript file with no test has no rows" 0 '.data.rows | length'

run_review index tests-for NOTES.md --json
assert_exit "an unscanned path refuses" 1
assert_refusal "the refusal names the code" "bad-flags"
assert_json_field "the refusal says the file is not an indexed source file" 1 \
  '[.error.message | select(test("NOTES.md is not an indexed source file at "))] | length'

stub_after="$(wc -l <"$EVAL_STUBLOG" | tr -d ' ')"
assert_eq "no host calls" "$stub_before" "$stub_after"

eval_end
