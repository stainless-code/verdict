package reportcore_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	reportcore "github.com/stainless-code/verdict/internal/core/report"
)

// The gate contract these tests assert is docs/manual.md § report --check:
// exit 0 for no open findings at-or-above the tier, 1 for findings at-or-above
// it (only `open` findings count, untagged findings never meet a tier), the
// completeness dimension (HEAD reviewed iff the completed post record carries
// the fetched HEAD, with typed missing-post/corrupt-post/stale-head lines),
// and the third live-thread dimension that reads neutral when skipped, fails
// when more threads stand open live than the store holds open, and fails
// closed only under --live strict.

// storeHeadSHA is the fetched HEAD the fixtures pin.
const storeHeadSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// entryJSON renders one findings.json entry carrying the fields the gate and
// the counts read. severity and anchored are JSON literals, so a case can pin
// an untagged finding (null) or a section entry ("body").
func entryJSON(sig, kind, status, severity, anchored string) string {
	path, line, heading := `"notes.txt"`, `3`, "null"
	if kind == "section" {
		path, line, heading = "null", "null", `"### Gate"`
	}
	return `{"kind":"` + kind + `","path":` + path + `,"side":"RIGHT","line":` + line +
		`,"anchored":` + anchored + `,"heading":` + heading +
		`,"body":"Gate fixture body.","suggestion":null,"kb":[],"inBounds":true,` +
		`"rigor":"walked","severity":` + severity + `,"sig":"` + sig + `","sig_version":"1",` +
		`"firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"` + status + `",` +
		`"rationale":"","history":[]}`
}

// severityTiers is the mixed store the tier gate counts: one open critical,
// two open important (a tie the tier must count twice), one open
// informational, and three decoys — an untagged open finding (never meets a
// tier), an addressed critical, and a deferred critical. Store order is not
// rank order, so a count that depends on position cannot pass.
var severityTiers = "[" + strings.Join([]string{
	entryJSON("1111111111111111111111111111111111111111", "inline", "open", `"informational"`, "true"),
	entryJSON("2222222222222222222222222222222222222222", "inline", "open", `"critical"`, "true"),
	entryJSON("3333333333333333333333333333333333333333", "inline", "open", `"important"`, "true"),
	entryJSON("4444444444444444444444444444444444444444", "inline", "addressed", `"critical"`, "true"),
	entryJSON("5555555555555555555555555555555555555555", "inline", "open", "null", "true"),
	entryJSON("6666666666666666666666666666666666666666", "inline", "deferred", `"critical"`, "true"),
	entryJSON("7777777777777777777777777777777777777777", "section", "open", `"important"`, `"body"`),
}, ",") + "]"

// TestRunReportCheckTierGate pins the tier dimension for all three tiers plus
// the passing leg. Each expected line names the exact count, so a gate that
// counts non-open findings, positional findings, or off-by-one cannot pass.
//
//nolint:revive // the tier matrix asserts the count, the line, and the exit inline
func TestRunReportCheckTierGate(t *testing.T) {
	tests := []struct {
		name     string
		findings string
		check    string
		wantLine string
		wantFail bool
	}{
		{
			// One open critical; the addressed and deferred criticals are
			// dispositioned, so they stay human and never count.
			name: "critical", findings: severityTiers, check: "critical", wantFail: true,
			wantLine: "check: 1 open finding(s) at-or-above critical — gate fails",
		},
		{
			// Both open important findings count, the tie included.
			name: "important", findings: severityTiers, check: "important", wantFail: true,
			wantLine: "check: 3 open finding(s) at-or-above important — gate fails",
		},
		{
			name: "informational", findings: severityTiers, check: "informational", wantFail: true,
			wantLine: "check: 4 open finding(s) at-or-above informational — gate fails",
		},
		{
			// An untagged open finding never meets any tier, and the only
			// tiered findings are dispositioned: the gate passes.
			name: "untagged-and-dispositioned-pass",
			findings: "[" + strings.Join([]string{
				entryJSON("8888888888888888888888888888888888888888", "inline", "open", "null", "true"),
				entryJSON("9999999999999999999999999999999999999999", "inline", "addressed", `"critical"`, "true"),
			}, ",") + "]",
			check: "critical", wantFail: false,
			wantLine: "check: no open findings at-or-above critical — gate passes",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := fixture(t, tc.findings)
			resp, ref := reportcore.Run(context.Background(), &core.ReportRequest{
				ReadRequest: req,
				Flags:       core.ReportFlags{Check: tc.check},
			})
			if ref != nil {
				t.Fatalf("Run refusal: %v", ref)
			}
			if resp.GateFailed != tc.wantFail {
				t.Errorf("GateFailed = %v, want %v", resp.GateFailed, tc.wantFail)
			}
			if len(resp.CheckLines) != 1 || resp.CheckLines[0] != tc.wantLine {
				t.Errorf("CheckLines = %q, want exactly %q", resp.CheckLines, tc.wantLine)
			}
		})
	}
}

