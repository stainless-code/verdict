package verdict_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/testenv"
	"github.com/stainless-code/verdict/pkg/verdict"
)

// exampleNow is the deterministic clock every example injects.
var exampleNow = time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

// writeFile writes one fixture file, creating parents.
func writeFile(path, content string) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		panic(err)
	}
	if err := os.WriteFile(path, []byte(testenv.MaybeFindingsReview(path, content)), 0o644); err != nil {
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

// reportBundle writes a minimal .verdict/state/5101 bundle: one open finding.
func reportBundle() {
	writeFile(".verdict/state/5101/PR.json",
		`{"state":"OPEN","headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",`+
			`"baseRefOid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`)
	writeFile(".verdict/state/5101/findings.json",
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
	// report: .verdict/state/5101 (1 shown) open=1 head=aaaaaaa
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
	writeFile(".verdict/state/local-demo/source.json",
		`{"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","base_sha":"`+strings.Repeat("b", 40)+`","head_ref":"work"}`)
	writeFile(".verdict/state/local-demo/full.diff",
		"diff --git a/notes.txt b/notes.txt\nindex 1111111..2222222 100644\n"+
			"--- a/notes.txt\n+++ b/notes.txt\n@@ -1,3 +1,3 @@\n top\n-old line\n+new line\n bottom\n")
	writeFile(".verdict/state/local-demo/review.json",
		`{"schema_version":"1","body":"> [!IMPORTANT]\n> Record service check with enough length.","approved":false,`+
			`"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"Cited finding here with enough substance.",`+
			`"rigor":"walked","severity":"important"}]}`)
	snap, ref := controls.Resolve(context.Background(), nil, &controls.Request{
		BundleDir: ".verdict/state/local-demo", BaseSHA: strings.Repeat("b", 40), AllowHead: true,
	})
	if ref != nil {
		fmt.Println("error:", ref)
		return
	}
	rs := &config.RunState{
		BaseSHA:    strings.Repeat("b", 40),
		Trusted:    true,
		CapturedAt: core.NowUTC(exampleNow),
		Evidence:   config.Evidence{SchemaVersion: "1", BaseSHA: strings.Repeat("b", 40), Plan: config.Plan{Status: config.PendingStatus}},
		Resolved:   &config.Resolved{Plan: config.Plan{Status: config.PendingStatus}},
		Inventory:  config.InventoryHashes(snap),
	}
	if serr := config.WriteRunState(".verdict/state/local-demo", rs); serr != nil {
		fmt.Println("error:", serr)
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

// ExampleClient_Post renders the dry-run payload without submitting it.
func ExampleClient_Post() {
	defer chdirTemp()()
	writeFile(".verdict/state/7/PR.json",
		`{"state":"OPEN","headRefOid":"`+strings.Repeat("a", 40)+`","baseRefOid":"`+strings.Repeat("b", 40)+`",`+
			`"author":{"login":"teammate"}}`)
	writeFile(".verdict/state/7/full.diff",
		"diff --git a/notes.txt b/notes.txt\nindex 1111111..2222222 100644\n"+
			"--- a/notes.txt\n+++ b/notes.txt\n@@ -1,3 +1,3 @@\n top\n-old line\n+new line\n bottom\n")
	writeFile(".verdict/state/7/review.json",
		`{"schema_version":"1","body":"> ✅ Looks good — minor note below.","approved":false,`+
			`"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"Consider clarifying this line.",`+
			`"rigor":"pointed","severity":"informational"}]}`)
	seedExampleControls("7")
	cmdr := &scriptCommander{res: verdict.CommandResult{Stdout: strings.Repeat("a", 40) + "\n"}}
	c := verdict.New(verdict.Options{
		Now:       func() time.Time { return exampleNow },
		Commander: cmdr,
	})
	defer c.Close()
	inv := verdict.Invocation{Repo: "acme/app", PR: "7"}
	resp, err := c.Post(context.Background(), &verdict.PostRequest{
		Invocation: inv,
		ReviewFile: ".verdict/state/7/review.json",
		Approve:    "false",
		DryRun:     true,
	})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(resp.At)
	fmt.Println(resp.Next)
	// Output:
	// 2026-11-02T12:00:00Z
	// payload NOT posted (dry-run)
}

// ExampleClient_Fetch pulls a pull request into .verdict/state/7 through the
// scripted commander seam.
func ExampleClient_Fetch() {
	defer chdirTemp()()
	defer stubExampleTools()()
	c := verdict.New(verdict.Options{
		Now:       func() time.Time { return exampleNow },
		Commander: exampleFetchCommander(),
	})
	defer c.Close()
	inv := verdict.Invocation{Repo: "acme/app", PR: "7"}
	resp, err := c.Fetch(context.Background(), &verdict.FetchRequest{
		Invocation:    inv,
		BudgetConsent: true,
		Ignore:        []string{"!**", "!notes.txt"}, NoDefaultExcludes: true,
	})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(resp.At)
	fmt.Println(resp.Next)
	// Output:
	// 2026-11-02T12:00:00Z
	// feed .verdict/state/7/procedure.md + .verdict/state/7/PR.json + full.diff to any agent; ask it to write .verdict/state/7/review.json
}

// ExampleClient_Gate composes the CI gate over the bundle's findings.
func ExampleClient_Gate() {
	defer chdirTemp()()
	reportBundle()
	c := verdict.New(verdict.Options{Now: func() time.Time { return exampleNow }})
	defer c.Close()
	inv := verdict.Invocation{Repo: "acme/app", PR: "5101"}
	gate, err := c.Gate(context.Background(), &verdict.GateRequest{
		Invocation: inv, MaxSeverity: "important", Live: "skip", Coverage: "warn",
	})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("passed:", gate.Passed)
	for _, dim := range gate.Dimensions {
		fmt.Printf("%s %s\n", dim.ID, dim.Status)
	}
	// Output:
	// passed: false
	// findings fail
	// coverage skip
	// live_threads skip
	// spec pass
	// evidence unknown
}

// ExampleClient_Fix runs one bounded pass over the top open finding.
func ExampleClient_Fix() {
	defer chdirTemp()()
	writeFile("notes.txt", "line1\nline2\nline3\n")
	writeFile("app.txt", "alpha\nbeta\ngamma\n")
	writeFile("other.txt", "other1\nother2\n")
	gitExample("init", "-q")
	gitExample("add", "-A")
	gitExample("commit", "-qm", "seed")
	writeFile(".verdict/state/local-pass/findings.json", "["+
		fixFinding("1123456789abcdef0123456789abcdef01234567", "informational")+","+
		fixFinding("c123456789abcdef0123456789abcdef01234567", "critical")+"]")
	c := verdict.New(verdict.Options{Now: func() time.Time { return exampleNow }})
	defer c.Close()
	inv := verdict.Invocation{Repo: "acme/app", PR: "local-pass"}
	out, err := c.Fix(context.Background(), &verdict.FixRequest{Invocation: inv})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(out.At)
	fmt.Println(out.Next)
	// Output:
	// 2026-11-02T12:00:00Z
	// patch the anchor file, write verification.json, run record to land it, then re-run fix
}

// fixFinding renders one inline findings entry for the fix example.
func fixFinding(sig, severity string) string {
	return `{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,` +
		`"anchored":true,"body":"Usable body here with enough substance.","suggestion":null,` +
		`"inBounds":true,"rigor":"pointed","severity":"` + severity + `",` +
		`"sig":"` + sig + `","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa",` +
		`"status":"open","rationale":"","history":[]}`
}

// gitExample runs one git command in the temp checkout with a pinned identity.
func gitExample(args ...string) {
	cmd := exec.Command("git", args...)
	cmd.Env = testenv.Base(map[string]string{
		"GIT_AUTHOR_NAME":     "example",
		"GIT_AUTHOR_EMAIL":    "example@test",
		"GIT_COMMITTER_NAME":  "example",
		"GIT_COMMITTER_EMAIL": "example@test",
	})
	if out, err := cmd.CombinedOutput(); err != nil {
		panic(fmt.Sprintf("git %v: %v\n%s", args, err, out))
	}
}

// stubExampleTools puts do-nothing gh/git executables on PATH so the fetch
// pre-check resolves and every spawn still goes to the scripted commander.
func stubExampleTools() func() {
	dir, err := os.MkdirTemp("", "verdict-stubs-")
	if err != nil {
		panic(err)
	}
	name, body := "%s", "#!/bin/sh\nexit 1\n"
	if runtime.GOOS == "windows" {
		name, body = "%s.bat", "@exit /b 1\r\n"
	}
	for _, tool := range []string{"gh", "git"} {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf(name, tool)), []byte(body), 0o755); err != nil {
			panic(err)
		}
	}
	old := os.Getenv("PATH")
	if err := os.Setenv("PATH", dir+string(os.PathListSeparator)+old); err != nil {
		panic(err)
	}
	return func() {
		os.Setenv("PATH", old)
		os.RemoveAll(dir)
	}
}

// exampleRule answers one spawned command whose argv contains every token.
type exampleRule struct {
	tokens []string
	result verdict.CommandResult
}

// exampleCommander scripts the public Commander seam without a *testing.T.
type exampleCommander struct {
	rules []exampleRule
	got   [][]string
}

// Run implements verdict.Commander: the first rule matching every token
// answers, and an unmatched spawn panics.
func (e *exampleCommander) Run(_ context.Context, cmd *verdict.Command) (verdict.CommandResult, error) {
	e.got = append(e.got, cmd.Argv)
	joined := strings.Join(cmd.Argv, " ")
	for _, rule := range e.rules {
		matched := true
		for _, token := range rule.tokens {
			if !strings.Contains(joined, token) {
				matched = false
				break
			}
		}
		if matched {
			return rule.result, nil
		}
	}
	panic("unexpected command: " + joined)
}

// exampleFetchCommander scripts the remote fetch reads: the PR, its diff,
// the file list, reviews/comments, and the GraphQL threads.
func exampleFetchCommander() *exampleCommander {
	head := strings.Repeat("a", 40)
	base := strings.Repeat("b", 40)
	prJSON := `{"number":7,"title":"Add notes","body":"Body text.","baseRefName":"main",` +
		`"headRefName":"work","headRefOid":"` + head + `","baseRefOid":"` + base + `",` +
		`"author":{"login":"teammate"},"url":"https://github.com/acme/app/pull/7","commits":[],` +
		`"files":[{"path":"notes.txt","additions":1,"deletions":1}],` +
		`"additions":1,"deletions":1,"isDraft":false,"reviewDecision":"","state":"OPEN"}`
	threads := `{"data":{"repository":{"pullRequest":{"reviewThreads":` +
		`{"nodes":[],"pageInfo":{"hasNextPage":false,"endCursor":""}}}}}}`
	return &exampleCommander{rules: []exampleRule{
		{tokens: []string{"git remote get-url origin"}, result: verdict.CommandResult{Stdout: "https://github.com/acme/app.git"}},
		{tokens: []string{"gh pr view", "--jq"}, result: verdict.CommandResult{Stdout: "notes.txt (+1/-1)\n"}},
		{tokens: []string{"gh pr view"}, result: verdict.CommandResult{Stdout: prJSON}},
		{tokens: []string{"gh pr diff"}, result: verdict.CommandResult{Stdout: "diff --git a/notes.txt b/notes.txt\nindex 1111111..2222222 100644\n--- a/notes.txt\n+++ b/notes.txt\n@@ -1,3 +1,3 @@\n top\n-old line\n+new line\n bottom\n"}},
		{tokens: []string{"git", "patch-id"}, result: verdict.CommandResult{Stdout: "9f3c2a1b7d9f3c2a1b7d9f3c2a1b7d9f3c2a1b7d 0000000000000000000000000000000000000000\n"}},
		{tokens: []string{"pulls/7/files"}, result: verdict.CommandResult{
			Stdout: `[{"filename":"notes.txt","status":"modified","additions":1,"deletions":1,"changes":2}]`,
		}},
		{tokens: []string{"pulls/7/reviews"}, result: verdict.CommandResult{Stdout: `[]`}},
		{tokens: []string{"pulls/7/comments"}, result: verdict.CommandResult{Stdout: `[]`}},
		{tokens: []string{"graphql"}, result: verdict.CommandResult{Stdout: threads}},
		{tokens: []string{"git", "ls-tree"}, result: verdict.CommandResult{}},
	}}
}

// seedExampleControls snapshots the review controls and writes the run bundle
// the post binding requires.
func seedExampleControls(pr string) {
	dir := ".verdict/state/" + pr
	base := strings.Repeat("b", 40)
	snap, ref := controls.Resolve(context.Background(), nil, &controls.Request{
		BundleDir: dir, BaseSHA: base, AllowHead: true,
	})
	if ref != nil {
		panic(ref)
	}
	rs := &config.RunState{
		BaseSHA:    base,
		Trusted:    true,
		CapturedAt: core.NowUTC(exampleNow),
		Evidence:   config.Evidence{SchemaVersion: "1", BaseSHA: base, Plan: config.Plan{Status: config.PendingStatus}},
		Resolved:   &config.Resolved{Plan: config.Plan{Status: config.PendingStatus}},
		Inventory:  config.InventoryHashes(snap),
	}
	if serr := config.WriteRunState(dir, rs); serr != nil {
		panic(serr)
	}
}

// scriptCommander is the injected command seam: every spawn is scripted.
type scriptCommander struct {
	got []*verdict.Command
	res verdict.CommandResult
}

// Run implements verdict.Commander.
func (s *scriptCommander) Run(_ context.Context, cmd *verdict.Command) (verdict.CommandResult, error) {
	s.got = append(s.got, cmd)
	return s.res, nil
}

// TestClientVerifyFakeCommander proves the Commander seam: a scripted runner
// serves the watchdog spawn and the typed outcome maps the nonzero exit and
// combined stderr through, instead of echoing zero values.
func TestClientVerifyFakeCommander(t *testing.T) {
	t.Chdir(t.TempDir())
	writeFile(".verdict/state/5201/patch.json", `{"verifyCmd":"printf ok"}`)
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
