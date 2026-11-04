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

// readyBundle writes a PR bundle with a findings store for the advisory.
func readyBundle(t *testing.T, dir, pr string) {
	t.Helper()
	mkPRBundle(t, dir, pr)
	writeFiles(t, dir, map[string]string{
		".review/" + pr + "/findings.json": mixedFindings(),
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
		allowKeys("C/.review/5101/.ready.err"), goArgs...)

	goArgs = splitRepo("ready", "5101", "--json")
	goldenRead(t, "ready/refused-dead-json", dead, nil,
		allowKeys("C/.review/5101/.ready.err"), goArgs...)

	goArgs = splitRepo("ready", "5101", "--check", "--json")
	goldenRead(t, "ready/refused-dead-check", dead, nil,
		allowKeys("C/.review/5101/.ready.err"), goArgs...)

	blocked := writeStub(t, readyStubScript, map[string]string{"READY_THREADS": "open"})
	goArgs = splitRepo("ready", "5101")
	goldenRead(t, "ready/blocked-human", dead, blocked,
		allowKeys("C/.review/5101/.ready.err"), goArgs...)

	goArgs = splitRepo("ready", "5101", "--json")
	goldenRead(t, "ready/blocked-json", dead, blocked,
		allowKeys("C/.review/5101/.ready.err"), goArgs...)

	goArgs = splitRepo("ready", "5101", "--check")
	goldenRead(t, "ready/blocked-check", dead, blocked,
		allowKeys("C/.review/5101/.ready.err"), goArgs...)

	allClear := writeStub(t, readyStubScript, map[string]string{"READY_THREADS": "clear"})
	goArgs = splitRepo("ready", "5101", "--check", "--json")
	goldenRead(t, "ready/ready-check-json", dead, allClear,
		allowKeys("C/.review/5101/.ready.err"), goArgs...)

	local := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkLocalBundle(t, dir, "local-work")
		writeFiles(t, dir, map[string]string{
			".review/local-work/findings.json": mixedFindings(),
		})
	}
	goArgs = splitRepo("ready", "local-work", "--json")
	goldenRead(t, "ready/local-refused", local, nil, nil, goArgs...)

	gitlab := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPRBundle(t, dir, "5101")
		writeFiles(t, dir, map[string]string{
			".review/5101/PR.json": `{"provider":"gitlab","number":5101,` +
				`"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`,
			".review/5101/findings.json": mixedFindings(),
		})
	}
	goArgs = splitRepo("ready", "5101", "--json")
	goldenRead(t, "ready/provider-refused", gitlab, nil, nil, goArgs...)

	missing := func(t *testing.T, _, _ string) {
		t.Helper()
		// No bundle at all: the missing-bundle refusal names the fetch.
	}
	goArgs = splitRepo("ready", "5101", "--json")
	goldenRead(t, "ready/missing-bundle", missing, nil, nil, goArgs...)
}