// TestRunReportCompletenessGate pins the HEAD-reviewed dimension: a matching
// post passes, and a missing, head-less, or stale post fails with its typed
// line naming the re-fetch command. The combined case proves one run fails on
// either dimension.
//
//nolint:cyclop,gocognit,revive // each dimension asserts its typed check line inline
func TestRunReportCompletenessGate(t *testing.T) {
	tests := []struct {
		name       string
		post       string
		wantPosted string
		wantSubs   []string
		wantFail   bool
		postSet    bool
	}{
		{
			name: "matching-post-passes", post: `{"head":"` + storeHeadSHA + `"}`, postSet: true,
			wantPosted: storeHeadSHA, wantFail: false,
			wantSubs: []string{"check: HEAD aaaaaaa reviewed (post matches fetch) — completeness passes"},
		},
		{
			// Nothing posted yet: the recovery is a plain fetch (no posted
			// head to advance from).
			name: "missing-post-fails", wantFail: true,
			wantSubs: []string{"missing-post", "verdict fetch acme/app 5101"},
		},
		{
			name: "headless-post-fails", post: `{"state":"POSTED"}`, postSet: true, wantFail: true,
			wantSubs: []string{"corrupt-post"},
		},
		{
			// A post recorded at another SHA: the recovery advances from it.
			name: "stale-post-fails", post: `{"head":"` + strings.Repeat("c", 40) + `"}`, postSet: true,
			wantPosted: strings.Repeat("c", 40), wantFail: true,
			wantSubs: []string{"stale-head", "HEAD aaaaaaa != posted ccccccc", "--base-sha ccccccc"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := fixture(t, "[]")
			if tc.postSet {
				writeFile(t, ".review/5101/post.json", tc.post)
			}
			resp, ref := reportcore.Run(context.Background(), &core.ReportRequest{
				ReadRequest: req,
				Flags:       core.ReportFlags{Completeness: true},
			})
			if ref != nil {
				t.Fatalf("Run refusal: %v", ref)
			}
			if resp.GateFailed != tc.wantFail {
				t.Errorf("GateFailed = %v, want %v", resp.GateFailed, tc.wantFail)
			}
			joined := strings.Join(resp.CheckLines, "\n")
			for _, sub := range tc.wantSubs {
				if !strings.Contains(joined, sub) {
					t.Errorf("CheckLines = %q, want it to contain %q", joined, sub)
				}
			}
			// The envelope carries the same dimension: the fetched HEAD is
			// always present, the posted HEAD only when a post recorded one.
			comp := resp.Data.Completeness
			if comp == nil {
				t.Fatal("data.completeness is nil under --completeness")
			}
			if comp.Reviewed != !tc.wantFail {
				t.Errorf("data.completeness.reviewed = %v, want %v", comp.Reviewed, !tc.wantFail)
			}
			if comp.FetchedHead == nil || *comp.FetchedHead != storeHeadSHA {
				t.Errorf("data.completeness.fetched_head = %v, want %s", comp.FetchedHead, storeHeadSHA)
			}
			if tc.wantPosted == "" {
				if comp.PostedHead != nil {
					t.Errorf("data.completeness.posted_head = %v, want null", *comp.PostedHead)
				}
			} else if comp.PostedHead == nil || *comp.PostedHead != tc.wantPosted {
				t.Errorf("data.completeness.posted_head = %v, want %s", comp.PostedHead, tc.wantPosted)
			}
		})
	}

	// Combined dimensions: the tier passes and completeness fails, so the
	// gate fails and both lines render in dimension order.
	t.Run("combined-fails-on-either", func(t *testing.T) {
		req := fixture(t, "[]")
		resp, ref := reportcore.Run(context.Background(), &core.ReportRequest{
			ReadRequest: req,
			Flags:       core.ReportFlags{Check: "critical", Completeness: true},
		})
		if ref != nil {
			t.Fatalf("Run refusal: %v", ref)
		}
		if !resp.GateFailed {
			t.Error("GateFailed = false; a missing post must fail the combined gate")
		}
		if len(resp.CheckLines) != 2 {
			t.Fatalf("CheckLines = %q, want one line per dimension", resp.CheckLines)
		}
		if !strings.HasPrefix(resp.CheckLines[0], "check: no open findings at-or-above critical — gate passes") {
			t.Errorf("CheckLines[0] = %q, want the tier line first", resp.CheckLines[0])
		}
		if !strings.Contains(resp.CheckLines[1], "missing-post") {
			t.Errorf("CheckLines[1] = %q, want the completeness line second", resp.CheckLines[1])
		}
	})
}

