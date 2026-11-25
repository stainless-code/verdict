package postcore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
)

func TestDryRunRetainsReproduction(t *testing.T) {
	t.Chdir(t.TempDir())
	postBundle(t, postReproductionReview)
	var warnings, human []string
	bundle := filepath.Join(".verdict", "state", "7")
	req := postTestReq(postCommander(t), Flags{Approve: "false", DryRun: true, ReviewFile: filepath.Join(bundle, "review.json")}, &warnings, &human)
	if _, ref := Run(t.Context(), req); ref != nil {
		t.Fatal(ref)
	}
	raw, err := os.ReadFile(filepath.Join(bundle, "findings.dryrun.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"command": "sh tests/e4c372b6be85faab079dae8ace6c8081a75c950f-2ed27c1421e6.sh"`) {
		t.Fatalf("stored reproduction = %s", raw)
	}
	checkDryRunReproductionFiles(t, bundle)
}

func checkDryRunReproductionFiles(t *testing.T, bundle string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(bundle, "tests", "e4c372b6be85faab079dae8ace6c8081a75c950f-2ed27c1421e6.sh"))
	if err != nil || string(raw) != "false\n" {
		t.Fatalf("retained test = %q, %v", raw, err)
	}
	raw, err = os.ReadFile(filepath.Join(bundle, "tests", "e4c372b6be85faab079dae8ace6c8081a75c950f-2ed27c1421e6.command.json"))
	if err != nil || string(raw) != `{"path":"tests/e4c372b6be85faab079dae8ace6c8081a75c950f-2ed27c1421e6.sh","command":"sh tests/e4c372b6be85faab079dae8ace6c8081a75c950f-2ed27c1421e6.sh","redacted":false}` {
		t.Fatalf("retained command = %q, %v", raw, err)
	}
}

const postReproductionReview = `{"schema_version":"1","findings":[{"path":"notes.txt","line":2,"side":"RIGHT","severity":"important","claim":"A reproduced defect.","quote":"new line","confidence":"high","rigor":"reproduced","reproduction":{"path":"","command":"false"}}],"receipts":[{"hunk":"notes.txt\tNEW\t1\t3","quote":"new line"}]}`

func TestDryRunPreservesRecordedReproduction(t *testing.T) {
	t.Chdir(t.TempDir())
	postBundle(t, postReproductionReview)
	var warnings, human []string
	bundle := filepath.Join(".verdict", "state", "7")
	req := postTestReq(postCommander(t), Flags{Approve: "false", DryRun: true, ReviewFile: filepath.Join(bundle, "review.json")}, &warnings, &human)
	recordReproductionPhase(t, req, bundle)
	changed := strings.Replace(postReproductionReview, `"command":"false"`, `"command":"true"`, 1)
	if err := os.WriteFile(filepath.Join(bundle, "review.json"), []byte(changed), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ref := Run(t.Context(), req); ref != nil {
		t.Fatal(ref)
	}
	checkRecordedReproduction(t, bundle)
}

func checkRecordedReproduction(t *testing.T, bundle string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(bundle, "findings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"command": "sh tests/e4c372b6be85faab079dae8ace6c8081a75c950f-2ed27c1421e6.sh"`) {
		t.Fatalf("recorded receipt changed: %s", raw)
	}
	checkDryRunReproductionFiles(t, bundle)
	raw, err = os.ReadFile(filepath.Join(bundle, "findings.dryrun.json"))
	if err != nil || !strings.Contains(string(raw), `"command": "sh tests/e4c372b6be85faab079dae8ace6c8081a75c950f-a17fcf0a2f50.sh"`) {
		t.Fatalf("new draft receipt = %s, %v", raw, err)
	}
}

func recordReproductionPhase(t *testing.T, req *Request, bundle string) {
	t.Helper()
	if err := recordcore.EnsureHunksSnapshot(*req.Runtime, bundle); err != nil {
		t.Fatal(err)
	}
	review, err := recordcore.LoadReview(filepath.Join(bundle, "review.json"))
	if err != nil {
		t.Fatal(err)
	}
	snap, ref := controls.Gate(bundle, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	if ref != nil {
		t.Fatal(ref)
	}
	_, err = recordcore.Merge(t.Context(), req.Runtime, &recordcore.MergeParams{
		Dir: bundle, Prov: "github", HeadFull: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Review: review,
		ReviewFile: filepath.Join(bundle, "review.json"), FindingsFile: filepath.Join(bundle, "findings.json"), Root: ".", RepoRoot: ".", Repo: "acme/app", Pr: "7", Cmd: "record", Effective: core.Defaults(), Controls: snap,
	})
	if err != nil {
		t.Fatal(err)
	}
}
