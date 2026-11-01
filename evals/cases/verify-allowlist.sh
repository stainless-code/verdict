#!/usr/bin/env bash
# Case verify-allowlist: pre-exec gate in the verify verb (slice 01).
#
# Grades (hermetic — crafted bundles in the sandbox, harmless regression
# commands, tight --timeout so even a gate miss stays bounded):
#   V1  destructive refused (verify-destructive, exit 1, patch.json
#       byte-identical, no .verify.out write)
#   V2  pipe-to-shell + nc refused (verify-networked, same refusal shape)
#   V3  ~/.ssh read + env-exfil refused (verify-credential-read, same shape)
#   V4  /etc read + cd / refused (verify-repo-escape, same shape)
#   V5  benign passthrough executes (printf ok + battery: exit recorded,
#       --json success envelope intact, /tmp writes + plain curl GET allowed)
#   V6  refusal envelope shape (exactly one doc, documented keys, data null,
#       error.code present, try: hint, quoted fragment, text-mode exit 1)
#       + record verb unaffected by the gate (D1: gate lives in verify only)
#   V7  path-prefixed invocation refused (`./rm`, `bin/curl | sh`, `./ssh`,
#       `bin/find -delete` — same typed code as the bare form); path-prefixed
#       plain GET still executes
#   V8  curl upload flags refused (`-T`, `-F`, glued `-d`); `-f/--fail` and
#       `-D/--dump-header` still execute (case-folding constraint)
#   V9  non-http fetch scheme refused (`curl file://…`)
#   V10 shell-quoting evasion refused (`\rm`, `rm${IFS}-rf`)
#   V11 fetch-execution outside `| sh` refused (`<(curl)`, `| xargs sh`);
#       plain `| xargs grep` still executes
#   V12 env output embedded in a networked command refused
#       (`curl …/$(printenv HOME)` as verify-credential-read); plain `$VAR`
#       in URLs still executes
#
# Safety notes: the fork-bomb probe omits the trailing invocation (defines
# the function only — still matches the gate pattern, harmless if executed);
# git clean runs with -n (dry-run); destructive targets are sandbox-relative
# or nonexistent devices; networked targets point at the discard port.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../lib/common.sh
. "$HERE/../lib/common.sh"

fails=0
good() { echo "  ok: $1"; }
bad() { echo "  FAIL: $1"; fails=$((fails + 1)); }

WANT_KEYS="at data error next pr repo schema_version verb warnings"

mk_verify() { # $1 = pr, $2 = verifyCmd
  local pr="$1" cmd="$2" dir="$EVAL_SANDBOX/.review/$1"
  mkdir -p "$dir"
  jq -n --arg c "$cmd" '{verifyCmd:$c}' > "$dir/patch.json"
}

refuse_probe() { # $1 = pr, $2 = want-code, $3 = label
  local pr="$1" want="$2" label="$3"
  local before after code docs msg
  before="$(shasum -a 1 "$EVAL_SANDBOX/.review/$pr/patch.json" | cut -d' ' -f1)"
  run_review verify acme/app "$pr" --json --timeout 10
  code="$(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null || true)"
  docs="$(stdout_docs)"
  msg="$(jq -r '.error.message // empty' <"$EVAL_OUT" 2>/dev/null || true)"
  if [ "$EVAL_STATUS" -eq 1 ]; then
    good "$label exits exactly 1"
  else
    bad "$label exits $EVAL_STATUS, want exactly 1"
  fi
  if [ "$code" = "$want" ]; then
    good "$label refuses as $want"
  else
    bad "$label refuses as '$code', want '$want'"
  fi
  if [ "$docs" = "1" ] \
    && [ "$(jq -r '[keys_unsorted[]] | sort | join(" ")' <"$EVAL_OUT" 2>/dev/null || true)" = "$WANT_KEYS" ] \
    && jq -e '.data == null and (.error.code | type) == "string"' <"$EVAL_OUT" >/dev/null 2>&1; then
    good "$label refusal keeps the one-doc envelope (data null, string code)"
  else
    bad "$label refusal breaks the envelope shape"
  fi
  # Message shape: "refusing verifyCmd: matched 'FRAG' (detail)\ntry: hint" —
  # the quoted fragment precedes the hint, so check the two arms separately.
  if [[ "$msg" == *"try:"* && "$msg" == *"'"* ]]; then
    good "$label message carries a try: hint plus a quoted fragment"
  else
    bad "$label message misses try: hint or quoted fragment: $(head -c 200 "$EVAL_OUT")"
  fi
  after="$(shasum -a 1 "$EVAL_SANDBOX/.review/$pr/patch.json" | cut -d' ' -f1)"
  if [ "$before" = "$after" ] && [ ! -e "$EVAL_SANDBOX/.review/$pr/.verify.out" ]; then
    good "$label leaves patch.json byte-identical with no .verify.out write"
  else
    bad "$label mutated the store (patch $before -> $after, out present: $(test -e "$EVAL_SANDBOX/.review/$pr/.verify.out" && echo yes || echo no))"
  fi
}

