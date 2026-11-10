package readgolden_test

import (
	"maps"
	"os"
	"testing"
)

// readyStubScript answers the three live reads deterministically: PR view
// (open, mergeable, long description), passing checks, and threads per
// $READY_THREADS (open = one unresolved thread, clear = all resolved).
// Anything else fails like the network-dead stub.
const readyStubScript = `#!/usr/bin/env bash
echo "stub-gh $*" >> "${EVAL_STUBLOG:-/dev/null}"
if [[ "$*" == *"api graphql"* ]]; then
  if [ "${READY_THREADS:-open}" = "clear" ]; then
    printf '%s' '{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[{"isResolved":true}]}}}}}'
  else
    printf '%s' '{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[{"isResolved":false},{"isResolved":true}]}}}}}'
  fi
  exit 0
fi
case "$*" in
  *"pr view"*)
    printf '%s' '{"isDraft":false,"mergeable":"MERGEABLE","mergeStateStatus":"CLEAN","body":"A description that is long enough to pass the thin check easily here.","state":"OPEN","headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}'
    exit 0;;
  *"pr checks"*)
    printf '%s' '[{"bucket":"pass","name":"ci / test","state":"SUCCESS"}]'
    exit 0;;
esac
echo "stub-gh: no match ($*)" >&2
exit 1
`

// writeStub writes an executable gh stub with the given script, returning
// extraEnv that puts it first on PATH. The stub goes through
// harness.WriteStubAt so Windows gets the .exe launcher beside the script
// (exec.LookPath resolves only PATHEXT names there), and PATH joins with the
// host separator.
func writeStub(t *testing.T, script string, extra map[string]string) map[string]string {
	t.Helper()
	dir := t.TempDir()
	if err := harness.WriteStubAt(dir, "gh", script); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"PATH": dir + string(os.PathListSeparator) + os.Getenv("PATH")}
	maps.Copy(env, extra)
	return env
}

// writeGlabStub writes an executable glab stub with the given script,
// returning extraEnv that puts it first on PATH.
func writeGlabStub(t *testing.T, script string, extra map[string]string) map[string]string {
	t.Helper()
	dir := t.TempDir()
	if err := harness.WriteStubAt(dir, "glab", script); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"PATH": dir + string(os.PathListSeparator) + os.Getenv("PATH")}
	maps.Copy(env, extra)
	return env
}

// gitlabReadyStubScript answers the three GitLab live reads: a draft,
// conflicting merge request with one failed pipeline, and one open plus
// one resolved discussion. Anything else fails like the network-dead stub.
const gitlabReadyStubScript = `#!/usr/bin/env bash
echo "stub-glab $*" >> "${EVAL_STUBLOG:-/dev/null}"
case "$*" in
  *"merge_requests/5101/pipelines"*)
    printf '%s' '[{"id":12,"status":"success"},{"id":13,"status":"failed"}]'
    exit 0;;
  *"merge_requests/5101/discussions"*)
    printf '%s' '[{"id":"d1","notes":[{"id":1,"body":"still open","resolved":false,"system":false}]},{"id":"d2","notes":[{"id":2,"body":"lgtm","resolved":true,"system":false}]}]'
    exit 0;;
  *"merge_requests/5101"*)
    printf '%s' '{"id":9001,"iid":5101,"title":"Retry count off by one","description":"A description that is long enough to pass the thin check easily here.","state":"opened","draft":true,"has_conflicts":true,"detailed_merge_status":"conflict","sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","merged_at":null,"head_pipeline":{"id":12,"status":"success","sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}'
    exit 0;;
esac
echo "stub-glab: no match ($*)" >&2
exit 1
`

// readyBundle writes a PR bundle with a findings store for the advisory.
func readyBundle(t *testing.T, dir, pr string) {
	t.Helper()
	mkPRBundle(t, dir, pr)
	writeFiles(t, dir, map[string]string{
		".verdict/state/" + pr + "/findings.json": mixedFindings(),
	})
}