// threadCommander is a hermetic Commander for the live probe: every argv
// gets the canned streams, and the call count proves the probe ran (or did
// not) without a real `gh`.
type threadCommander struct {
	stdout string
	stderr string
	fail   bool
	calls  int
}

// Run records the call and replays the canned result.
func (c *threadCommander) Run(_ context.Context, _ core.Command) (core.Result, error) {
	c.calls++
	res := core.Result{Stdout: c.stdout, Stderr: c.stderr}
	if c.fail {
		res.ExitCode = 1
	}
	return res, nil
}

// threadsPage renders a GraphQL thread page with open unresolved threads and
// resolved ones, the shape the probe's slurp reads.
func threadsPage(open, resolved int) string {
	nodes := make([]string, 0, open+resolved)
	for range open {
		nodes = append(nodes, `{"isResolved":false}`)
	}
	for range resolved {
		nodes = append(nodes, `{"isResolved":true}`)
	}
	return `{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[` +
		strings.Join(nodes, ",") + `]}}}}}`
}

// TestRunReportLiveGate pins the third gate dimension: more live open threads
// than local open findings fails the gate, an equal count passes (the
// boundary), an unreachable lookup reads neutral under --live auto and fails
// closed under --live strict, and the explicit skip plus the local-bundle
// structural skip stay neutral without touching the network. Advisory runs
// never probe at all.
//
//nolint:gocognit,cyclop,revive // the live matrix asserts calls, lines, warns, and the exit inline
func TestRunReportLiveGate(t *testing.T) {
	// oneOpen is a single open local finding, so the probe compares against 1.
	oneOpen := "[" + entryJSON("1111111111111111111111111111111111111111", "inline", "open", `"important"`, "true") + "]"

	tests := []struct {
		name      string
		findings  string
		live      string
		page      string
		wantLines []string
		wantWarns []string
		noWarns   []string
		wantCalls int
		gate      bool
		cmdFails  bool
		wantFail  bool
	}{
		{
			name: "more-live-than-local-fails", findings: oneOpen, gate: true,
			page: threadsPage(2, 1), wantCalls: 1, wantFail: true,
			wantLines: []string{"check: 2 live open thread(s) vs 1 local open finding(s) — gate fails " +
				"(fetch + triage them, then re-check)"},
		},
		{
			// One live open vs one local open: not more, so the dimension
			// passes. The tie is what the comparison boundary turns on.
			name: "equal-count-passes", findings: oneOpen, gate: true,
			page: threadsPage(1, 1), wantCalls: 1, wantFail: false,
			wantLines: []string{"check: 1 live open thread(s) vs 1 local open finding(s) — live recheck passes"},
		},
		{
			// Resolved threads never count as open.
			name: "resolved-threads-ignored", findings: oneOpen, gate: true,
			page: threadsPage(0, 3), wantCalls: 1, wantFail: false,
			wantLines: []string{"check: 0 live open thread(s) vs 1 local open finding(s) — live recheck passes"},
			noWarns:   []string{"malformed thread node"},
		},
		{
			// A thread whose resolution state cannot be read counts as open
			// (fail-closed) and says so, so the verdict is explainable.
			name: "malformed-thread-counts-open", findings: oneOpen, gate: true,
			page:      `{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[{"isResolved":"yes"}]}}}}}`,
			wantCalls: 1, wantFail: false,
			wantLines: []string{"check: 1 live open thread(s) vs 1 local open finding(s) — live recheck passes"},
			wantWarns: []string{"live thread recheck: 1 malformed thread node(s) counted as unresolved (fail-closed)"},
		},
		{
			name: "unreachable-auto-neutral", findings: oneOpen, gate: true,
			cmdFails: true, wantCalls: 1, wantFail: false,
			wantWarns: []string{"live thread recheck unreachable (boom — gating on local dimensions only)"},
			noWarns:   []string{"gate fails"},
		},
		{
			name: "unreachable-strict-fails-closed", findings: oneOpen, live: "strict", gate: true,
			cmdFails: true, wantCalls: 1, wantFail: true,
			wantLines: []string{"check: live thread recheck unreachable (boom — gate fails: offline posture is strict; " +
				"restore the network or re-run with --live skip, then re-check)"},
		},
		{
			// --live skip pays zero network and says so in both the warn
			// stream and a neutral check line.
			name: "skip-explicit-neutral", findings: oneOpen, live: "skip", gate: true,
			page: threadsPage(9, 0), wantCalls: 0, wantFail: false,
			wantLines: []string{"check: live recheck skipped (--live skip) — gating on local dimensions only"},
			wantWarns: []string{"live recheck skipped (--live skip requested — gating on local dimensions only)"},
		},
		{
			// An advisory run (no gate flag) never probes, even with a
			// commander available and --live auto.
			name: "advisory-never-probes", findings: oneOpen, gate: false,
			page: threadsPage(9, 0), wantCalls: 0, wantFail: false,
			noWarns: []string{"live recheck", "live thread recheck"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := fixture(t, tc.findings)
			cmd := &threadCommander{stdout: tc.page, stderr: "boom", fail: tc.cmdFails}
			req.Commander = cmd
			flags := core.ReportFlags{Live: tc.live}
			if tc.gate {
				// A matching post record passes completeness, and no tier
				// is set: this dimension alone moves GateFailed.
				writeFile(t, ".review/5101/post.json", `{"head":"`+storeHeadSHA+`"}`)
				flags.Completeness = true
			}
			resp, ref := reportcore.Run(context.Background(), &core.ReportRequest{ReadRequest: req, Flags: flags})
			if ref != nil {
				t.Fatalf("Run refusal: %v", ref)
			}
			if cmd.calls != tc.wantCalls {
				t.Errorf("commander calls = %d, want %d", cmd.calls, tc.wantCalls)
			}
			if resp.GateFailed != tc.wantFail {
				t.Errorf("GateFailed = %v, want %v (lines %q)", resp.GateFailed, tc.wantFail, resp.CheckLines)
			}
			for _, want := range tc.wantLines {
				if !containsLine(resp.CheckLines, want) {
					t.Errorf("CheckLines = %q, want it to contain %q", resp.CheckLines, want)
				}
			}
			for _, want := range tc.wantWarns {
				if !containsLine(resp.Warnings, want) {
					t.Errorf("Warnings = %q, want it to contain %q", resp.Warnings, want)
				}
			}
			for _, banned := range tc.noWarns {
				for _, w := range append(append([]string{}, resp.CheckLines...), resp.Warnings...) {
					if strings.Contains(w, banned) {
						t.Errorf("line %q must not mention %q", w, banned)
					}
				}
			}
			if !tc.gate {
				if len(resp.CheckLines) != 0 {
					t.Errorf("advisory run wrote check lines %q", resp.CheckLines)
				}
			}
		})
	}
}