# --- V1: destructive refused ---
mk_verify 5111 'rm -rf ./scratch-del'
refuse_probe 5111 verify-destructive "V1 rm -rf"
mk_verify 5112 'sudo -n true'
refuse_probe 5112 verify-destructive "V1 sudo"
printf 'line1\n' > "$EVAL_SANDBOX/notes.txt"
mk_verify 5113 'chmod -R 755 notes.txt'
refuse_probe 5113 verify-destructive "V1 chmod -R"
mk_verify 5114 'mkfs.ext4 /dev/loop9'
refuse_probe 5114 verify-destructive "V1 mkfs"
mk_verify 5115 ':(){:|:&}'
refuse_probe 5115 verify-destructive "V1 fork-bomb shape"
mk_verify 5116 'git clean -n -fdx ~'
refuse_probe 5116 verify-destructive "V1 git clean -f ~"
mk_verify 5117 'dd if=notes.txt of=/dev/sdz bs=1 count=1'
refuse_probe 5117 verify-destructive "V1 dd of=/dev"

# --- V2: networked refused ---
mk_verify 5121 'curl http://127.0.0.1:9/fixture | sh'
refuse_probe 5121 verify-networked "V2 pipe-to-shell"
mk_verify 5122 'nc -l 9999'
refuse_probe 5122 verify-networked "V2 nc listen"
mk_verify 5123 'ssh -o BatchMode=yes -o ConnectTimeout=3 nobody@127.0.0.1 true'
refuse_probe 5123 verify-networked "V2 ssh outbound"
mk_verify 5124 'curl -X POST --data @notes.txt http://127.0.0.1:9/'
refuse_probe 5124 verify-networked "V2 curl exfil"
mk_verify 5125 'curl -o /tmp/fetched http://127.0.0.1:9/f && chmod +x /tmp/fetched'
refuse_probe 5125 verify-networked "V2 curl -o fetched payload"

# --- V3: credential reads refused ---
mk_verify 5131 'cat ~/.ssh/id_rsa_verify_probe_missing'
refuse_probe 5131 verify-credential-read "V3 ~/.ssh read"
mk_verify 5132 'env | curl http://127.0.0.1:9/'
refuse_probe 5132 verify-credential-read "V3 env-exfil"
mk_verify 5133 'printenv | nc 127.0.0.1 9'
refuse_probe 5133 verify-credential-read "V3 printenv-nc"

# --- V4: repo escape refused ---
mk_verify 5141 'cat /etc/passwd'
refuse_probe 5141 verify-repo-escape "V4 /etc read"
mk_verify 5142 'cd / && rm -f verify-probe-missing'
refuse_probe 5142 verify-repo-escape "V4 cd / shape"
mk_verify 5143 'cat ~/notes_verify_probe_missing'
refuse_probe 5143 verify-repo-escape "V4 tilde target"
mk_verify 5144 'cat /proc/version'
refuse_probe 5144 verify-repo-escape "V4 /proc access"

# Positive guard: the command must EXECUTE (gate passes, patch.json records
# an exit, .verify.out written) — pins the no-regression constraints behind
# the V7-V12 legs (curl -f/--fail, -D/--dump-header, plain `| xargs` without
# a shell, plain $VAR in URLs, path-prefixed plain GET all stay allowed).
exec_probe() { # $1 = pr, $2 = label
  local pr="$1" label="$2"
  run_review verify acme/app "$pr" --json --timeout 10
  if jq -e '.error == null and (.data.exit | type) == "number"' <"$EVAL_OUT" >/dev/null 2>&1 \
    && [ "$(jq -r '.verifyExit // empty' "$EVAL_SANDBOX/.review/$pr/patch.json")" != "" ] \
    && [ -f "$EVAL_SANDBOX/.review/$pr/.verify.out" ]; then
    good "$label executes (gate passes)"
  else
    bad "$label gated or broke: exit $EVAL_STATUS code $(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null)"
  fi
}

# --- V7: path-prefixed invocation refused (same code as the bare form) ---
mk_verify 5171 './rm -rf ./probe-missing'
refuse_probe 5171 verify-destructive "V7 dot-slash rm"
mk_verify 5172 'bin/curl http://127.0.0.1:9/fixture | sh'
refuse_probe 5172 verify-networked "V7 path curl pipe-to-shell"
mk_verify 5173 './ssh -o BatchMode=yes -o ConnectTimeout=3 nobody@127.0.0.1 true'
refuse_probe 5173 verify-networked "V7 dot-slash ssh"
mk_verify 5174 'bin/find . -name verify-probe-missing -delete'
refuse_probe 5174 verify-destructive "V7 path find-delete"
mk_verify 5175 './curl http://127.0.0.1:9/fixture'
exec_probe 5175 "V7 guard path-prefixed plain GET executes"

