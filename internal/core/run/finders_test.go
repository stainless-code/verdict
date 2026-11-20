package run

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

func TestDefaultLaunchUsesTwoFinders(t *testing.T) {
	req, commander := coordinatorFixture(t)
	req.FindersRaw = ""
	response, ref := Run(t.Context(), req)
	if ref != nil {
		t.Fatal(ref)
	}
	if got := strings.Join(commander.phases, ","); got != "x-finder-1,x-finder-2,x-cross-1,x-cross-2" {
		t.Fatalf("launches = %s", got)
	}
	if response.Data.Execution == nil || response.Data.Execution.Counts.Completed != 2 {
		t.Fatalf("execution = %+v", response.Data.Execution)
	}
	manifest := readPhaseObject(t, filepath.Join(commander.dir, "launcher.json"))
	count, _ := jq.IntOf(readcommon.FieldOf(manifest, "finders"))
	if readcommon.FieldOf(manifest, "finder_source") != "built-in" || count != 2 {
		t.Fatalf("policy = %s", manifest.MarshalCompact())
	}
}

func TestSingleFinderKeepsLegacyArtifacts(t *testing.T) {
	root := fixtureRoot(t)
	dir := bundleDir(root)
	commander := &scripted{write: func() { writeReview(t, dir) }}
	req := request(root, commander)
	response, ref := Run(t.Context(), req)
	if ref != nil {
		t.Fatal(ref)
	}
	if commander.calls != 1 || response.Data.Execution != nil || response.Data.Repair != nil {
		t.Fatalf("launches=%d data=%+v", commander.calls, response.Data)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if !slices.Equal(names, []string{".run.out", "PR.json", "full.diff", "launcher.json", "review.json"}) {
		t.Fatalf("single finder artifacts = %q", names)
	}
}

func TestTwoFinderRenderingRefusesWithoutWrites(t *testing.T) {
	for _, stage := range []string{"", "1"} {
		t.Run("stage="+stage, func(t *testing.T) {
			assertRenderingRefuses(t, stage)
		})
	}
}

func TestFinderCountRejectsInvalid(t *testing.T) {
	for _, raw := range []string{"0", "3", "two", "+1", "01"} {
		t.Run(raw, func(t *testing.T) {
			req, _ := coordinatorFixture(t)
			req.FindersRaw = raw
			if _, ref := Run(t.Context(), req); ref == nil || ref.Code != "bad-flags" {
				t.Fatalf("count %s: %v", raw, ref)
			}
		})
	}
}

func TestCapturedSingleFinderDefault(t *testing.T) {
	root := fixtureRoot(t)
	dir := bundleDir(root)
	writeUnder(t, dir, "run.json", `{"schema_version":"1","resolved":{"defaults":{"values":{"finders":1},"origins":{"finders":{"layer":"repo","source":"base"}}}}}`)
	commander := &scripted{write: func() { writeReview(t, dir) }}
	req := request(root, commander)
	req.FindersRaw = ""
	response, ref := Run(t.Context(), req)
	if ref != nil || commander.calls != 1 || response.Data.Execution != nil {
		t.Fatalf("captured single finder: calls=%d ref=%v", commander.calls, ref)
	}
}

func TestFinderFlagOverridesCapturedDefault(t *testing.T) {
	req, commander := coordinatorFixture(t)
	writeUnder(t, commander.dir, "run.json", `{"schema_version":"1","resolved":{"defaults":{"values":{"finders":1},"origins":{"finders":{"layer":"repo","source":"base"}}}}}`)
	req.FindersRaw = "2"
	if _, ref := Run(t.Context(), req); ref != nil {
		t.Fatal(ref)
	}
	if got := strings.Join(commander.phases, ","); got != "x-finder-1,x-finder-2,x-cross-1,x-cross-2" {
		t.Fatalf("launches = %s", got)
	}
	capture := readPhaseObject(t, filepath.Join(commander.dir, "run.json"))
	if readcommon.FieldOf(capture, "finder_source") != "flag" {
		t.Fatalf("override source = %s", capture.MarshalCompact())
	}
}

func assertRenderingRefuses(t *testing.T, stage string) {
	t.Helper()
	req, commander := coordinatorFixture(t)
	req.FindersRaw = "2"
	req.Print = true
	req.StageRaw = stage
	_, ref := Run(t.Context(), req)
	if ref == nil || ref.Code != "bad-flags" || !strings.Contains(ref.Message, "--finders 1") {
		t.Fatalf("refusal = %v", ref)
	}
	if len(commander.phases) != 0 {
		t.Fatalf("render launched %v", commander.phases)
	}
	if _, err := os.Stat(filepath.Join(commander.dir, "launcher.json")); !os.IsNotExist(err) {
		t.Fatalf("render wrote manifest: %v", err)
	}
}
