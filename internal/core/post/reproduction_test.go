package postcore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDryRunRetainsReproduction(t *testing.T) {
	t.Chdir(t.TempDir())
	postBundle(t, `{"schema_version":"1","findings":[{"path":"notes.txt","line":2,"side":"RIGHT","severity":"important","claim":"A reproduced defect.","quote":"new line","confidence":"high","rigor":"reproduced","reproduction":{"path":"","command":"false"}}],"receipts":[{"hunk":"notes.txt\tNEW\t1\t3","quote":"new line"}]}`)
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
	if !strings.Contains(string(raw), `"command": "sh tests/e4c372b6be85faab079dae8ace6c8081a75c950f.sh"`) {
		t.Fatalf("stored reproduction = %s", raw)
	}
	checkDryRunReproductionFiles(t, bundle)
}

func checkDryRunReproductionFiles(t *testing.T, bundle string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(bundle, "tests", "e4c372b6be85faab079dae8ace6c8081a75c950f.sh"))
	if err != nil || string(raw) != "false\n" {
		t.Fatalf("retained test = %q, %v", raw, err)
	}
	raw, err = os.ReadFile(filepath.Join(bundle, "tests", "e4c372b6be85faab079dae8ace6c8081a75c950f.command.json"))
	if err != nil || string(raw) != `{"path":"tests/e4c372b6be85faab079dae8ace6c8081a75c950f.sh","command":"sh tests/e4c372b6be85faab079dae8ace6c8081a75c950f.sh","redacted":false}` {
		t.Fatalf("retained command = %q, %v", raw, err)
	}
}
