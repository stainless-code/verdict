package verdict_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/pkg/verdict"
)

// exampleNow is the deterministic clock every example injects.
var exampleNow = time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

// writeFile writes one fixture file, creating parents.
func writeFile(path, content string) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		panic(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		panic(err)
	}
}

// chdirTemp moves into a fresh temp dir and returns a restore function.
// Examples cannot use [testing.T], so failures panic.
func chdirTemp() func() {
	dir, err := os.MkdirTemp("", "verdict-example-")
	if err != nil {
		panic(err)
	}
	prev, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	if err := os.Chdir(dir); err != nil {
		panic(err)
	}
	return func() {
		if err := os.Chdir(prev); err != nil {
			panic(err)
		}
		os.RemoveAll(dir)
	}
}

// reportBundle writes a minimal .review/5101 bundle: one open finding.
func reportBundle() {
	writeFile(".review/5101/PR.json",
		`{"state":"OPEN","headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",`+
			`"baseRefOid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`)
	writeFile(".review/5101/findings.json",
		`[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,`+
			`"anchored":true,"body":"Usable body.","suggestion":null,`+
			`"inBounds":true,"rigor":"walked","severity":"critical",`+
			`"sig":"c1111111111111111111111111111111111111111","sig_version":"1",`+
			`"firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open",`+
			`"rationale":"","history":[]}]`)
}

// ExampleClient_Report renders the offline review report in-process.
func ExampleClient_Report() {
	defer chdirTemp()()
	reportBundle()
	c := verdict.New(verdict.Options{Now: func() time.Time { return exampleNow }})
	defer c.Close()
	inv := verdict.Invocation{Repo: "acme/app", PR: "5101"}
	resp, err := c.Report(context.Background(), &verdict.ReportRequest{Invocation: inv})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(resp.Human[0])
	fmt.Println(resp.Next)
	// Output:
	// report: .review/5101 (1 shown) open=1 head=aaaaaaa
	// address open threads
}

// ExampleClient_Triage flips a finding status and matches the typed refusal
// for an unknown signature.
func ExampleClient_Triage() {
	defer chdirTemp()()
	reportBundle()
	c := verdict.New(verdict.Options{Now: func() time.Time { return exampleNow }})
	defer c.Close()
	inv := verdict.Invocation{Repo: "acme/app", PR: "5101"}
	tri, err := c.Triage(context.Background(), &verdict.TriageRequest{
		Invocation: inv,
		Sig:        "c1111111111111111111111111111111111111111",
		Status:     "deferred",
		Rationale:  "Deferred pending the upstream fix, with rationale.",
	})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(tri.Human)
	_, err = c.Triage(context.Background(), &verdict.TriageRequest{Invocation: inv, Sig: "deadbeef", Status: "open", Force: true})
	if ref, ok := errors.AsType[*verdict.RefusalError](err); ok {
		fmt.Println("refused:", ref.Code)
		fmt.Println("hint:", ref.Hint != "")
	}
	// Output:
	// triaged c1111111 -> deferred
	// refused: unknown-sig
	// hint: true
}

// ExampleClient_Record merges a local review.json into the stores.
func ExampleClient_Record() {
	defer chdirTemp()()
	writeFile(".review/local-demo/source.json",
		`{"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","base_sha":"`+strings.Repeat("b", 40)+`","head_ref":"work"}`)
	writeFile(".review/local-demo/full.diff",
		"diff --git a/notes.txt b/notes.txt\nindex 1111111..2222222 100644\n"+
			"--- a/notes.txt\n+++ b/notes.txt\n@@ -1,3 +1,3 @@\n top\n-old line\n+new line\n bottom\n")
	writeFile(".review/local-demo/review.json",
		`{"schema_version":"1","body":"> [!IMPORTANT]\n> Record service check with enough length.","approved":false,`+
			`"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"Cited finding here with enough substance.",`+
			`"rigor":"walked","severity":"important"}]}`)
	if _, ref := controls.Resolve(context.Background(), nil, controls.Request{
		BundleDir: ".review/local-demo", BaseSHA: strings.Repeat("b", 40), AllowHead: true,
	}); ref != nil {
		fmt.Println("error:", ref)
		return
	}
	c := verdict.New(verdict.Options{Now: func() time.Time { return exampleNow }})
	defer c.Close()
	inv := verdict.Invocation{Repo: "acme/app", PR: "local-demo"}
	resp, err := c.Record(context.Background(), &verdict.RecordRequest{Invocation: inv})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(resp.At)
	fmt.Println(resp.Next)
	// Output:
	// 2026-11-02T12:00:00Z
	// report or triage the local bundle; post stays refused (no PR target)
}

// scriptCommander is the injected command seam: every spawn is scripted.
type scriptCommander struct {
	got []verdict.Command
	res verdict.CommandResult
}

// Run implements verdict.Commander.
func (s *scriptCommander) Run(_ context.Context, cmd verdict.Command) (verdict.CommandResult, error) {
	s.got = append(s.got, cmd)
	return s.res, nil
}

// TestClientVerifyFakeCommander proves the Commander seam: a scripted runner
// serves the watchdog spawn and the typed outcome maps the nonzero exit and
// combined stderr through, instead of echoing zero values.
func TestClientVerifyFakeCommander(t *testing.T) {
	t.Chdir(t.TempDir())
	writeFile(".review/5201/patch.json", `{"verifyCmd":"printf ok"}`)
	cmdr := &scriptCommander{res: verdict.CommandResult{Stdout: "ok", Stderr: "boom\n", ExitCode: 3}}
	c := verdict.New(verdict.Options{
		Now:       func() time.Time { return exampleNow },
		Commander: cmdr,
	})
	defer c.Close()
	inv := verdict.Invocation{Repo: "acme/app", PR: "5201"}
	resp, err := c.Verify(context.Background(), &verdict.VerifyRequest{Invocation: inv, Timeout: "10"})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	checkVerifySpawn(t, cmdr)
	if resp.Exit != 3 || resp.Tail != "okboom" {
		t.Errorf("outcome = exit %d tail %q, want exit 3 tail okboom", resp.Exit, resp.Tail)
	}
	if resp.TimedOut {
		t.Error("outcome timed out, want a completed run")
	}
	if resp.Next != "proof failed — fix, do not claim" {
		t.Errorf("next = %q, want the failed-proof guidance", resp.Next)
	}
}

// checkVerifySpawn proves the one watchdog spawn fired with the patch's
// verifyCmd.
func checkVerifySpawn(t *testing.T, cmdr *scriptCommander) {
	t.Helper()
	if len(cmdr.got) != 1 || len(cmdr.got[0].Argv) != 3 || cmdr.got[0].Argv[2] != "printf ok" {
		t.Fatalf("commands = %+v, want one bash -c spawn", cmdr.got)
	}
}

// TestClientRefusalErrorsAs proves [errors.As] matches the public refusal
// across the boundary, with code, message, hint, and warnings intact.
func TestClientRefusalErrorsAs(t *testing.T) {
	t.Chdir(t.TempDir())
	c := verdict.New(verdict.Options{Now: func() time.Time { return exampleNow }})
	defer c.Close()
	inv := verdict.Invocation{Repo: "acme/app", PR: "5101"}
	_, err := c.Report(context.Background(), &verdict.ReportRequest{Invocation: inv})
	ref, ok := errors.AsType[*verdict.RefusalError](err)
	if !ok {
		t.Fatalf("err = %v, want *verdict.RefusalError", err)
	}
	if ref.Code == "" || ref.Message == "" || ref.Hint == "" {
		t.Errorf("refusal = %+v, want code/message/hint", ref)
	}
}