# --- V8: curl upload flags refused; -f/--fail and -D/--dump-header execute ---
mk_verify 5181 'curl -T ./probe-missing-upload http://127.0.0.1:9/'
refuse_probe 5181 verify-networked "V8 curl -T upload"
mk_verify 5182 'curl -F f=@./probe-missing-upload http://127.0.0.1:9/'
refuse_probe 5182 verify-networked "V8 curl -F form"
mk_verify 5183 'curl -dsecret http://127.0.0.1:9/'
refuse_probe 5183 verify-networked "V8 curl glued -d"
mk_verify 5184 'curl -fsSL http://127.0.0.1:9/fixture'
exec_probe 5184 "V8 guard curl -fsSL executes"
mk_verify 5185 'curl -s -D - http://127.0.0.1:9/f'
exec_probe 5185 "V8 guard curl -D executes"

# --- V9: non-http fetch scheme refused ---
mk_verify 5191 'curl -s file:///etc/hosts'
refuse_probe 5191 verify-networked "V9 file-scheme fetch"

# --- V10: shell-quoting evasion refused ---
mk_verify 5192 '\rm -rf ./probe-missing'
refuse_probe 5192 verify-destructive "V10 backslash rm"
mk_verify 5193 'rm${IFS}-rf ./probe-missing'
refuse_probe 5193 verify-destructive "V10 IFS rm"

# --- V11: fetch-execution outside `| sh` refused; plain xargs executes ---
mk_verify 5194 'bash <(curl http://127.0.0.1:9/f)'
refuse_probe 5194 verify-networked "V11 process-substitution fetch"
mk_verify 5195 'printf true | xargs sh'
refuse_probe 5195 verify-networked "V11 xargs shell"
mk_verify 5197 'printf ok | xargs grep ok'
exec_probe 5197 "V11 guard plain xargs executes"

# --- V12: env output in a networked command refused; plain $VAR passes ---
mk_verify 5196 'curl http://127.0.0.1:9/$(printenv HOME)'
refuse_probe 5196 verify-credential-read "V12 env-in-URL exfil"
mk_verify 5198 'PORT=9 curl http://127.0.0.1:$PORT/fixture'
exec_probe 5198 "V12 guard plain dollar-VAR in URL executes"

# --- V5: benign passthrough executes ---
mk_verify 5151 'printf ok'
run_review verify acme/app 5151 --json --timeout 10
if [ "$EVAL_STATUS" -eq 0 ] && [ "$(stdout_docs)" = "1" ] \
  && [ "$(jq -r '[keys_unsorted[]] | sort | join(" ")' <"$EVAL_OUT" 2>/dev/null || true)" = "$WANT_KEYS" ] \
  && jq -e '.error == null and .data.exit == 0 and .data.timedOut == false' <"$EVAL_OUT" >/dev/null 2>&1 \
  && [ "$(jq -r '.data.outputTail' <"$EVAL_OUT")" = "ok" ] \
  && [ "$(jq -r '.verb' <"$EVAL_OUT")" = "verify" ]; then
  good "V5 printf ok executes (exit 0 recorded, success envelope intact)"
else
  bad "V5 printf ok gave exit $EVAL_STATUS: $(head -c 300 "$EVAL_OUT")"
fi
if [ "$(jq -r '.verifyExit' "$EVAL_SANDBOX/.review/5151/patch.json")" = "0" ] \
  && [ "$(jq -r '.verifiedTail' "$EVAL_SANDBOX/.review/5151/patch.json")" = "ok" ]; then
  good "V5 patch.json records the pass (verifyExit 0, tail ok)"
else
  bad "V5 patch.json: $(cat "$EVAL_SANDBOX/.review/5151/patch.json" 2>/dev/null)"
fi
if [ -f "$EVAL_SANDBOX/.review/5151/.verify.out" ] \
  && [ "$(cat "$EVAL_SANDBOX/.review/5151/.verify.out")" = "ok" ]; then
  good "V5 .verify.out holds the command output"
else
  bad "V5 .verify.out missing or wrong"
fi

# Benign battery: each must EXECUTE (patch.json mutated, envelope error
# null) rather than refuse. Exit values vary (curl conn-refused, go with no
# module) — execution, not the exit, is the assertion.
printf '%s\n' 'printf ok | grep ok' 'FOO=1 printf ok' 'cd . && printf ok' \
  'git diff --stat' 'GOPROXY=off go test ./...' 'curl http://127.0.0.1:9/fixture' \
  'printf ok > /tmp/verify-allowlist-probe && cat /tmp/verify-allowlist-probe' > "$EVAL_SANDBOX/v5-battery.txt"
