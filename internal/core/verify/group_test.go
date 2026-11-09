package verifycore_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	verifycore "github.com/stainless-code/verdict/internal/core/verify"
)

// groupCommander answers the two argv shapes group validation issues: the
// fixed git head probe and the operator's bash -c command. Each result is
// scripted per test so a nonzero exit and a moved head are distinguishable.
type groupCommander struct {
	head    string
	out     string
	runs    []*core.Command
	exit    int
	headErr bool
	timed   bool
}

func (c *groupCommander) Run(_ context.Context, cmd *core.Command) (core.Result, error) {
	c.runs = append(c.runs, cmd)
	if len(cmd.Argv) > 2 && cmd.Argv[0] == "git" && cmd.Argv[3] == "rev-parse" {
		if c.headErr {
			return core.Result{Stderr: "fatal: not a git repository", ExitCode: 128}, nil
		}
		return core.Result{Stdout: c.head + "\n"}, nil
	}
	return core.Result{Stdout: c.out, ExitCode: c.exit, TimedOut: c.timed}, nil
}

const (
	groupHead = "1234567890abcdef1234567890abcdef12345678"
	groupBase = "abcdef1234567890abcdef1234567890abcdef12"
)

// groupBundle builds the real producer artifacts: a head-policy controls
// snapshot, a written run state with one capped group, and PR metadata
// carrying the reviewed head. No patch.json exists anywhere in the tree.
func groupBundle(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, ".verdict", "state", "7")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	controls := `{"schema_version":"1","policy":"head","base_sha":"` + groupBase + `","sha":"","files":[]}`
	writeGroupFile(t, filepath.Join(dir, "controls.json"), controls)
	writeGroupFile(t, filepath.Join(dir, "PR.json"), `{"headRefOid":"`+groupHead+`","state":"OPEN"}`)
	maxLenses := 1
	rs := &config.RunState{BaseSHA: groupBase, Trusted: true, Resolved: &config.Resolved{Plan: config.Plan{
		Status: config.PendingStatus,
		Groups: []config.PlanGroup{{ID: "group-a1", Label: "deep-a", Profile: "deep-a", Depth: "deep", MaxLenses: &maxLenses}},
	}}}
	if serr := config.WriteRunState(dir, rs); serr != nil {
		t.Fatalf("WriteRunState: %v", serr)
	}
	return root
}

func writeGroupFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func groupRequest(root, group, command string, cmdr core.Commander) *core.VerifyRequest {
	return &core.VerifyRequest{
		Repo: "acme/app", Pr: "7", RepoRoot: root, Runtime: &core.Runtime{Commander: cmdr},
		TimeoutRaw: "30",
		Group:      group,
		Command:    command,
	}
}

// TestGroupValidationRecordsMatchingReceipt proves the positive path records
// a receipt bound to the run capture and renders the success next step.
func TestGroupValidationRecordsMatchingReceipt(t *testing.T) {
	root := groupBundle(t)
	cmdr := &groupCommander{head: groupHead, out: "ok\n"}
	resp, ref := verifycore.Run(context.Background(), groupRequest(root, "group-a1", "go test ./...", cmdr))
	if ref != nil {
		t.Fatalf("Run: %v", ref)
	}
	if !strings.Contains(resp.Next, "validation holds") {
		t.Fatalf("next = %q, want the passing next step", resp.Next)
	}
	dir := filepath.Join(root, ".verdict", "state", "7")
	requireBoundReceipt(t, dir, "go test ./...")
	// Two head probes bracket one command: the receipt binds both the head
	// the command started on and the head it finished on.
	if len(cmdr.runs) != 3 {
		t.Fatalf("command runs = %d, want two head probes and one command", len(cmdr.runs))
	}
}

