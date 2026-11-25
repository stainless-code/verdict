package run

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/agentcatalog"
	"github.com/stainless-code/verdict/internal/core"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
)

func TestLaunchTargetSeparatesEvidenceAndOutput(t *testing.T) {
	t.Parallel()
	root := fixtureRoot(t)
	evidence := bundleDir(root)
	output := filepath.Join(evidence, "x-finder-1")
	st := &state{req: request(root, &scripted{})}
	target := launchTarget{
		evidence: evidence, output: output, procedure: filepath.Join(output, "procedure.md"), artifact: "candidate.json",
	}

	args, ref := st.argsForTarget(&agentcatalog.Launch{Name: "claude", Binary: "claude"}, 30, target)
	if ref != nil {
		t.Fatal(ref)
	}
	if args.abs != filepath.Join(root, ".verdict", "state", "42", "x-finder-1", "candidate.json") {
		t.Fatalf("artifact = %q", args.abs)
	}
	if args.dir != output {
		t.Fatalf("output = %q", args.dir)
	}
	procedure := filepath.ToSlash(filepath.Join(".verdict", "state", "42", "x-finder-1", "procedure.md"))
	if !strings.Contains(args.prompt, "Read `"+procedure+"` first;") {
		t.Fatalf("procedure missing: %s", args.prompt)
	}
}

func TestLaunchTargetChecker(t *testing.T) {
	t.Parallel()
	checked := false
	args := &launchArgs{target: launchTarget{
		evidence: "evidence",
		checker: func(dir string, raw []byte) (recordcore.CheckResult, *core.RefusalError) {
			checked = true
			if dir != "evidence" {
				t.Fatalf("evidence = %q", dir)
			}
			if string(raw) != "candidate bytes" {
				t.Fatalf("bytes = %q", raw)
			}
			return recordcore.CheckResult{}, &core.RefusalError{Code: "sentinel"}
		},
	}}
	_, ref := args.checkReview([]byte("candidate bytes"))
	if !checked {
		t.Fatal("checker was not called")
	}
	if ref.Code != "sentinel" {
		t.Fatalf("checker result = %v", ref)
	}
}

func TestLaunchTargetDefaultCheckerReadsEvidence(t *testing.T) {
	t.Parallel()
	dir := bundleDir(fixtureRoot(t))
	args := &launchArgs{dir: filepath.Join(dir, "x-finder-1"), target: launchTarget{evidence: dir}}
	result, ref := args.checkReview([]byte(reviewDoc))
	if ref != nil || result.Errors != 0 {
		t.Fatalf("default check = %+v, %v", result, ref)
	}
}
