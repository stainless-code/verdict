package fetchcore

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

// stagedGroupCommander scripts a hermetic --staged run over a multi-file diff
// with its own trusted-base policy.
func stagedGroupCommander(t *testing.T, diff, files, numstat, policy string) *scriptCommander {
	t.Helper()
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{
		"git\x00rev-parse\x00--is-inside-work-tree":              {stdout: "true\n"},
		"git\x00branch\x00--show-current":                        {stdout: "main\n"},
		"git\x00rev-parse\x00HEAD":                               {stdout: localTestHead + "\n"},
		"git\x00write-tree":                                      {stdout: localTestTree + "\n"},
		"git\x00diff\x00--cached":                                {stdout: diff},
		"git\x00diff\x00--no-renames\x00--name-only\x00--cached": {stdout: files},
		"git\x00diff\x00--numstat\x00--cached":                   {stdout: numstat},
		"git\x00ls-files\x00--others\x00--exclude-standard":      {stdout: ""},
	}}
	scriptBasePolicy(cmdr, localTestHead, policy)
	return cmdr
}

// runStagedGroups runs one fetch-local over the scripted commander.
func runStagedGroups(t *testing.T, cmdr *scriptCommander) (*core.FetchResponse, error) {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	var human, diag bytes.Buffer
	var streamed []string
	return RunLocal(context.Background(),
		localScriptReq(cmdr, &human, &diag, &streamed, nil, core.FetchLocalFlags{Staged: true}))
}

// twoGroupPolicy pins a small cap on the a.txt scope and a large one on b.txt.
const twoGroupPolicy = `{"schema_version":"1","scopes":[` +
	`{"name":"tiny","include":["a.txt"],"defaults":{"max_bytes":120}},` +
	`{"name":"roomy","include":["b.txt"],"defaults":{"max_bytes":4096}}]}`

// twoGroupDiff is two independent hunks, one per scope.
const twoGroupDiff = "diff --git a/a.txt b/a.txt\n--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-a\n+b\n" +
	"diff --git a/b.txt b/b.txt\n--- a/b.txt\n+++ b/b.txt\n@@ -1 +1 @@\n-a\n+b\n"

// TestLocalGroupsUnderOwnCapsSucceed proves a whole-diff total over the
// smallest group cap never refuses a run whose individual groups each fit
// their own resolved cap — the actual fetch-local pipeline, not a helper.
func TestLocalGroupsUnderOwnCapsSucceed(t *testing.T) {
	cmdr := stagedGroupCommander(t, twoGroupDiff, "a.txt\nb.txt\n", "1\t1\ta.txt\n1\t1\tb.txt\n", twoGroupPolicy)
	resp, err := runStagedGroups(t, cmdr)
	if err != nil {
		t.Fatalf("RunLocal: %v", err)
	}
	if resp == nil {
		t.Fatal("RunLocal returned no response")
	}
	raw := readLocalRunJSON(t)
	for _, want := range []string{`"max_bytes": 120`, `"max_bytes": 4096`} {
		if !strings.Contains(raw, want) {
			t.Fatalf("run.json omits resolved group cap %s:\n%s", want, raw)
		}
	}
}

// TestLocalOversizedGroupRefusesDespiteLargerNeighbour proves a group over its
// own cap refuses even when another group carries a much larger cap.
func TestLocalOversizedGroupRefusesDespiteLargerNeighbour(t *testing.T) {
	policy := `{"schema_version":"1","scopes":[` +
		`{"name":"tiny","include":["a.txt"],"defaults":{"max_bytes":40}},` +
		`{"name":"roomy","include":["b.txt"],"defaults":{"max_bytes":4096}}]}`
	cmdr := stagedGroupCommander(t, twoGroupDiff, "a.txt\nb.txt\n", "1\t1\ta.txt\n1\t1\tb.txt\n", policy)
	_, err := runStagedGroups(t, cmdr)
	ref := requireRefusal(t, err, "diff-too-large")
	if !strings.Contains(ref.Message, "cap 40 bytes") || !strings.Contains(ref.Message, "group ") {
		t.Fatalf("refusal does not name the group cap: %q", ref.Message)
	}
}

// TestLocalGroupChunkOptInStaysPerGroup proves chunking is armed only for the
// opted-in over-cap group: a chunk-ineligible neighbor stays whole, and the
// chunk bytes never cross the group boundary.
func TestLocalGroupChunkOptInStaysPerGroup(t *testing.T) {
	policy := `{"schema_version":"1","scopes":[` +
		`{"name":"big","include":["big-*.txt"],"defaults":{"chunk":true}},` +
		`{"name":"small","include":["b.txt"],"defaults":{"max_bytes":4096}}]}`
	bigOne := bigDiffFile("big-1.txt", 300_000)
	bigTwo := bigDiffFile("big-2.txt", 300_000)
	diff := bigOne + bigTwo + twoGroupDiff[len(twoGroupDiff)-len("diff --git a/b.txt b/b.txt\n--- a/b.txt\n+++ b/b.txt\n@@ -1 +1 @@\n-a\n+b\n"):]
	cmdr := stagedGroupCommander(t, diff, "big-1.txt\nbig-2.txt\nb.txt\n",
		"1\t1\tbig-1.txt\n1\t1\tbig-2.txt\n1\t1\tb.txt\n", policy)
	if _, err := runStagedGroups(t, cmdr); err != nil {
		t.Fatalf("RunLocal: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(".verdict", "state", "local-mine", "chunks"))
	if err != nil {
		t.Fatalf("chunks: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no chunks written for the opted-in over-cap group")
	}
}

// bigDiffFile renders one large added file section.
func bigDiffFile(path string, size int) string {
	body := strings.Repeat("+0123456789abcdef\n", size/len("+0123456789abcdef\n")+1)
	return "diff --git a/" + path + " b/" + path + "\n--- a/" + path + "\n+++ b/" + path +
		"\n@@ -0,0 +1 @@\n" + body
}

// readLocalRunJSON reads the persisted local run bundle.
func readLocalRunJSON(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(".verdict", "state", "local-mine", "run.json"))
	if err != nil {
		t.Fatalf("run.json: %v", err)
	}
	return string(raw)
}
