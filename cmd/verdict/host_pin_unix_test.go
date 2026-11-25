//go:build unix

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hostResolvingGh is a gh stub that picks the host the real gh would: the
// --hostname operand, else the host of a three-part --repo, else GH_HOST, else
// github.com. It logs "<host>\t<argv>" per call and answers just enough for
// fetch, post --dry-run, ready and threads to run against PR 4701.
const hostResolvingGh = `#!/usr/bin/env bash
host="${GH_HOST:-github.com}"
prev=""
for a in "$@"; do
  [ "$prev" = "--hostname" ] && host="$a"
  if [ "$prev" = "--repo" ]; then
    IFS=/ read -r first second third <<<"$a"
    [ -n "$third" ] && host="$first"
  fi
  prev="$a"
done
call="$*"
printf '%s\t%s\n' "$host" "${call//$'\n'/ }" >>"$GH_STUB_LOG"
case "$*" in
  */contents/*) echo "gh: Not Found (HTTP 404)" >&2; exit 1 ;;
  *".head.sha"*) echo aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa ;;
  *".login"*) echo reviewer ;;
  *--jq*) echo "a/save.go (+1/-1)" ;;
  "pr view 4701 "*) cat "$GH_STUB_DIR/PR.json" ;;
  "pr diff 4701 "*) cat "$GH_STUB_DIR/canned.diff" ;;
  *graphql*) echo '{"data":{"repository":{"pullRequest":{"reviewThreads":{"pageInfo":{"hasNextPage":false,"endCursor":null},"nodes":[]}}}}}' ;;
  *) echo "[]" ;;
esac
`

const pinnedPR = `{"state":"OPEN","headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",` +
	`"baseRefOid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","title":"Harden the retry budget","body":"Retries stop at three.",` +
	`"headRefName":"harden-retry","author":{"login":"teammate"},"url":"https://github.com/acme/app/pull/4701",` +
	`"files":[{"path":"a/save.go","additions":1,"deletions":1}],"additions":1,"deletions":1,"commits":[]}`

const pinnedDiff = "diff --git a/a/save.go b/a/save.go\nindex 1111111..2222222 100644\n--- a/a/save.go\n+++ b/a/save.go\n" +
	"@@ -3 +3 @@\n-func Save() {}\n+func Save() { _ = 1 }\n"

// TestHostCallsNameTheTargetsHostOverGHHost is the end-to-end proof for a
// github.com checkout on a machine whose GH_HOST names an enterprise host:
// every gh call fetch, post, ready and threads make, through the real runner,
// names github.com, so none can reach the other host's pull request with the
// same owner/repo/number.
func TestHostCallsNameTheTargetsHostOverGHHost(t *testing.T) {
	root, logPath := pinnedHostCheckout(t)
	if code, out, derr := runCapture(t, []string{"fetch", "4701", "--json"}); code != 0 {
		t.Fatalf("fetch by number exit %d:\n%s\n%s", code, out, derr)
	}
	if code, _, derr := runCapture(t, []string{"fetch", "https://github.com/acme/app/pull/4701", "--json"}); code != 0 {
		t.Fatalf("fetch by URL exit %d:\n%s", code, derr)
	}
	writeIn(t, filepath.Join(root, ".verdict", "state", "4701", "review.json"),
		`{"schema_version":"1","findings":[],"spec_checks":[],"receipts":[],"summary":"clean review"}`+"\n")
	for _, args := range [][]string{
		{"post", "4701", "--dry-run", "--json"},
		{"ready", "4701", "--json"},
		{"threads", "4701", "--json"},
	} {
		mustCallHost(t, logPath, args)
	}
	for _, call := range hostCalls(t, logPath) {
		if host, argv, _ := strings.Cut(call, "\t"); host != "github.com" {
			t.Errorf("gh %s went to %s, not the target's github.com", argv, host)
		}
	}
}

// pinnedHostCheckout is a github.com acme/app checkout with the host-resolving
// gh stub first on PATH and GH_HOST naming another host; it returns the
// checkout root and the stub's call log.
func pinnedHostCheckout(t *testing.T) (root, logPath string) {
	t.Helper()
	hermeticEnv(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root = t.TempDir()
	gitIn(t, root, "init", "-q", "-b", "harden-retry")
	if err := os.MkdirAll(filepath.Join(root, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeIn(t, filepath.Join(root, "a", "save.go"), "package a\n\nfunc Save() {}\n")
	gitIn(t, root, "add", "-A")
	gitIn(t, root, "remote", "add", "origin", "git@github.com:acme/app.git")
	t.Chdir(root)

	stubs := t.TempDir()
	logPath = filepath.Join(t.TempDir(), "gh.log")
	writeIn(t, filepath.Join(stubs, "PR.json"), pinnedPR)
	writeIn(t, filepath.Join(stubs, "canned.diff"), pinnedDiff)
	if err := os.WriteFile(filepath.Join(stubs, "gh"), []byte(hostResolvingGh), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", stubs+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GH_STUB_DIR", stubs)
	t.Setenv("GH_STUB_LOG", logPath)
	t.Setenv("GH_HOST", "ghe.corp.example")
	return root, logPath
}

// mustCallHost runs one verb and requires that it made at least one gh call,
// so "no call left the target's host" is never vacuously true for it.
func mustCallHost(t *testing.T, logPath string, args []string) {
	t.Helper()
	before := len(hostCalls(t, logPath))
	runCapture(t, args)
	if len(hostCalls(t, logPath)) == before {
		t.Fatalf("%v made no gh call, so it proves nothing about its host", args)
	}
}

func hostCalls(t *testing.T, logPath string) []string {
	t.Helper()
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("no gh call was made: %v", err)
	}
	return strings.Split(strings.TrimSpace(string(raw)), "\n")
}