// TestReadyGolden pins the ready verb: the dead-gh
// refusal (exit 1 advisory, exit 3 under --check), the advisory and gate
// over stubbed live state, the local/provider/missing-bundle refusals,
// and the staged probe err file (byte-exact).
func TestReadyGolden(t *testing.T) {
	t.Parallel()

	dead := func(t *testing.T, dir, _ string) {
		t.Helper()
		readyBundle(t, dir, "5101")
	}

	goArgs := splitRepo("ready", "5101")
	goldenRead(t, "ready/refused-dead", dead, nil,
		allowKeys("C/.verdict/state/5101/.ready.err"), goArgs...)

	goArgs = splitRepo("ready", "5101", "--json")
	goldenRead(t, "ready/refused-dead-json", dead, nil,
		allowKeys("C/.verdict/state/5101/.ready.err"), goArgs...)

	goArgs = splitRepo("ready", "5101", "--check", "--json")
	goldenRead(t, "ready/refused-dead-check", dead, nil,
		allowKeys("C/.verdict/state/5101/.ready.err"), goArgs...)

	blocked := writeStub(t, readyStubScript, map[string]string{"READY_THREADS": "open"})
	goArgs = splitRepo("ready", "5101")
	goldenRead(t, "ready/blocked-human", dead, blocked,
		allowKeys("C/.verdict/state/5101/.ready.err"), goArgs...)

	goArgs = splitRepo("ready", "5101", "--json")
	goldenRead(t, "ready/blocked-json", dead, blocked,
		allowKeys("C/.verdict/state/5101/.ready.err"), goArgs...)

	goArgs = splitRepo("ready", "5101", "--check")
	goldenRead(t, "ready/blocked-check", dead, blocked,
		allowKeys("C/.verdict/state/5101/.ready.err"), goArgs...)

	allClear := writeStub(t, readyStubScript, map[string]string{"READY_THREADS": "clear"})
	goArgs = splitRepo("ready", "5101", "--check", "--json")
	goldenRead(t, "ready/ready-check-json", dead, allClear,
		allowKeys("C/.verdict/state/5101/.ready.err"), goArgs...)

	local := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkLocalBundle(t, dir, "local-work")
		writeFiles(t, dir, map[string]string{
			".verdict/state/local-work/findings.json": mixedFindings(),
		})
	}
	goArgs = splitRepo("ready", "local-work", "--json")
	goldenRead(t, "ready/local-refused", local, nil, nil, goArgs...)

	gitlab := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPRBundle(t, dir, "5101")
		writeFiles(t, dir, map[string]string{
			".verdict/state/5101/PR.json": `{"provider":"forgejo","number":5101,` +
				`"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`,
			".verdict/state/5101/findings.json": mixedFindings(),
		})
	}
	goArgs = splitRepo("ready", "5101", "--json")
	goldenRead(t, "ready/provider-refused", gitlab, nil, nil, goArgs...)

	gitlabBlocked := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPRBundle(t, dir, "5101")
		writeFiles(t, dir, map[string]string{
			".verdict/state/5101/PR.json": `{"provider":"gitlab","number":5101,` +
				`"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`,
			".verdict/state/5101/findings.json": mixedFindings(),
		})
	}
	glab := writeGlabStub(t, gitlabReadyStubScript, nil)
	goArgs = splitRepo("ready", "5101", "--json")
	goldenRead(t, "ready/gitlab-blocked-json", gitlabBlocked, glab,
		allowKeys("C/.verdict/state/5101/.ready.err"), goArgs...)

	missing := func(t *testing.T, _, _ string) {
		t.Helper()
		// No bundle at all: the missing-bundle refusal names the fetch.
	}
	goArgs = splitRepo("ready", "5101", "--json")
	goldenRead(t, "ready/missing-bundle", missing, nil, nil, goArgs...)
}
