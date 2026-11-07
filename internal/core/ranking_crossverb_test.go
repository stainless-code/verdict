package core_test

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	fixcore "github.com/stainless-code/verdict/internal/core/fix"
	reportcore "github.com/stainless-code/verdict/internal/core/report"
)

// rankingGitCommander answers fix's clean-worktree audit.
type rankingGitCommander struct{}

// Run returns an empty audit for git and fails any unscripted spawn.
func (rankingGitCommander) Run(_ context.Context, cmd *core.Command) (core.Result, error) {
	if cmd.Argv[0] == "git" {
		return core.Result{}, nil
	}
	return core.Result{}, fmt.Errorf("unexpected argv: %q", cmd.Argv)
}

// rankingFinding renders one inline finding with explicit rank fields.
func rankingFinding(sig, status, severity, rigor, path string, line int, anchored, inBounds bool) string {
	return fmt.Sprintf(`{"kind":"inline","path":%q,"side":"RIGHT","line":%d,`+
		`"anchored":%t,"body":"Body here with enough substance.","suggestion":null,`+
		`"inBounds":%t,"rigor":%q,"severity":%q,"sig":%q,"sig_version":"1",`+
		`"firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":%q,"rationale":"","history":[]}`,
		path, line, anchored, inBounds, rigor, severity, sig, status)
}

// writeRankingFixture creates one local bundle with the given entries and
// returns a request whose runtime makes fix's audit hermetic.
func writeRankingFixture(t *testing.T, findings ...string) core.ReadRequest {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.MkdirAll(".verdict/state/local-rank", 0o755); err != nil {
		t.Fatal(err)
	}
	doc := "[" + strings.Join(findings, ",") + "]"
	if err := os.WriteFile(".verdict/state/local-rank/findings.json", []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	return core.ReadRequest{
		Runtime: &core.Runtime{
			Now:       func() time.Time { return time.Time{} },
			Commander: rankingGitCommander{},
		},
		Repo: "acme/app",
		Pr:   "local-rank",
	}
}

// rankingItemSig reads the full sig of one next item.
func rankingItemSig(t *testing.T, item *core.FindingItem) string {
	t.Helper()
	if item == nil || item.Raw == nil {
		t.Fatalf("next item = %+v; want a raw finding", item)
	}
	v, _ := item.Raw.Get("sig")
	s, ok := v.(string)
	if !ok {
		t.Fatalf("next item sig = %v; want a string", v)
	}
	return s
}

// rankingSigs are the three identity sigs the table reorders.
const (
	rankSigA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	rankSigB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	rankSigC = "cccccccccccccccccccccccccccccccccccccccc"
)

// nextPicked runs next and returns its top item's full sig.
func nextPicked(t *testing.T, req core.ReadRequest) string {
	t.Helper()
	resp, ref := reportcore.RunNext(context.Background(), &core.NextRequest{ReadRequest: req})
	if ref != nil {
		t.Fatalf("next refusal: %v", ref)
	}
	if resp.StoreUnreadable || resp.Item == nil {
		t.Fatalf("next = %+v; want a top item", resp)
	}
	return rankingItemSig(t, resp.Item)
}

// fixPicked runs fix's default pass and returns its target sig.
func fixPicked(t *testing.T, req core.ReadRequest) string {
	t.Helper()
	resp, ref := fixcore.Run(context.Background(), &fixcore.Request{
		ReadRequest: req,
		Human:       io.Discard,
	})
	if ref != nil {
		t.Fatalf("fix refusal: %v", ref)
	}
	if resp.Data == nil || resp.Data.Sig == nil {
		t.Fatalf("fix data = %+v; want a pass target", resp.Data)
	}
	return *resp.Data.Sig
}

// rankingCase is one ranking table row.
type rankingCase struct {
	name     string
	want     string
	findings []string
}

// TestNextAndFixPickTheSameFinding pins next's top item and fix's default
// pass target to the same eight-key ranking for findings that differ in one
// ranked key at a time. The rigor, anchored, and inBounds rows are the ones
// the old four-key fix comparator got wrong.
func TestNextAndFixPickTheSameFinding(t *testing.T) {
	cases := []rankingCase{
		{name: "rigor", want: rankSigB, findings: []string{
			rankingFinding(rankSigA, "open", "important", "said", "notes.txt", 2, true, true),
			rankingFinding(rankSigB, "open", "important", "reproduced", "notes.txt", 2, true, true),
		}},
		{name: "anchored", want: rankSigB, findings: []string{
			rankingFinding(rankSigA, "open", "important", "walked", "notes.txt", 2, false, true),
			rankingFinding(rankSigB, "open", "important", "walked", "notes.txt", 2, true, true),
		}},
		{name: "inBounds", want: rankSigB, findings: []string{
			rankingFinding(rankSigA, "open", "important", "walked", "notes.txt", 2, true, false),
			rankingFinding(rankSigB, "open", "important", "walked", "notes.txt", 2, true, true),
		}},
		{name: "path", want: rankSigA, findings: []string{
			rankingFinding(rankSigB, "open", "important", "walked", "b.txt", 2, true, true),
			rankingFinding(rankSigA, "open", "important", "walked", "a.txt", 2, true, true),
		}},
		{name: "severity", want: rankSigB, findings: []string{
			rankingFinding(rankSigA, "open", "informational", "walked", "notes.txt", 2, true, true),
			rankingFinding(rankSigB, "open", "critical", "walked", "notes.txt", 2, true, true),
		}},
		{name: "status filter", want: rankSigB, findings: []string{
			rankingFinding(rankSigC, "deferred", "critical", "reproduced", "a.txt", 1, true, true),
			rankingFinding(rankSigB, "open", "important", "walked", "notes.txt", 2, true, true),
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertPicksAgree(t, tc)
		})
	}
}

// assertPicksAgree proves next and fix target the same want sig.
func assertPicksAgree(t *testing.T, tc rankingCase) {
	t.Helper()
	req := writeRankingFixture(t, tc.findings...)
	if got := nextPicked(t, req); got != tc.want {
		t.Errorf("next picked %q; want %q", got, tc.want)
	}
	if got := fixPicked(t, req); got != tc.want {
		t.Errorf("fix target = %q; want %q", got, tc.want)
	}
}

// TestTopOpenInlineSkipsSectionAndDeferred proves the shared selector only
// ranks open inline findings even when a higher-severity section or deferred
// entry is present.
func TestTopOpenInlineSkipsSectionAndDeferred(t *testing.T) {
	req := writeRankingFixture(t,
		`{"kind":"section","path":"notes.txt","line":1,"severity":"critical",`+
			`"rigor":"reproduced","anchored":true,"inBounds":true,`+
			`"sig":"`+rankSigC+`","status":"open"}`,
		rankingFinding(rankSigA, "deferred", "critical", "reproduced", "a.txt", 1, true, true),
		rankingFinding(rankSigB, "open", "informational", "said", "notes.txt", 2, true, true),
	)
	resp, ref := fixcore.Run(context.Background(), &fixcore.Request{ReadRequest: req, Human: io.Discard})
	if ref != nil {
		t.Fatalf("fix refusal: %v", ref)
	}
	if resp.Data == nil || resp.Data.Sig == nil || *resp.Data.Sig != rankSigB {
		t.Errorf("fix target = %+v; want the open inline %q", resp.Data, rankSigB)
	}
}
