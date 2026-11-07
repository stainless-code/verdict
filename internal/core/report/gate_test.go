package reportcore_test

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	reportcore "github.com/stainless-code/verdict/internal/core/report"
)

const storeHeadSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

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
			name: "critical", findings: severityTiers, check: "critical", wantFail: true,
			wantLine: "check: 1 open finding(s) at-or-above critical — gate fails",
		},
		{
			name: "important", findings: severityTiers, check: "important", wantFail: true,
			wantLine: "check: 3 open finding(s) at-or-above important — gate fails",
		},
		{
			name: "informational", findings: severityTiers, check: "informational", wantFail: true,
			wantLine: "check: 4 open finding(s) at-or-above informational — gate fails",
		},
		{
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
			name: "missing-post-fails", wantFail: true,
			wantSubs: []string{"missing-post", "verdict fetch acme/app 5101"},
		},
		{
			name: "headless-post-fails", post: `{"state":"POSTED"}`, postSet: true, wantFail: true,
			wantSubs: []string{"corrupt-post"},
		},
		{
			name: "stale-post-fails", post: `{"head":"` + strings.Repeat("c", 40) + `"}`, postSet: true,
			wantPosted: strings.Repeat("c", 40), wantFail: true,
			wantSubs: []string{"stale-head", "HEAD aaaaaaa != posted ccccccc", "--base-sha ccccccc"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := fixture(t, "[]")
			if tc.postSet {
				writeFile(t, ".verdict/state/5101/post.json", tc.post)
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

type threadCommander struct {
	stdout string
	stderr string
	fail   bool
	calls  int
}

func (c *threadCommander) Run(_ context.Context, _ *core.Command) (core.Result, error) {
	c.calls++
	res := core.Result{Stdout: c.stdout, Stderr: c.stderr}
	if c.fail {
		res.ExitCode = 1
	}
	return res, nil
}

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
			name: "equal-count-passes", findings: oneOpen, gate: true,
			page: threadsPage(1, 1), wantCalls: 1, wantFail: false,
			wantLines: []string{"check: 1 live open thread(s) vs 1 local open finding(s) — live recheck passes"},
		},
		{
			name: "resolved-threads-ignored", findings: oneOpen, gate: true,
			page: threadsPage(0, 3), wantCalls: 1, wantFail: false,
			wantLines: []string{"check: 0 live open thread(s) vs 1 local open finding(s) — live recheck passes"},
			noWarns:   []string{"malformed thread node"},
		},
		{
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
			name: "skip-explicit-neutral", findings: oneOpen, live: "skip", gate: true,
			page: threadsPage(9, 0), wantCalls: 0, wantFail: false,
			wantLines: []string{"check: live recheck skipped (--live skip) — gating on local dimensions only"},
			wantWarns: []string{"live recheck skipped (--live skip requested — gating on local dimensions only)"},
		},
		{
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
				writeFile(t, ".verdict/state/5101/post.json", `{"head":"`+storeHeadSHA+`"}`)
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
	writeFile(t, ".verdict/state/local-gate/source.json",
		`{"head_sha":"`+storeHeadSHA+`","base_sha":"`+strings.Repeat("b", 40)+`"}`)
	writeFile(t, ".verdict/state/local-gate/findings.json", "["+
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
		if got, want := resp.Data.Counts, wantUntaggedCounts(5, 2, 2, 3, 7); !reflect.DeepEqual(got, want) {
			t.Errorf("counts = %+v, want %+v", got, want)
		}
		if got, want := resp.Data.Verdict, (core.ReportVerdict{Open: 3, Deferred: 1, Addressed: 1, Terminal: 2}); got != want {
			t.Errorf("verdict = %+v, want %+v", got, want)
		}
		if len(resp.Data.Items) != 7 {
			t.Errorf("items = %d, want the whole store shown", len(resp.Data.Items))
		}
	})

	t.Run("filtered", func(t *testing.T) {
		req := fixture(t, findings)
		resp, ref := reportcore.Run(context.Background(), &core.ReportRequest{
			ReadRequest: req,
			Flags:       core.ReportFlags{Status: "open"},
		})
		if ref != nil {
			t.Fatalf("Run refusal: %v", ref)
		}
		if got, want := resp.Data.Counts, wantUntaggedCounts(5, 2, 2, 3, 7); !reflect.DeepEqual(got, want) {
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
	writeFile(t, ".verdict/state/5101/PR.json",
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

// TestRunReportGateData pins the typed result report --json publishes as
// data.gate: one dimension per requested flag in wire order, a skip neutral, a
// failed or unknown dimension failing the block, and no gate block at all on
// an advisory run.
//
//nolint:cyclop,gocognit,revive // each leg asserts the dimensions, the status, and the count inline
func TestRunReportGateData(t *testing.T) {
	tests := []struct {
		name      string
		findings  string
		post      string
		live      string
		check     string
		wantIDs   []string
		wantStat  []string
		wantCount []int
		complete  bool
		wantFail  bool
	}{
		{
			name: "tier-only-pass", findings: "[]", live: "skip", check: "critical",
			wantIDs: []string{"findings", "live_threads"}, wantStat: []string{"pass", "skip"},
			wantCount: []int{0, -1}, wantFail: false,
		},
		{
			name:     "tier-only-fail",
			findings: "[" + entryJSON("1111111111111111111111111111111111111111", "inline", "open", `"critical"`, "true") + "]",
			live:     "skip", check: "critical",
			wantIDs: []string{"findings", "live_threads"}, wantStat: []string{"fail", "skip"},
			wantCount: []int{1, -1}, wantFail: true,
		},
		{
			name: "head-only-missing", findings: "[]", live: "skip", complete: true,
			wantIDs: []string{"head", "live_threads"}, wantStat: []string{"unknown", "skip"},
			wantCount: []int{-1, -1}, wantFail: true,
		},
		{
			name: "head-stale", findings: "[]", post: `{"head":"` + strings.Repeat("c", 40) + `"}`,
			live: "skip", complete: true,
			wantIDs: []string{"head", "live_threads"}, wantStat: []string{"fail", "skip"},
			wantCount: []int{-1, -1}, wantFail: true,
		},
		{
			name: "head-pass", findings: "[]", post: `{"head":"` + storeHeadSHA + `"}`,
			live: "skip", complete: true,
			wantIDs: []string{"head", "live_threads"}, wantStat: []string{"pass", "skip"},
			wantCount: []int{-1, -1}, wantFail: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := fixture(t, tc.findings)
			if tc.post != "" {
				writeFile(t, ".verdict/state/5101/post.json", tc.post)
			}
			flags := core.ReportFlags{Check: tc.check, Completeness: tc.complete, Live: tc.live}
			resp, ref := reportcore.Run(context.Background(), &core.ReportRequest{ReadRequest: req, Flags: flags})
			if ref != nil {
				t.Fatalf("Run refusal: %v", ref)
			}
			gate := resp.Data.Gate
			if gate == nil {
				t.Fatal("data.gate is nil on a gate run")
			}
			if gate.Passed != !tc.wantFail {
				t.Errorf("passed = %v, want %v", gate.Passed, !tc.wantFail)
			}
			if gate.Passed == resp.GateFailed {
				t.Errorf("passed = %v and GateFailed = %v disagree", gate.Passed, resp.GateFailed)
			}
			if len(gate.Dimensions) != len(tc.wantIDs) {
				t.Fatalf("dimensions = %+v, want %d", gate.Dimensions, len(tc.wantIDs))
			}
			for i, dim := range gate.Dimensions {
				if dim.ID != tc.wantIDs[i] || dim.Status != tc.wantStat[i] {
					t.Errorf("dimension %d = %s/%s, want %s/%s", i, dim.ID, dim.Status, tc.wantIDs[i], tc.wantStat[i])
				}
				wantCount := tc.wantCount[i]
				switch {
				case wantCount < 0 && dim.Count != nil:
					t.Errorf("dimension %d count = %d, want null", i, *dim.Count)
				case wantCount >= 0 && (dim.Count == nil || *dim.Count != wantCount):
					t.Errorf("dimension %d count = %v, want %d", i, dim.Count, wantCount)
				}
				if dim.Detail == "" {
					t.Errorf("dimension %d carries no detail", i)
				}
			}
		})
	}

	t.Run("advisory-run-carries-no-gate-block", func(t *testing.T) {
		req := fixture(t, "[]")
		resp, ref := reportcore.Run(context.Background(), &core.ReportRequest{ReadRequest: req})
		if ref != nil {
			t.Fatalf("Run refusal: %v", ref)
		}
		if resp.Data.Gate != nil {
			t.Errorf("data.gate = %+v on an advisory run, want none", resp.Data.Gate)
		}
	})
}

// wantUntaggedCounts builds the expected whole-store counts for a fixture
// whose entries carry no category: every published key zero, untagged holding
// the whole store.
func wantUntaggedCounts(inline, section, anchored, unanchored, untagged int) core.ReportCounts {
	return core.ReportCounts{Inline: inline, Section: section, Anchored: anchored, Unanchored: unanchored, ByCategory: map[string]int{
		"logic": 0, "edge-case": 0, "security": 0, "performance": 0,
		"accidental-commit": 0, "style": 0, "docs": 0, "other": 0, "untagged": untagged,
	}}
}