// requireBoundReceipt asserts no patch was fabricated and the receipt binds
// the group, both heads, the run capture, and the command.
func requireBoundReceipt(t *testing.T, dir, command string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(dir, "patch.json")); !os.IsNotExist(err) {
		t.Fatalf("group validation fabricated patch.json")
	}
	receipt, found, ref := verifycore.ReadValidationReceipt(dir, "group-a1")
	if ref != nil || !found {
		t.Fatalf("receipt found=%v ref=%v", found, ref)
	}
	if !receipt.Passed || receipt.Exit != 0 || receipt.Group != "group-a1" ||
		receipt.ReviewedHead != groupHead || receipt.ExecutionHead != groupHead ||
		receipt.Command != command || receipt.Run != "7" || receipt.InputsSHA256 == "" {
		t.Fatalf("receipt = %+v", receipt)
	}
}

// runVisibleCase asserts one refusal leaves no receipt, while an executed
// failure records a receipt and never renders a pass.
func runVisibleCase(t *testing.T, group, command string, cmdr *groupCommander, wantCode string) {
	t.Helper()
	root := groupBundle(t)
	resp, ref := verifycore.Run(context.Background(), groupRequest(root, group, command, cmdr))
	dir := filepath.Join(root, ".verdict", "state", "7")
	if wantCode != "" {
		if ref == nil || ref.Code != wantCode {
			t.Fatalf("ref = %v, want code %q", ref, wantCode)
		}
		if _, err := os.Stat(filepath.Join(dir, "validation-"+group+".json")); err == nil {
			t.Fatalf("refused run left a receipt")
		}
		return
	}
	if ref != nil {
		t.Fatalf("Run: %v", ref)
	}
	requireFailedReceipt(t, dir, group, resp.Exit)
}

// requireFailedReceipt asserts an executed failure is recorded and never
// renders a pass.
func requireFailedReceipt(t *testing.T, dir, group string, exit int) {
	t.Helper()
	receipt, found, rr := verifycore.ReadValidationReceipt(dir, group)
	if rr != nil || !found {
		t.Fatalf("receipt found=%v ref=%v", found, rr)
	}
	if receipt.Passed || exit == 0 {
		t.Fatalf("failed run certified a pass: receipt=%+v exit=%d", receipt, exit)
	}
}

// TestGroupValidationFailuresStayVisible proves every failure mode stays
// visible: a refusal leaves no receipt, an executed failure records one.
func TestGroupValidationFailuresStayVisible(t *testing.T) {
	tests := []struct {
		name     string
		group    string
		command  string
		cmdr     *groupCommander
		wantCode string
		wantPass bool
	}{
		{
			name: "nonzero exit records a failed receipt", group: "group-a1", command: "go test ./...",
			cmdr: &groupCommander{head: groupHead, out: "FAIL", exit: 1},
		},
		{
			name: "timed out records a failed receipt", group: "group-a1", command: "go test ./...",
			cmdr: &groupCommander{head: groupHead, out: "partial", exit: 137, timed: true},
		},
		{
			name: "unreadable head refuses", group: "group-a1", command: "true",
			cmdr: &groupCommander{headErr: true}, wantCode: "verify-failed",
		},
		{
			name: "checked-out head is not the reviewed head", group: "group-a1", command: "true",
			cmdr: &groupCommander{head: strings.Repeat("9", 40)}, wantCode: "head-mismatch",
		},
		{
			name: "unknown group refuses", group: "group-zzz", command: "true",
			cmdr: &groupCommander{head: groupHead}, wantCode: "unknown-group",
		},
		{
			name: "command without group refuses", group: "", command: "true",
			cmdr: &groupCommander{head: groupHead}, wantCode: "bad-group-validation",
		},
		{
			name: "group without command refuses", group: "group-a1", command: "",
			cmdr: &groupCommander{head: groupHead}, wantCode: "bad-group-validation",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { runVisibleCase(t, tt.group, tt.command, tt.cmdr, tt.wantCode) })
	}
}