// TestRunReportLocalBundleLiveSkip pins the structural skip: a local bundle
// has no live threads to verify, so every posture reads neutral with a warn
// and zero network.
func TestRunReportLocalBundleLiveSkip(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeFile(t, ".review/local-gate/source.json",
		`{"head_sha":"`+storeHeadSHA+`","base_sha":"`+strings.Repeat("b", 40)+`"}`)
	writeFile(t, ".review/local-gate/findings.json", "["+
		entryJSON("1111111111111111111111111111111111111111", "inline", "open", `"important"`, "true")+"]")
	cmd := &threadCommander{stdout: threadsPage(4, 0)}
	req := core.ReadRequest{
		Runtime: &core.Runtime{Now: func() time.Time { return fixedNow }, Commander: cmd},
		Repo:    "acme/app", Pr: "local-gate",
	}
	resp, ref := reportcore.Run(context.Background(), &core.ReportRequest{
		ReadRequest: req,
		Flags:       core.ReportFlags{Completeness: true},
	})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if cmd.calls != 0 {
		t.Errorf("commander calls = %d; a local bundle pays zero network", cmd.calls)
	}
	if !containsLine(resp.Warnings, "live recheck skipped: local bundle threads unreadable here (gating on local dimensions only)") {
		t.Errorf("Warnings = %q, want the local-bundle skip notice", resp.Warnings)
	}
	if containsLine(resp.CheckLines, "live open thread(s)") {
		t.Errorf("CheckLines = %q, want no live-thread verdict line", resp.CheckLines)
	}
}

