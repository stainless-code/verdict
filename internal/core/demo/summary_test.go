package democore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	fetchcore "github.com/stainless-code/verdict/internal/core/fetch"
	"github.com/stainless-code/verdict/internal/testkit"
)

// summaryDiff is a diff over files changed files, one hunk each.
func summaryDiff(files int) string {
	var b strings.Builder
	for i := range files {
		name := "file" + string(rune('a'+i)) + ".txt"
		b.WriteString("diff --git a/" + name + " b/" + name + "\n")
		b.WriteString("index 1111111..2222222 100644\n")
		b.WriteString("--- a/" + name + "\n+++ b/" + name + "\n@@ -1,3 +1,3 @@\n")
		b.WriteString(" top\n-old line\n+new line\n bottom\n")
	}
	return b.String()
}

// summaryBundle writes a bundle directory carrying a diff over files files.
func summaryBundle(t *testing.T, files int) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "full.diff")
	if err := os.WriteFile(path, []byte(summaryDiff(files)), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestTopologyBlockFollowsTheKnob proves the block is the knob's: off by
// operator choice, it is empty, and the human render adds nothing.
func TestTopologyBlockFollowsTheKnob(t *testing.T) {
	t.Parallel()
	dir := summaryBundle(t, 1)
	off := &Request{Effective: core.Defaults()}
	off.Effective.PublishOverview = core.FlagOff
	if got := topologyBlock(off, dir, "7"); got != "" {
		t.Errorf("topologyBlock with the knob off = %q, want empty", got)
	}
	if topologyAppended(off, dir, "7") {
		t.Error("topologyAppended with the knob off = true, want false")
	}
}

// TestTopologyAppendedBelowTheGate proves the top-up: record draws no block
// below its size gate, so the demo adds one for a two-file change — the exact
// case the acceptance criterion ("shows the computed topology") is about.
func TestTopologyAppendedBelowTheGate(t *testing.T) {
	t.Parallel()
	dir := summaryBundle(t, 1)
	req := &Request{Effective: core.Defaults()}
	block := topologyBlock(req, dir, "7")
	if !strings.Contains(block, "Change topology") {
		t.Fatalf("topologyBlock = %q, want a topology block", block)
	}
	if !topologyAppended(req, dir, "7") {
		t.Error("topologyAppended below the gate = false, want true")
	}
}

// TestTopologyNotDuplicatedAboveTheGate proves the other side: over the gate
// record's body already carries the block, so the render must not add a second.
func TestTopologyNotDuplicatedAboveTheGate(t *testing.T) {
	t.Parallel()
	dir := summaryBundle(t, 9)
	req := &Request{Effective: core.Defaults()}
	if topologyBlock(req, dir, "7") == "" {
		t.Fatal("topologyBlock above the gate is empty, want the block")
	}
	if topologyAppended(req, dir, "7") {
		t.Error("topologyAppended above the gate = true, want false (the body already carries it)")
	}
}

// TestSummaryAndSteps proves the human facts line and the next steps: the line
// names the change, the commit pair, the agent, and the finders; the steps name
// the one-command upgrade, the other invocation form, and the explicit
// equivalent. Nothing claims a post.
func TestSummaryLine(t *testing.T) {
	t.Parallel()
	resp := &Response{Data: &Data{
		Agent:      AgentData{Name: "claude", Binary: "/usr/local/bin/claude"},
		Finders:    1,
		DurationMS: 1200,
	}}
	out := outcome{
		fetched: &fetchcore.Response{Data: &core.FetchData{
			Files: 3, Additions: int64(12), Deletions: int64(4),
			HeadSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			BaseSHA: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		}},
		ran: &core.RunResponse{Data: &core.RunData{Agent: "claude", Binary: "/usr/local/bin/claude"}},
	}
	if got, want := summaryLine(resp, out), "3 files · +12 −4 · bbbbbbb → aaaaaaa · agent claude (finders 1) · 1.2s"; got != want {
		t.Errorf("summaryLine = %q, want %q", got, want)
	}
	// One changed file reads as one file, and a sub-second run in ms.
	out.fetched.Data.Files, out.fetched.Data.Additions, out.fetched.Data.Deletions = 1, 1, 0
	resp.Data.DurationMS = 420
	if got, want := summaryLine(resp, out),
		"1 file · +1 −0 · bbbbbbb → aaaaaaa · agent claude (finders 1) · 420ms"; got != want {
		t.Errorf("summaryLine (singular) = %q, want %q", got, want)
	}
}

// TestSteps proves the human "next" block: the one-command upgrade path, the
// other invocation form, and the full explicit equivalent (fetch, run, record)
// for both forms. Nothing claims a post.
func TestSteps(t *testing.T) {
	t.Parallel()
	out := outcome{
		fetched: &fetchcore.Response{Data: &core.FetchData{}},
		ran:     &core.RunResponse{Data: &core.RunData{Agent: "claude"}},
	}
	remote := Steps(&bundle{repo: "acme/app", pr: "7", fetchedVia: fetchedViaGh}, out)
	assertStepLines(t, remote, 3)
	if want := "verdict fetch acme/app 7 && verdict run acme/app 7 --agent claude --finders 1 && verdict record acme/app 7 --dry-run"; remote[2] != want {
		t.Errorf("remote step 3 = %q, want %q", remote[2], want)
	}
	local := Steps(&bundle{repo: "acme/app", pr: localName, checkout: "/tmp/repo", fetchedVia: fetchedViaGit}, out)
	assertStepLines(t, local, 3)
	if !strings.Contains(local[2], "verdict fetch-local acme/app demo") {
		t.Errorf("local step 3 = %q, want the local equivalent", local[2])
	}
	if strings.Contains(local[2], "--range") {
		t.Errorf("local step 3 = %q, want no range for a working-tree review", local[2])
	}
	if !strings.HasSuffix(local[2], "&& verdict record acme/app demo") {
		t.Errorf("local step 3 = %q, want the local record stage (no dry run)", local[2])
	}
	// A clean tree reviews the last commit, and the equivalent names that range
	// so a re-run cannot silently review the working tree instead.
	ranged := Steps(&bundle{
		repo: "acme/app", pr: localName, checkout: "/tmp/repo",
		fetchedVia: fetchedViaGit, rangeSpec: "HEAD~1..HEAD",
	}, out)
	assertStepLines(t, ranged, 3)
	if !strings.Contains(ranged[2], "verdict fetch-local acme/app demo --range 'HEAD~1..HEAD' &&") {
		t.Errorf("ranged step 3 = %q, want the --range carried onto fetch-local", ranged[2])
	}
	// An anonymous read cannot re-run `verdict fetch` unchanged: the note names
	// the login the fetch step needs.
	anon := Steps(&bundle{repo: "acme/app", pr: "7", fetchedVia: fetchedViaPublicAPI}, out)
	assertStepLines(t, anon, 4)
	if !strings.Contains(anon[3], "gh auth login") {
		t.Errorf("anonymous note = %q, want it to name the login prerequisite", anon[3])
	}
}

// TestExplicitRunQuotesTheArguments proves the printed equivalent survives a
// paste: a repository slug or agent name with a space stays one word.
func TestExplicitRunQuotesTheArguments(t *testing.T) {
	t.Parallel()
	got := explicitRun(&bundle{repo: "acme/my app", pr: localName, checkout: "/tmp/repo", fetchedVia: fetchedViaGit}, "my agent")
	want := "verdict fetch-local 'acme/my app' demo && verdict run 'acme/my app' demo --agent 'my agent' --finders 1 && verdict record 'acme/my app' demo"
	if got != want {
		t.Errorf("explicitRun = %q, want %q", got, want)
	}
}

// TestHeaderNamesTheRange proves the local header states what was read: a clean
// tree reads the last commit, so calling it the working tree would be wrong.
func TestHeaderNamesTheRange(t *testing.T) {
	t.Parallel()
	if got := header(&bundle{repo: "acme/app", pr: localName, fetchedVia: fetchedViaGit}); got !=
		"verdict demo · acme/app#demo · local review of your working tree" {
		t.Errorf("header (dirty) = %q", got)
	}
	ranged := header(&bundle{repo: "acme/app", pr: localName, fetchedVia: fetchedViaGit, rangeSpec: "HEAD~1..HEAD"})
	if want := "verdict demo · acme/app#demo · local review of HEAD~1..HEAD"; ranged != want {
		t.Errorf("header (ranged) = %q, want %q", ranged, want)
	}
}

// TestElapsedUsesTheInjectedClock proves duration_ms follows the injected
// clock, never the process clock, and never goes negative when that clock steps
// back.
func TestElapsedUsesTheInjectedClock(t *testing.T) {
	t.Parallel()
	base := testkit.FixedNow()
	frozen := &Request{}
	frozen.Runtime = &core.Runtime{Now: func() time.Time { return base }}
	if got := elapsed(frozen, base.Add(-1500*time.Millisecond)); got != 1500 {
		t.Errorf("elapsed = %d, want 1500 from the injected clock", got)
	}
	if got := elapsed(frozen, base.Add(time.Second)); got != 0 {
		t.Errorf("elapsed = %d, want 0 for a clock that steps back", got)
	}
}

// assertStepLines proves the shared shape of the next steps: the upgrade path
// first, the expected line count, and no promise of a post.
func assertStepLines(t *testing.T, steps []string, want int) {
	t.Helper()
	if len(steps) != want {
		t.Fatalf("Steps = %q, want %d lines", steps, want)
	}
	if steps[0] != "verdict init --ci github            # review every PR in your repo" {
		t.Errorf("step 1 = %q", steps[0])
	}
	for _, step := range steps {
		if strings.Contains(step, "post") {
			t.Errorf("a next step names post: %q", step)
		}
	}
}

// TestDedupeWarnings proves the ordered union: the same line from two composed
// services is one line, and the first occurrence keeps its place.
func TestDedupeWarnings(t *testing.T) {
	t.Parallel()
	got := dedupe([]string{"a", "b", "a", "", "c", "b"})
	want := []string{"a", "b", "c"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("dedupe = %q, want %q", got, want)
	}
}
