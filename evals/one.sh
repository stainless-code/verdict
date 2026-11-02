#!/usr/bin/env bash
# Single-case eval runner: ./evals/one.sh <case-name-without-.sh> [extra env...]
# Mirrors evals/run.sh sandbox setup for one case only (fast iteration).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
case_name="${1:?usage: evals/one.sh <case>}"
for dep in jq shasum git; do
  command -v "$dep" >/dev/null 2>&1 || { echo "evals need $dep on PATH" >&2; exit 2; }
done
SANDBOX="$(mktemp -d "${TMPDIR:-/tmp}/verdict-eval-one.XXXXXX")"
STUBBIN="$SANDBOX/stubbin"
STUBLOG="$SANDBOX/gh-stub.log"
mkdir -p "$STUBBIN"
: > "$STUBLOG"
cat > "$STUBBIN/gh" <<'EOF'
#!/usr/bin/env bash
echo "stub-gh $*" >> "${EVAL_STUBLOG:-/dev/null}"
if [[ -n "${GH_STUB_DOCTOR+x}" ]]; then
  case "$*" in
    "api user --include")
      if [[ "$GH_STUB_DOCTOR" != "__NO_HEADER__" ]]; then
        echo "x-oauth-scopes: $GH_STUB_DOCTOR"
      fi
      echo '{"login":"evals-stub"}'
      exit 0;;
    "auth status") exit 0;;
    "api rate_limit"*) echo "999"; exit 0;;
  esac
fi
echo "evals-stub-gh: network disabled ($*)" >&2
exit 1
EOF
chmod +x "$STUBBIN/gh"
export EVAL_ROOT="$ROOT" EVAL_SANDBOX="$SANDBOX"
export EVAL_STUBBIN="$STUBBIN" EVAL_STUBLOG="$STUBLOG"
trap 'rm -rf "$SANDBOX"' EXIT
bash "$ROOT/evals/cases/$case_name.sh"
