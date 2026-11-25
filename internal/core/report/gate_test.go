package reportcore_test

import (
	"context"
	"os"
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
// the passing leg. Each expected detail names the exact count, so a gate that
// counts non-open findings, positional findings, or off-by-one cannot pass.
//
//nolint:revive // the tier matrix asserts the count, the detail, and the exit inline
func TestRunReportCheckTierGate(t *testing.T) {
	tests := []struct {
		name     string
		findings string
		check    string
		want     wantDim
		wantFail bool
	}{
		{
			name: "critical", findings: severityTiers, check: "critical", wantFail: true,
			want: wantDim{"findings", "fail", "1 open finding(s) at-or-above critical"},
		},
		{
			name: "important", findings: severityTiers, check: "important", wantFail: true,
			want: wantDim{"findings", "fail", "3 open finding(s) at-or-above important"},
		},
		{
			name: "informational", findings: severityTiers, check: "informational", wantFail: true,
			want: wantDim{"findings", "fail", "4 open finding(s) at-or-above informational"},
		},
		{
			name: "untagged-and-dispositioned-pass",
			findings: "[" + strings.Join([]string{
				entryJSON("8888888888888888888888888888888888888888", "inline", "open", "null", "true"),
				entryJSON("9999999999999999999999999999999999999999", "inline", "addressed", `"critical"`, "true"),
			}, ",") + "]",
			check: "critical", wantFail: false,
			want: wantDim{"findings", "pass", "no open findings at-or-above critical"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := fixture(t, tc.findings)
			resp, ref := reportcore.Run(context.Background(), &reportcore.Request{
				ReadRequest: req,
				Flags:       reportcore.Flags{Check: tc.check},
			})
			if ref != nil {
				t.Fatalf("Run refusal: %v", ref)
			}
			if resp.GateFailed != tc.wantFail {
				t.Errorf("GateFailed = %v, want %v", resp.GateFailed, tc.wantFail)
			}
			if n := len(resp.Gate.Dimensions); n != 4 || resp.Gate.Dimensions[0].ID != "findings" {
				t.Errorf("dimensions = %s, want the tier dimension, the live probe, spec and evidence", dimSummary(resp))
			}
			requireDims(t, resp, tc.want)
		})
	}
}

// TestRunReportGateRunKeepsTheAdvisoryNext proves a run that composes the gate
// leaves report's own next step alone: the merge hint belongs to the gate verb,
// which words it from its dimensions.
func TestRunReportGateRunKeepsTheAdvisoryNext(t *testing.T) {
	below := "[" + entryJSON("1111111111111111111111111111111111111111", "inline", "open", `"informational"`, "true") + "]"
	atFloor := "[" + entryJSON("2222222222222222222222222222222222222222", "inline", "open", `"important"`, "true") + "]"
	tests := []struct {
		name       string
		findings   string
		wantNext   string
		gateFailed bool
	}{
		{name: "pass", findings: below, wantNext: "address open threads", gateFailed: false},
		{name: "fail", findings: atFloor, wantNext: "address open threads", gateFailed: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, ref := reportcore.Run(context.Background(), &reportcore.Request{
				ReadRequest: fixture(t, tc.findings),
				Flags:       reportcore.Flags{Check: "important"},
			})
			if ref != nil {
				t.Fatalf("Run refusal: %v", ref)
			}
			assertGateNext(t, resp, tc.gateFailed, tc.wantNext)
		})
	}
}

func assertGateNext(t *testing.T, resp *reportcore.Response, gateFailed bool, wantNext string) {
	t.Helper()
	if resp.GateFailed != gateFailed {
		t.Errorf("GateFailed = %v, want %v", resp.GateFailed, gateFailed)
	}
	if resp.Next != wantNext {
		t.Errorf("Next = %q, want %q", resp.Next, wantNext)
	}
	if got, want := resp.Human[len(resp.Human)-1], "next: "+wantNext; got != want {
		t.Errorf("human footer = %q, want %q", got, want)
	}
}