// containsLine reports whether any line equals want or contains it (warns
// carry a detail suffix the test may not pin).
func containsLine(lines []string, want string) bool {
	for _, line := range lines {
		if strings.Contains(line, want) {
			return true
		}
	}
	return false
}

// TestRunReportCounts pins the counts the envelope publishes: kind and
// anchoring tallies run over the whole store, while the verdict tallies run
// over the displayed (filtered) ranking. Every tally below has a distinct
// value, so an off-by-one, a swapped pair, or a store-wide/displayed mix-up
// cannot pass.
//
//nolint:gocognit,revive // both tallies assert inline so the store-wide/filtered split is visible
func TestRunReportCounts(t *testing.T) {
	// Five inline findings (anchored, unanchored, addressed, deferred, and an
	// absent anchored flag) and two sections (wont-fix, false-positive).
	findings := "[" + strings.Join([]string{
		entryJSON("1111111111111111111111111111111111111111", "inline", "open", `"critical"`, "true"),
		entryJSON("2222222222222222222222222222222222222222", "inline", "open", `"important"`, "false"),
		entryJSON("3333333333333333333333333333333333333333", "inline", "addressed", `"important"`, "true"),
		entryJSON("4444444444444444444444444444444444444444", "inline", "deferred", "null", "false"),
		entryJSON("5555555555555555555555555555555555555555", "inline", "open", "null", "null"),
		entryJSON("6666666666666666666666666666666666666666", "section", "wont-fix", "null", `"body"`),
		entryJSON("7777777777777777777777777777777777777777", "section", "false-positive", "null", `"body"`),
	}, ",") + "]"

	t.Run("whole-store", func(t *testing.T) {
		req := fixture(t, findings)
		resp, ref := reportcore.Run(context.Background(), &core.ReportRequest{ReadRequest: req})
		if ref != nil {
			t.Fatalf("Run refusal: %v", ref)
		}
		if got, want := resp.Data.Counts, (core.ReportCounts{Inline: 5, Section: 2, Anchored: 2, Unanchored: 3}); got != want {
			t.Errorf("counts = %+v, want %+v", got, want)
		}
		if got, want := resp.Data.Verdict, (core.ReportVerdict{Open: 3, Deferred: 1, Addressed: 1, Terminal: 2}); got != want {
			t.Errorf("verdict = %+v, want %+v", got, want)
		}
		if len(resp.Data.Items) != 7 {
			t.Errorf("items = %d, want the whole store shown", len(resp.Data.Items))
		}
	})

	// A status filter narrows the displayed ranking — and so the verdict —
	// but never the store-wide kind counts.
	t.Run("filtered", func(t *testing.T) {
		req := fixture(t, findings)
		resp, ref := reportcore.Run(context.Background(), &core.ReportRequest{
			ReadRequest: req,
			Flags:       core.ReportFlags{Status: "open"},
		})
		if ref != nil {
			t.Fatalf("Run refusal: %v", ref)
		}
		if got, want := resp.Data.Counts, (core.ReportCounts{Inline: 5, Section: 2, Anchored: 2, Unanchored: 3}); got != want {
			t.Errorf("counts under a filter = %+v, want the whole store %+v", got, want)
		}
		if got, want := resp.Data.Verdict, (core.ReportVerdict{Open: 3}); got != want {
			t.Errorf("verdict under a filter = %+v, want %+v", got, want)
		}
		if len(resp.Data.Items) != 3 {
			t.Errorf("items = %d, want the 3 open findings", len(resp.Data.Items))
		}
	})
}