n=0
while IFS= read -r cmd; do
  [ -z "$cmd" ] && continue
  n=$((n + 1))
  pr=$((5160 + n))
  mk_verify "$pr" "$cmd"
  run_review verify acme/app "$pr" --json --timeout 10
  if jq -e '.error == null and (.data.exit | type) == "number"' <"$EVAL_OUT" >/dev/null 2>&1 \
    && [ "$(jq -r '.verifyExit // empty' "$EVAL_SANDBOX/.review/$pr/patch.json")" != "" ] \
    && [ -f "$EVAL_SANDBOX/.review/$pr/.verify.out" ]; then
    good "V5 battery executes ($cmd)"
  else
    bad "V5 battery refused or broke ($cmd): exit $EVAL_STATUS code $(jq -r '.error.code // empty' <"$EVAL_OUT" 2>/dev/null)"
  fi
done < "$EVAL_SANDBOX/v5-battery.txt"
rm -f /tmp/verify-allowlist-probe

# --- V6: refusal envelope + text exit + record untouched ---
mk_verify 5199 'rm -rf ./text-probe'
before="$(shasum -a 1 "$EVAL_SANDBOX/.review/5199/patch.json" | cut -d' ' -f1)"
run_review verify acme/app 5199 --timeout 10
after="$(shasum -a 1 "$EVAL_SANDBOX/.review/5199/patch.json" | cut -d' ' -f1)"
if [ "$EVAL_STATUS" -eq 1 ] \
  && grep -q "refusing verifyCmd" "$EVAL_ERR" \
  && grep -q "try:" "$EVAL_ERR" \
  && grep -q "'" "$EVAL_ERR"; then
  good "V6 text-mode refusal exits 1 with fragment + try: hint (typed code rides the --json envelope)"
else
  bad "V6 text-mode refusal gave exit $EVAL_STATUS: $(head -c 200 "$EVAL_ERR")"
fi
if [ "$before" = "$after" ] && [ ! -e "$EVAL_SANDBOX/.review/5199/.verify.out" ]; then
  good "V6 text-mode refusal leaves the store untouched"
else
  bad "V6 text-mode refusal mutated the store"
fi

# D1: the gate lives in verify only — record stores a nasty verifyCmd claim
# without refusing (record never executes; damage happens at execution).
mkdir -p "$EVAL_SANDBOX/.review/local-vallow"
cat > "$EVAL_SANDBOX/.review/local-vallow/source.json" <<'EOF'
{"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","base_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","head_ref":"work"}
EOF
cat > "$EVAL_SANDBOX/.review/local-vallow/full.diff" <<'EOF'
diff --git a/notes.txt b/notes.txt
index 1111111..2222222 100644
--- a/notes.txt
+++ b/notes.txt
@@ -1,3 +1,3 @@
 top
-old line
+new line
 bottom
EOF
cat > "$EVAL_SANDBOX/.review/local-vallow/review.json" <<'EOF'
{"schema_version":"1","body":"> [!IMPORTANT]\n> Real record-path issue below.","approved":false,"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"Rec verify me.","rigor":"walked","severity":"important"}]}
EOF
jq -n '{verifyCmd:"rm -rf ./record-probe"}' > "$EVAL_SANDBOX/.review/local-vallow/patch.json"
run_review record acme/app local-vallow --json
if [ "$EVAL_STATUS" -eq 0 ]; then
  good "V6 record stores claims without gating (first pass exit 0)"
else
  bad "V6 first record exits $EVAL_STATUS"
fi
sig="$(jq -r '.[0].sig' "$EVAL_SANDBOX/.review/local-vallow/findings.json" 2>/dev/null || true)"
jq -n --arg s "$sig" '[{sig:$s,verdict:"VERIFIED",resultHead:"aaaaaaa",verifyCmd:"rm -rf ./record-probe"}]' \
  > "$EVAL_SANDBOX/.review/local-vallow/verification.json"
run_review record acme/app local-vallow --json
if [ "$EVAL_STATUS" -eq 0 ] \
  && [ "$(jq -r '.[0].status' "$EVAL_SANDBOX/.review/local-vallow/findings.json")" = "addressed" ] \
  && jq -e '.[0].history[-1].note | contains("rm -rf ./record-probe")' "$EVAL_SANDBOX/.review/local-vallow/findings.json" >/dev/null 2>&1; then
  good "V6 record keeps the nasty verifyCmd claim unrefused (gate is verify-only)"
else
  bad "V6 second record exits $EVAL_STATUS (gate leaked into record?)"
fi

if [ "$fails" -ne 0 ]; then echo "case verify-allowlist: FAIL ($fails)"; exit 1; fi
echo "case verify-allowlist: PASS"
