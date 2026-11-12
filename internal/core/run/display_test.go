package run

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestDisplayPassThrough pins the agent launch contract.
func TestDisplayPassThrough(t *testing.T) {
	args := []string{"--model", "opus", "--max-budget-usd=2.5"}
	got, safe := displayPassThrough(args, nil)
	if !safe || !slices.Equal(got, []string{"--model", "opus", "--max-budget-usd=2.5"}) {
		t.Fatalf("safe options = %q, %v", got, safe)
	}
	for _, args := range [][]string{{"--token", "credential"}, {"https://user:password@host"}, {"--model", "secretvalue"}, {"--custom=private-value-123"}} {
		got, safe := displayPassThrough(args, []string{"API_KEY=secretvalue"})
		if safe || strings.Contains(strings.Join(got, " "), "credential") || strings.Contains(strings.Join(got, " "), "secretvalue") {
			t.Fatalf("unsafe options exposed: %q, %v", got, safe)
		}
	}
}

// TestPrintRefusesUnsafeArguments pins the agent launch contract.
func TestPrintRefusesUnsafeArguments(t *testing.T) {
	req := request(fixtureRoot(t), &scripted{})
	req.Print = true
	req.PassThrough = []string{"--custom=private-value-123"}
	_, ref := Run(t.Context(), req)
	if ref == nil || ref.Code != codeBadFlags || strings.Contains(ref.Message, "private-value-123") {
		t.Fatalf("unsafe print refusal = %v", ref)
	}
}

// TestMissingAgentInstallStep pins the agent launch contract.
func TestMissingAgentInstallStep(t *testing.T) {
	req := request(fixtureRoot(t), &scripted{})
	req.Agent = "codex"
	req.LookPath = missingLookPath
	_, ref := Run(t.Context(), req)
	if ref == nil || ref.Code != codeAgentMissing || !strings.Contains(ref.Message, "npm install -g @openai/codex") {
		t.Fatalf("missing agent refusal = %v", ref)
	}
}

// TestAgentProbePrecedesDirective pins the agent launch contract.
func TestAgentProbePrecedesDirective(t *testing.T) {
	root := fixtureRoot(t)
	cmdr := &scripted{write: func() { writeReview(t, bundleDir(root)) }}
	req := request(root, cmdr)
	req.LookPath = func(string) (string, error) {
		req.Pr = "99"
		return "/opt/agents/claude", nil
	}
	if _, ref := Run(t.Context(), req); ref != nil {
		t.Fatal(ref)
	}
	if !strings.Contains(cmdr.cmd.Argv[len(cmdr.cmd.Argv)-1], "PR 99") {
		t.Fatal("directive assembled before binary probe")
	}
}

// TestProvenanceHidesUnknownArguments pins the agent launch contract.
func TestProvenanceHidesUnknownArguments(t *testing.T) {
	root := fixtureRoot(t)
	cmdr := &scripted{write: func() { writeReview(t, bundleDir(root)) }}
	req := request(root, cmdr)
	req.PassThrough = []string{"--custom=private-value-123"}
	if _, ref := Run(t.Context(), req); ref != nil {
		t.Fatal(ref)
	}
	if !slices.Contains(cmdr.cmd.Argv, "--custom=private-value-123") {
		t.Fatal("pass-through argument changed")
	}
	data, err := os.ReadFile(filepath.Join(bundleDir(root), "launcher.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "private-value-123") {
		t.Fatalf("provenance exposed argument: %s", data)
	}
}

// TestPrintedArgvMatchesSpawn pins the agent launch contract.
func TestPrintedArgvMatchesSpawn(t *testing.T) {
	root := fixtureRoot(t)
	cmdr := &scripted{write: func() { writeReview(t, bundleDir(root)) }}
	req := request(root, cmdr)
	req.PassThrough = []string{"--model", "opus", "--max-budget-usd", "2.5"}
	req.Print = true
	printed, ref := Run(t.Context(), req)
	if ref != nil {
		t.Fatal(ref)
	}
	req.Print = false
	if _, ref := Run(t.Context(), req); ref != nil {
		t.Fatal(ref)
	}
	if !slices.Equal(printed.Data.Argv, cmdr.cmd.Argv) {
		t.Fatalf("printed %q differs from spawned %q", printed.Data.Argv, cmdr.cmd.Argv)
	}
}