// TestGroupValidationRejectsStaleCapture proves a tampered run capture
// refuses controls-stale before any command runs.
func TestGroupValidationRejectsStaleCapture(t *testing.T) {
	root := groupBundle(t)
	dir := filepath.Join(root, ".verdict", "state", "7")
	raw, err := os.ReadFile(filepath.Join(dir, "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	doc["base_sha"] = strings.Repeat("0", 40)
	tampered, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "run.json"), tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	cmdr := &groupCommander{head: groupHead}
	_, ref := verifycore.Run(context.Background(), groupRequest(root, "group-a1", "true", cmdr))
	if ref == nil || ref.Code != "controls-stale" {
		t.Fatalf("ref = %v, want controls-stale", ref)
	}
	if len(cmdr.runs) != 0 {
		t.Fatalf("command ran on tampered capture: %v", cmdr.runs)
	}
}

// mutatingCommander rewrites the run capture while the command is "running",
// proving a command run against an older capture is never blessed.
type mutatingCommander struct {
	dir string
	groupCommander
}

func (c *mutatingCommander) Run(ctx context.Context, cmd *core.Command) (core.Result, error) {
	if len(cmd.Argv) > 2 && cmd.Argv[0] == "bash" {
		if err := rewriteCapture(c.dir); err != nil {
			return core.Result{}, err
		}
	}
	return c.groupCommander.Run(ctx, cmd)
}

// rewriteCapture rewrites one identity field of the real producer run.json,
// the mutation a mid-command re-fetch would cause.
func rewriteCapture(dir string) error {
	path := filepath.Join(dir, "run.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return err
	}
	doc["captured_at"] = "2026-11-06T00:00:00Z"
	doc["base_sha"] = strings.Repeat("b", 40)
	next, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	return os.WriteFile(path, next, 0o600)
}

// TestGroupValidationRejectsCaptureMovedDuringCommand pins the receipt
// identity to the capture pinned before execution: a capture rewritten while
// the command ran records unavailable evidence and refuses.
func TestGroupValidationRejectsCaptureMovedDuringCommand(t *testing.T) {
	root := groupBundle(t)
	dir := filepath.Join(root, ".verdict", "state", "7")
	cmdr := &mutatingCommander{head: groupHead, out: "ok\n", dir: dir}
	resp, ref := verifycore.Run(context.Background(), groupRequest(root, "group-a1", "true", cmdr))
	if ref != nil {
		t.Fatalf("capture move should be recorded evidence, not a pre-execution refusal: %v", ref)
	}
	receipt, found, rr := verifycore.ReadValidationReceipt(dir, "group-a1")
	if rr != nil || !found {
		t.Fatalf("receipt found=%v ref=%v", found, rr)
	}
	if receipt.Status != "unavailable" || receipt.Passed || resp.Next == "validation holds for this group and head — the receipt is recorded" {
		t.Fatalf("capture rewritten during the command was certified: %+v", receipt)
	}
}

// TestDeepGapsReadsReceiptsWithoutExecuting proves the record/report
// visibility surface: missing and stale receipts are reported as gaps, a
// current receipt is silent, and no command runs to produce the answer.
//
// "No command runs" is structural, not asserted: DeepGaps takes no commander
// and config.RunState carries none, so there is no channel through which it
// could execute. A run-count assertion here could never fire; the signature is
// the guard, and a test that pretended otherwise would be decoration.
func TestDeepGapsReadsReceiptsWithoutExecuting(t *testing.T) {
	root := groupBundle(t)
	dir := filepath.Join(root, ".verdict", "state", "7")
	rs, ref := config.VerifyRunState(dir)
	if ref != nil {
		t.Fatalf("VerifyRunState: %v", ref)
	}
	cmdr := &groupCommander{head: groupHead, out: "ok\n"}
	requireSingleGap(t, verifycore.DeepGaps(rs, dir, groupHead, root), "deep validation pending")
	if _, r2 := verifycore.Run(context.Background(), groupRequest(root, "group-a1", "true", cmdr)); r2 != nil {
		t.Fatalf("Run: %v", r2)
	}
	if gaps := verifycore.DeepGaps(rs, dir, groupHead, root); len(gaps) != 0 {
		t.Fatalf("current receipt still reported: %v", gaps)
	}
	requireSingleGap(t, verifycore.DeepGaps(rs, dir, strings.Repeat("9", 40), root), "deep validation stale")
}

// requireSingleGap asserts exactly one gap line carrying the state token.
func requireSingleGap(t *testing.T, gaps []string, token string) {
	t.Helper()
	if len(gaps) != 1 || !strings.Contains(gaps[0], token) {
		t.Fatalf("gaps = %v, want one %q line", gaps, token)
	}
}