// TestRunReportCompletenessGate pins the HEAD-reviewed dimension: a matching
// post passes, and a missing, head-less, or stale post fails with its typed
// cause naming the re-fetch command. The combined case proves one run fails on
// either dimension.
//
//nolint:cyclop,gocognit,revive // each dimension asserts its typed detail inline
func TestRunReportCompletenessGate(t *testing.T) {
	tests := []struct {
		name       string
		post       string
		wantStatus string
		wantSubs   []string
		wantFail   bool
		postSet    bool
	}{
		{
			name: "matching-post-passes", post: `{"head":"` + storeHeadSHA + `"}`, postSet: true,
			wantStatus: "pass", wantFail: false,
			wantSubs: []string{"HEAD aaaaaaa reviewed (post matches fetch)"},
		},
		{
			name: "missing-post-fails", wantStatus: "unknown", wantFail: true,
			wantSubs: []string{"missing-post", "verdict fetch acme/app 5101"},
		},
		{
			name: "headless-post-fails", post: `{"state":"POSTED"}`, postSet: true,
			wantStatus: "unknown", wantFail: true,
			wantSubs: []string{"corrupt-post", "fetched HEAD aaaaaaa"},
		},
		{
			name: "stale-post-fails", post: `{"head":"` + strings.Repeat("c", 40) + `"}`, postSet: true,
			wantStatus: "fail", wantFail: true,
			wantSubs: []string{"stale-head", "posted HEAD ccccccc != fetched HEAD aaaaaaa", "--base-sha ccccccc"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := fixture(t, "[]")
			if tc.postSet {
				writeFile(t, ".verdict/state/5101/post.json", tc.post)
			}
			resp, ref := reportcore.Run(context.Background(), &reportcore.Request{
				ReadRequest: req,
				Flags:       reportcore.Flags{Completeness: true},
			})
			if ref != nil {
				t.Fatalf("Run refusal: %v", ref)
			}
			if resp.GateFailed != tc.wantFail {
				t.Errorf("GateFailed = %v, want %v", resp.GateFailed, tc.wantFail)
			}
			requireDims(t, resp, wantDim{id: "head", status: tc.wantStatus})
			requireDimDetail(t, resp, "head", tc.wantSubs...)
		})
	}

	t.Run("combined-fails-on-either", func(t *testing.T) {
		req := fixture(t, "[]")
		resp, ref := reportcore.Run(context.Background(), &reportcore.Request{
			ReadRequest: req,
			Flags:       reportcore.Flags{Check: "critical", Completeness: true},
		})
		if ref != nil {
			t.Fatalf("Run refusal: %v", ref)
		}
		if !resp.GateFailed {
			t.Error("GateFailed = false; a missing post must fail the combined gate")
		}
		if len(resp.Gate.Dimensions) != 5 {
			t.Fatalf("dimensions = %s, want one per dimension (tier, head, live, spec, evidence)", dimSummary(resp))
		}
		if first := resp.Gate.Dimensions[0]; first.ID != "findings" || first.Status != "pass" ||
			!strings.HasPrefix(first.Detail, "no open findings at-or-above critical") {
			t.Errorf("dimension 0 = %+v, want the passing tier dimension first", first)
		}
		if second := resp.Gate.Dimensions[1]; second.ID != "head" || !strings.Contains(second.Detail, "missing-post") {
			t.Errorf("dimension 1 = %+v, want the head dimension second", second)
		}
	})
}

// TestRunReportPostedHeadRefetchCommand pins the stale-head recovery command
// per host: a host whose fetch owns the compare re-fetches incrementally and
// still names itself off the GitHub default, while a host without that
// support names only its provider.
func TestRunReportPostedHeadRefetchCommand(t *testing.T) {
	tests := []struct {
		provider string
		want     string
		absent   string
	}{
		{
			provider: "gitlab",
			want:     "verdict fetch acme/app 5101 --provider gitlab --base-sha ddddddd",
			absent:   "acme/app 5101 --base-sha",
		},
		{
			provider: "github",
			want:     "verdict fetch acme/app 5101 --base-sha ddddddd",
			absent:   "--provider",
		},
		{
			provider: "forgejo",
			want:     "verdict fetch acme/app 5101 --provider forgejo",
			absent:   "--base-sha",
		},
	}
	for _, tc := range tests {
		t.Run(tc.provider, func(t *testing.T) {
			assertRefetchHint(t, stalePostHeadDetail(t, tc.provider), tc.want, tc.absent)
		})
	}
}

func assertRefetchHint(t *testing.T, detail, want, absent string) {
	t.Helper()
	if !strings.Contains(detail, want) {
		t.Errorf("head detail = %q, want it to contain %q", detail, want)
	}
	if strings.Contains(detail, absent) {
		t.Errorf("head detail = %q, want it not to contain %q", detail, absent)
	}
}