// TestRunReportEnvelopeDefaults pins the envelope's empty-shaped defaults on
// a clean, panel-less bundle: no output artifact, no panel source, an empty
// (never null) panel member list, no completeness block, and the "nothing to
// address" next step in both the envelope and the human tail. Reporting
// "wrote" or "address open threads" on a clean run misdirects the operator.
//
//nolint:revive // the defaults assert inline
func TestRunReportEnvelopeDefaults(t *testing.T) {
	req := fixture(t, "[]")
	resp, ref := reportcore.Run(context.Background(), &core.ReportRequest{ReadRequest: req})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if resp.Next != "none open — nothing to address" {
		t.Errorf("next = %q, want the nothing-to-address step", resp.Next)
	}
	if got := resp.Human[len(resp.Human)-1]; got != "next: none open — nothing to address" {
		t.Errorf("last human line = %q, want the next line", got)
	}
	if resp.Data.Output != nil {
		t.Errorf("data.output = %v, want null without --output", *resp.Data.Output)
	}
	if resp.Data.Panel.Source != nil {
		t.Errorf("data.panel.source = %v, want null with no panel file", *resp.Data.Panel.Source)
	}
	if resp.Data.Panel.Members == nil {
		t.Error("data.panel.members = null, want []")
	}
	if len(resp.Data.Panel.Members) != 0 {
		t.Errorf("data.panel.members = %v, want none", resp.Data.Panel.Members)
	}
	if resp.Data.Completeness != nil {
		t.Error("data.completeness is set without --completeness")
	}
	if resp.CheckLines != nil {
		t.Errorf("check lines = %q on an advisory run", resp.CheckLines)
	}
}

// TestRunReportLiveProviderSkip pins the non-GitHub structural skip: a bundle
// fetched from another forge has threads this verb cannot read, so the live
// dimension warns neutral without a single spawn, and the completeness
// recovery command names that provider instead of the GitHub default.
func TestRunReportLiveProviderSkip(t *testing.T) {
	req := fixture(t, "["+entryJSON("1111111111111111111111111111111111111111", "inline", "open", `"important"`, "true")+"]")
	writeFile(t, ".review/5101/PR.json",
		`{"state":"OPEN","provider":"gitlab","headRefOid":"`+storeHeadSHA+`"}`)
	cmd := &threadCommander{stdout: threadsPage(9, 0)}
	req.Commander = cmd
	resp, ref := reportcore.Run(context.Background(), &core.ReportRequest{
		ReadRequest: req,
		Flags:       core.ReportFlags{Completeness: true},
	})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if cmd.calls != 0 {
		t.Errorf("commander calls = %d; a non-GitHub bundle pays zero network", cmd.calls)
	}
	if !containsLine(resp.Warnings, "live recheck skipped: gitlab threads unreadable here (gating on local dimensions only)") {
		t.Errorf("Warnings = %q, want the provider skip notice", resp.Warnings)
	}
	if !containsLine(resp.CheckLines, "verdict fetch acme/app 5101 --provider gitlab") {
		t.Errorf("CheckLines = %q, want the provider-aware re-fetch command", resp.CheckLines)
	}
}
