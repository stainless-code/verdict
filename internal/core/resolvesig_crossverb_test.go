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
	triagecore "github.com/stainless-code/verdict/internal/core/triage"
)

// sigGitCommander answers fix's clean-worktree audit.
type sigGitCommander struct{}

// Run returns an empty audit for git and fails any unscripted spawn.
func (sigGitCommander) Run(_ context.Context, cmd *core.Command) (core.Result, error) {
	if cmd.Argv[0] == "git" {
		return core.Result{}, nil
	}
	return core.Result{}, fmt.Errorf("unexpected argv: %q", cmd.Argv)
}

// sigResolveFinding renders one open inline finding with sig.
func sigResolveFinding(sig string) string {
	return fmt.Sprintf(`{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,`+
		`"anchored":true,"body":"Body here with enough substance.","suggestion":null,`+
		`"inBounds":true,"rigor":"walked","severity":"important","sig":%q,`+
		`"sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa",`+
		`"status":"open","rationale":"","history":[]}`, sig)
}

// writeSigFixture creates one local bundle and returns a request whose
// runtime makes fix's audit hermetic.
func writeSigFixture(t *testing.T, findings ...string) core.ReadRequest {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.MkdirAll(".verdict/state/local-sig", 0o755); err != nil {
		t.Fatal(err)
	}
	doc := "[" + strings.Join(findings, ",") + "]"
	if err := os.WriteFile(".verdict/state/local-sig/findings.json", []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	return core.ReadRequest{
		Runtime: &core.Runtime{
			Now:       func() time.Time { return time.Time{} },
			Commander: sigGitCommander{},
		},
		Repo: "acme/app",
		Pr:   "local-sig",
	}
}

// sigResolveTest is the shared identity sig pair.
const (
	sigDuplicate = "deadbeef00000000000000000000000000000000"
	sigUnique    = "cafebabe00000000000000000000000000000000"
)

// TestAmbiguousSigRefusedByReportFixAndTriage pins one ResolveSig tally: two
// entries with the same prefix (including two identical entries) are
// ambiguous in every verb path, so fix --sig cannot act where show refuses.
func TestAmbiguousSigRefusedByReportFixAndTriage(t *testing.T) {
	req := writeSigFixture(t, sigResolveFinding(sigDuplicate), sigResolveFinding(sigDuplicate))
	const prefix = "deadbeef"

	if _, ref := reportcore.RunShow(context.Background(), &core.ShowRequest{
		ReadRequest: req,
		Sig:         prefix,
	}); ref == nil || ref.Code != "ambiguous-sig" {
		t.Errorf("show refusal = %+v; want ambiguous-sig", ref)
	}

	if _, ref := fixcore.Run(context.Background(), &fixcore.Request{
		ReadRequest: req,
		Sig:         prefix,
		Human:       io.Discard,
	}); ref == nil || ref.Code != "ambiguous-sig" {
		t.Errorf("fix refusal = %+v; want ambiguous-sig", ref)
	}

	if _, ref := triagecore.Run(context.Background(), &triagecore.Request{
		ReadRequest: req,
		Sig:         prefix,
		Status:      "addressed",
		Rationale:   "verified the duplicate store stays ambiguous here",
	}); ref == nil || ref.Code != "ambiguous-sig" {
		t.Errorf("triage refusal = %+v; want ambiguous-sig", ref)
	}
}

// assertShowResolves checks show resolves the unique prefix to want.
func assertShowResolves(t *testing.T, req core.ReadRequest, prefix, want string) {
	t.Helper()
	resp, ref := reportcore.RunShow(context.Background(), &core.ShowRequest{
		ReadRequest: req,
		Sig:         prefix,
	})
	if ref != nil {
		t.Fatalf("show refusal: %v", ref)
	}
	if resp.Item == nil || resp.Fix == nil || resp.Fix.Sig != want {
		t.Errorf("show resolved %+v; want %q", resp.Item, want)
	}
}

// assertFixResolves checks fix --sig resolves the unique prefix to want.
func assertFixResolves(t *testing.T, req core.ReadRequest, prefix, want string) {
	t.Helper()
	resp, ref := fixcore.Run(context.Background(), &fixcore.Request{
		ReadRequest: req,
		Sig:         prefix,
		Human:       io.Discard,
	})
	if ref != nil {
		t.Fatalf("fix refusal: %v", ref)
	}
	if resp.Data == nil || resp.Data.Sig == nil || *resp.Data.Sig != want {
		t.Errorf("fix resolved %+v; want %q", resp.Data, want)
	}
}

// assertTriageResolves checks triage resolves the unique prefix to want.
func assertTriageResolves(t *testing.T, req core.ReadRequest, prefix, want string) {
	t.Helper()
	resp, ref := triagecore.Run(context.Background(), &triagecore.Request{
		ReadRequest: req,
		Sig:         prefix,
		Status:      "addressed",
		Rationale:   "verified the unique prefix resolves the same entry",
		DryRun:      true,
	})
	if ref != nil {
		t.Fatalf("triage refusal: %v", ref)
	}
	if resp.Data == nil || resp.Data.Sig != want {
		t.Errorf("triage resolved %+v; want %q", resp.Data, want)
	}
}

// TestUniqueSigResolvesAgreeInEveryVerb proves the shared tally still accepts
// a prefix that matches exactly one entry, and every verb resolves the same
// full sig.
func TestUniqueSigResolvesAgreeInEveryVerb(t *testing.T) {
	req := writeSigFixture(t, sigResolveFinding(sigDuplicate), sigResolveFinding(sigUnique))
	const prefix = "dead"
	assertShowResolves(t, req, prefix, sigDuplicate)
	assertFixResolves(t, req, prefix, sigDuplicate)
	assertTriageResolves(t, req, prefix, sigDuplicate)
}