func stalePostHeadDetail(t *testing.T, provider string) string {
	t.Helper()
	req := fixture(t, "[]")
	writeHostBundle(t, provider)
	writeFile(t, ".verdict/state/5101/post.json", `{"head":"`+strings.Repeat("d", 40)+`"}`)
	resp, ref := reportcore.Run(context.Background(), &reportcore.Request{
		ReadRequest: req,
		Flags:       reportcore.Flags{Completeness: true, Live: "skip"},
	})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if !resp.GateFailed {
		t.Error("GateFailed = false, want true for a stale post")
	}
	return dimOf(t, resp, "head").Detail
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
//nolint:gocognit,cyclop,revive // the live matrix asserts calls, dimensions, warns, and the exit inline
func TestRunReportLiveGate(t *testing.T) {
	oneOpen := "[" + entryJSON("1111111111111111111111111111111111111111", "inline", "open", `"important"`, "true") + "]"

	tests := []struct {
		name      string
		findings  string
		live      string
		page      string
		wantDims  []wantDim
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
			wantDims: []wantDim{{"live_threads", "fail", "2 live open thread(s) vs 1 local open finding(s)"}},
		},
		{
			name: "equal-count-passes", findings: oneOpen, gate: true,
			page: threadsPage(1, 1), wantCalls: 1, wantFail: false,
			wantDims: []wantDim{{"live_threads", "pass", "1 live open thread(s) vs 1 local open finding(s)"}},
		},
		{
			name: "resolved-threads-ignored", findings: oneOpen, gate: true,
			page: threadsPage(0, 3), wantCalls: 1, wantFail: false,
			wantDims: []wantDim{{"live_threads", "pass", "0 live open thread(s) vs 1 local open finding(s)"}},
			noWarns:  []string{"malformed thread node"},
		},
		{
			name: "malformed-thread-counts-open", findings: oneOpen, gate: true,
			page:      `{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[{"isResolved":"yes"}]}}}}}`,
			wantCalls: 1, wantFail: false,
			wantDims:  []wantDim{{"live_threads", "pass", "1 live open thread(s) vs 1 local open finding(s)"}},
			wantWarns: []string{"live thread recheck: 1 malformed thread node(s) counted as unresolved (fail-closed)"},
		},
		{
			name: "unreachable-auto-neutral", findings: oneOpen, gate: true,
			cmdFails: true, wantCalls: 1, wantFail: false,
			wantDims:  []wantDim{{"live_threads", "skip", "live thread recheck unreachable — gating on local dimensions only"}},
			wantWarns: []string{"live thread recheck unreachable (boom — gating on local dimensions only)"},
			noWarns:   []string{"gate fails"},
		},
		{
			name: "unreachable-strict-fails-closed", findings: oneOpen, live: "strict", gate: true,
			cmdFails: true, wantCalls: 1, wantFail: true,
			wantDims: []wantDim{{"live_threads", "unknown", "live thread recheck unreachable (boom) — offline posture is strict"}},
		},
		{
			name: "skip-explicit-neutral", findings: oneOpen, live: "skip", gate: true,
			page: threadsPage(9, 0), wantCalls: 0, wantFail: false,
			wantDims:  []wantDim{{"live_threads", "skip", "live recheck skipped (--live skip)"}},
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
			flags := reportcore.Flags{Live: tc.live}
			if tc.gate {
				writeFile(t, ".verdict/state/5101/post.json", `{"head":"`+storeHeadSHA+`"}`)
				flags.Completeness = true
			}
			resp, ref := reportcore.Run(context.Background(), &reportcore.Request{ReadRequest: req, Flags: flags})
			if ref != nil {
				t.Fatalf("Run refusal: %v", ref)
			}
			if cmd.calls != tc.wantCalls {
				t.Errorf("commander calls = %d, want %d", cmd.calls, tc.wantCalls)
			}
			if resp.GateFailed != tc.wantFail {
				t.Errorf("GateFailed = %v, want %v\n%s", resp.GateFailed, tc.wantFail, dimSummary(resp))
			}
			requireDims(t, resp, tc.wantDims...)
			for _, want := range tc.wantWarns {
				if !containsLine(resp.Warnings, want) {
					t.Errorf("Warnings = %q, want it to contain %q", resp.Warnings, want)
				}
			}
			for _, banned := range tc.noWarns {
				for _, w := range append([]string{dimSummary(resp)}, resp.Warnings...) {
					if strings.Contains(w, banned) {
						t.Errorf("line %q must not mention %q", w, banned)
					}
				}
			}
			if !tc.gate && resp.Gate != nil {
				t.Errorf("advisory run composed a gate: %s", dimSummary(resp))
			}
		})
	}
}

type liveProgress struct {
	human    []string
	warnings []string
}

func (p *liveProgress) Human(line string) { p.human = append(p.human, line) }
func (p *liveProgress) Warn(msg string)   { p.warnings = append(p.warnings, msg) }
func (p *liveProgress) Diag(_ string)     {}

func TestRunReportLocalBundleLiveSkip(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeFile(t, ".verdict/state/local-gate/source.json",
		`{"head_sha":"`+storeHeadSHA+`","base_sha":"`+strings.Repeat("b", 40)+`"}`)
	writeFile(t, ".verdict/state/local-gate/findings.json", "["+
		entryJSON("1111111111111111111111111111111111111111", "inline", "open", `"important"`, "true")+"]")
	cmd := &threadCommander{stdout: threadsPage(4, 0)}
	progress := &liveProgress{}
	req := core.ReadRequest{
		Runtime: &core.Runtime{Now: func() time.Time { return fixedNow }, Commander: cmd, Progress: progress},
		Repo:    "acme/app", Pr: "local-gate",
	}
	resp, ref := reportcore.Run(context.Background(), &reportcore.Request{
		ReadRequest: req,
		Flags:       reportcore.Flags{Completeness: true},
	})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if cmd.calls != 0 {
		t.Errorf("commander calls = %d; a local bundle pays zero network", cmd.calls)
	}
	wantInfo := "info: live recheck skipped: local bundle threads unreadable here (gating on local dimensions only)"
	if !containsLine(progress.human, wantInfo) {
		t.Errorf("human stream = %q, want %q", progress.human, wantInfo)
	}
	if containsLine(resp.Warnings, "live recheck skipped") {
		t.Errorf("Warnings = %q, want no warning for the structural local-bundle skip", resp.Warnings)
	}
	if containsLine(progress.warnings, "live recheck skipped") {
		t.Errorf("warning stream = %q, want no structural skip warning", progress.warnings)
	}
	if detail := dimOf(t, resp, "live_threads").Detail; strings.Contains(detail, "live open thread(s)") {
		t.Errorf("live detail = %q, want no live-thread verdict", detail)
	}
	if got := liveDimensionStatus(resp); got != "skip" {
		t.Errorf("live dimension status = %q, want skip", got)
	}
}

func liveDimensionStatus(resp *reportcore.Response) string {
	if resp.Gate == nil {
		return ""
	}
	for _, d := range resp.Gate.Dimensions {
		if d.ID == "live_threads" {
			return d.Status
		}
	}
	return ""
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
		resp, ref := reportcore.Run(context.Background(), &reportcore.Request{ReadRequest: req})
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
		resp, ref := reportcore.Run(context.Background(), &reportcore.Request{
			ReadRequest: req,
			Flags:       reportcore.Flags{Status: "open"},
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
	resp, ref := reportcore.Run(context.Background(), &reportcore.Request{ReadRequest: req})
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
	if resp.Gate != nil || resp.GateFailed {
		t.Errorf("an advisory run composed a gate: %s", dimSummary(resp))
	}
}

// liveThreadCommander scripts the per-host reads a live probe issues, keyed by
// the argv tail after the tool name. An unscripted call answers with an empty
// success, so a leg that forgets a fixture fails on the payload rather than on
// a hidden default.
type liveThreadCommander struct {
	script map[string]core.Result
	calls  []string
}

func (c *liveThreadCommander) Run(_ context.Context, cmd *core.Command) (core.Result, error) {
	key := strings.Join(cmd.Argv[1:], " ")
	c.calls = append(c.calls, key)
	return c.script[key], nil
}

// The two glab reads a GitLab live probe issues, in call order.
const (
	gitlabProjectIDCall = "api projects/acme%2Fapp --jq .id"
	gitlabDiscussions   = "api projects/11/merge_requests/5101/discussions?per_page=100&page=1"
)

// The GitCafe reads a partial page walk issues: a full first page naming a
// second cursor, then a comments page, then the failing second page.
const (
	gitcafeReviews     = "pr reviews 5101 -R acme/app --json"
	gitcafeReviewsNext = "pr reviews 5101 -R acme/app --json --cursor c2"
	gitcafeComments    = "pr comments 5101 -R acme/app --filter all --json"
)

// gitlabDiscussion renders one discussion whose first note resolution state is
// resolved. An open note carries no resolved key, the shape GitLab serves for
// a discussion nobody resolved.
func gitlabDiscussion(id string, resolved bool) string {
	flag := ""
	if resolved {
		flag = `"resolved":true,`
	}
	return `{"id":"` + id + `","notes":[{"id":9,"body":"nit",` + flag +
		`"author":{"username":"ana"},"position":{"new_path":"notes.txt","new_line":2,` +
		`"head_sha":"` + storeHeadSHA + `"}}]}`
}

// writeHostBundle rewrites the fixture's PR.json for another host.
func writeHostBundle(t *testing.T, provider string) {
	t.Helper()
	writeFile(t, ".verdict/state/5101/PR.json",
		`{"state":"OPEN","provider":"`+provider+`","headRefOid":"`+storeHeadSHA+
			`","head_sha":"`+storeHeadSHA+`","baseRefOid":"`+strings.Repeat("b", 40)+`"}`)
}

// TestRunReportLiveProviderRead pins the host read: a GitLab bundle's live
// dimension reads that host's discussions through its own adapter instead of
// skipping the dimension, and the completeness recovery command still names
// the provider rather than the GitHub default.
func TestRunReportLiveProviderRead(t *testing.T) {
	req := fixture(t, "["+entryJSON("1111111111111111111111111111111111111111", "inline", "open", `"important"`, "true")+"]")
	writeHostBundle(t, "gitlab")
	cmd := &liveThreadCommander{script: map[string]core.Result{
		gitlabProjectIDCall: {Stdout: "11\n"},
		gitlabDiscussions:   {Stdout: "[" + gitlabDiscussion("d1", false) + "]"},
	}}
	req.Commander = cmd
	resp, ref := reportcore.Run(context.Background(), &reportcore.Request{
		ReadRequest: req,
		Flags:       reportcore.Flags{Completeness: true},
	})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if got, want := strings.Join(cmd.calls, "\n"), gitlabProjectIDCall+"\n"+gitlabDiscussions; got != want {
		t.Errorf("commander calls = %q, want %q", got, want)
	}
	for _, w := range resp.Warnings {
		if strings.Contains(w, "live recheck skipped") {
			t.Errorf("Warnings = %q, want no structural skip on a readable host", resp.Warnings)
		}
	}
	requireDims(t, resp, wantDim{"live_threads", "pass", "1 live open thread(s) vs 1 local open finding(s)"})
	if detail := dimOf(t, resp, "head").Detail; !strings.Contains(detail, "verdict fetch acme/app 5101 --provider gitlab") {
		t.Errorf("head detail = %q, want the provider-aware re-fetch command", detail)
	}
}

// TestRunReportLiveOtherProviderGate pins the live dimension on a non-GitHub
// bundle across hosts: an open discussion fails the gate, a resolved one
// passes, a failed read warns neutral under --live auto and fails closed under
// --live strict, and a page walk that stopped early counts as an unread
// surface rather than a clean one.
//
//nolint:revive // the matrix asserts calls, dimensions, warns, and the exit inline
func TestRunReportLiveOtherProviderGate(t *testing.T) {
	failGitLab := map[string]core.Result{
		gitlabProjectIDCall: {Stdout: "11\n"},
		gitlabDiscussions:   {ExitCode: 1, Stderr: "glab: 500 down"},
	}
	tests := []struct { //nolint:govet // table grouping keeps the case fields readable
		name      string
		provider  string
		live      string
		script    map[string]core.Result
		wantCalls []string
		wantDim   wantDim
		wantWarn  string
		wantFail  bool
	}{
		{
			name: "open-discussion-fails", provider: "gitlab", live: "strict",
			script: map[string]core.Result{
				gitlabProjectIDCall: {Stdout: "11\n"},
				gitlabDiscussions:   {Stdout: "[" + gitlabDiscussion("d1", false) + "]"},
			},
			wantCalls: []string{gitlabProjectIDCall, gitlabDiscussions}, wantFail: true,
			wantDim: wantDim{"live_threads", "fail", "1 live open thread(s) vs 0 local open finding(s)"},
		},
		{
			name: "resolved-discussion-passes", provider: "gitlab", live: "strict",
			script: map[string]core.Result{
				gitlabProjectIDCall: {Stdout: "11\n"},
				gitlabDiscussions:   {Stdout: "[" + gitlabDiscussion("d1", true) + "]"},
			},
			wantCalls: []string{gitlabProjectIDCall, gitlabDiscussions}, wantFail: false,
			wantDim: wantDim{"live_threads", "pass", "0 live open thread(s) vs 0 local open finding(s)"},
		},
		{
			name: "read-failure-strict-fails-closed", provider: "gitlab", live: "strict",
			script:    failGitLab,
			wantCalls: []string{gitlabProjectIDCall, gitlabDiscussions}, wantFail: true,
			wantDim: wantDim{"live_threads", "unknown", "live thread recheck unreachable (glab: 500 down) — offline posture is strict"},
		},
		{
			name: "read-failure-auto-warns", provider: "gitlab",
			script:    failGitLab,
			wantCalls: []string{gitlabProjectIDCall, gitlabDiscussions}, wantFail: false,
			wantWarn: "live thread recheck unreachable (glab: 500 down — " +
				"gating on local dimensions only)",
			wantDim: wantDim{"live_threads", "skip", "live thread recheck unreachable — gating on local dimensions only"},
		},
		{
			name: "partial-page-walk-strict-fails-closed", provider: "gitcafe", live: "strict",
			script: map[string]core.Result{
				gitcafeReviews: {Stdout: `{"data":{"items":[{"id":"r1","body":"review",` +
					`"author":{"handle":"rev"}}]},"next":"c2"}`},
				gitcafeReviewsNext: {ExitCode: 1, Stderr: "cafe: 500 down"},
				gitcafeComments:    {Stdout: `{"data":{"items":[]}}`},
			},
			wantCalls: []string{gitcafeReviews, gitcafeReviewsNext, gitcafeComments}, wantFail: true,
			wantDim: wantDim{"live_threads", "unknown", "live thread recheck unreachable (gitcafe thread read incomplete) — offline posture is strict"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := fixture(t, "[]")
			writeHostBundle(t, tc.provider)
			cmd := &liveThreadCommander{script: tc.script}
			req.Commander = cmd
			resp, ref := reportcore.Run(context.Background(), &reportcore.Request{
				ReadRequest: req,
				Flags:       reportcore.Flags{Check: "critical", Live: tc.live},
			})
			if ref != nil {
				t.Fatalf("Run refusal: %v", ref)
			}
			if got, want := strings.Join(cmd.calls, "\n"), strings.Join(tc.wantCalls, "\n"); got != want {
				t.Errorf("commander calls = %q, want %q", got, want)
			}
			if resp.GateFailed != tc.wantFail {
				t.Errorf("GateFailed = %v, want %v\n%s", resp.GateFailed, tc.wantFail, dimSummary(resp))
			}
			requireDims(t, resp, tc.wantDim)
			if tc.wantWarn != "" && !containsLine(resp.Warnings, tc.wantWarn) {
				t.Errorf("Warnings = %q, want it to contain %q", resp.Warnings, tc.wantWarn)
			}
		})
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
		noRecord  bool
		wantFail  bool
	}{
		{
			name: "tier-only-pass", findings: "[]", live: "skip", check: "critical",
			wantIDs: []string{"findings", "live_threads", "spec", "evidence"}, wantStat: []string{"pass", "skip", "pass", "pass"},
			wantCount: []int{0, -1, 0, 0}, wantFail: false,
		},
		{
			name:     "tier-only-fail",
			findings: "[" + entryJSON("1111111111111111111111111111111111111111", "inline", "open", `"critical"`, "true") + "]",
			live:     "skip", check: "critical",
			wantIDs: []string{"findings", "live_threads", "spec", "evidence"}, wantStat: []string{"fail", "skip", "pass", "pass"},
			wantCount: []int{1, -1, 0, 0}, wantFail: true,
		},
		{
			// R11: --require-head without --max-severity still carries the
			// evidence dimension, which passes only on a receipt for the head.
			name: "head-only-missing", findings: "[]", live: "skip", complete: true,
			wantIDs: []string{"head", "live_threads", "spec", "evidence"}, wantStat: []string{"unknown", "skip", "pass", "pass"},
			wantCount: []int{-1, -1, 0, 0}, wantFail: true,
		},
		{
			name: "head-stale", findings: "[]", post: `{"head":"` + strings.Repeat("c", 40) + `"}`,
			live: "skip", complete: true,
			wantIDs: []string{"head", "live_threads", "spec", "evidence"}, wantStat: []string{"fail", "skip", "pass", "pass"},
			wantCount: []int{-1, -1, 0, 0}, wantFail: true,
		},
		{
			name: "head-pass", findings: "[]", post: `{"head":"` + storeHeadSHA + `"}`,
			live: "skip", complete: true,
			wantIDs: []string{"head", "live_threads", "spec", "evidence"}, wantStat: []string{"pass", "skip", "pass", "pass"},
			wantCount: []int{-1, -1, 0, 0}, wantFail: false,
		},
		{
			// R11: no receipt means no passing gate, whatever dimension the
			// operator asked for.
			name: "head-pass-without-receipt", findings: "[]", post: `{"head":"` + storeHeadSHA + `"}`,
			live: "skip", complete: true, noRecord: true,
			wantIDs: []string{"head", "live_threads", "spec", "evidence"}, wantStat: []string{"pass", "skip", "pass", "unknown"},
			wantCount: []int{-1, -1, 0, -1}, wantFail: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := fixture(t, tc.findings)
			if tc.post != "" {
				writeFile(t, ".verdict/state/5101/post.json", tc.post)
			}
			if tc.noRecord {
				if err := os.Remove(".verdict/state/5101/record.json"); err != nil {
					t.Fatalf("remove record.json: %v", err)
				}
			}
			flags := reportcore.Flags{Check: tc.check, Completeness: tc.complete, Live: tc.live}
			resp, ref := reportcore.Run(context.Background(), &reportcore.Request{ReadRequest: req, Flags: flags})
			if ref != nil {
				t.Fatalf("Run refusal: %v", ref)
			}
			gate := resp.Gate
			if gate == nil {
				t.Fatal("data.gate is nil on a gate run")
			}
			if gate.Passed != !tc.wantFail {
				t.Errorf("passed = %v, want %v", gate.Passed, !tc.wantFail)
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
		resp, ref := reportcore.Run(context.Background(), &reportcore.Request{ReadRequest: req})
		if ref != nil {
			t.Fatalf("Run refusal: %v", ref)
		}
		if resp.Gate != nil {
			t.Errorf("data.gate = %+v on an advisory run, want none", resp.Gate)
		}
	})
}

// TestRunReportBadTierRefusal proves the service refuses an unpublished gate
// tier in core, so the CLI, MCP, and Go SDK share one rule. Without it an
// unknown tier read as "critical only" in the gate and as the untagged rank
// everywhere else, so the same run passed and failed depending on the entry
// point.
func TestRunReportBadTierRefusal(t *testing.T) {
	store := "[" + entryJSON("1111111111111111111111111111111111111111", "inline", "open", `"informational"`, "true") + "]"
	req := fixture(t, store)
	_, ref := reportcore.Run(context.Background(), &reportcore.Request{
		ReadRequest: req,
		Flags:       reportcore.Flags{Check: "bogus"},
	})
	if ref == nil {
		t.Fatal("Run with Check bogus refused nothing, want bad-flags")
	}
	if ref.Code != "bad-flags" {
		t.Errorf("refusal code = %q, want bad-flags", ref.Code)
	}
	want := "gate: unknown gate tier 'bogus' (want critical|important|informational)"
	if ref.Message != want {
		t.Errorf("refusal message = %q, want %q", ref.Message, want)
	}
}

// wantUntaggedCounts builds the expected whole-store counts for a fixture
// whose entries carry no category: every published key zero, untagged holding
// the whole store.
func wantUntaggedCounts(inline, section, anchored, unanchored, untagged int) core.ReportCounts {
	return core.ReportCounts{
		Inline: inline, Section: section, Anchored: anchored, Unanchored: unanchored,
		ByCategory: map[string]int{
			"logic": 0, "edge-case": 0, "security": 0, "performance": 0,
			"accidental-commit": 0, "style": 0, "docs": 0, "other": 0, "untagged": untagged,
		},
		BySource: map[string]int{"model": inline + section, "tool": 0},
	}
}
