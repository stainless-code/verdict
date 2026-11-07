package readycore_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
	readycore "github.com/stainless-code/verdict/internal/core/ready"
)

// readyCommander answers the three live reads ready performs.
type readyCommander struct{ head string }

func (c *readyCommander) LookPath(string) error { return nil }

func (c *readyCommander) Run(_ context.Context, cmd *core.Command) (core.Result, error) {
	joined := strings.Join(cmd.Argv, " ")
	switch {
	case strings.Contains(joined, "pr checks"):
		return core.Result{Stdout: `[]`}, nil
	case strings.Contains(joined, "pr view"):
		return core.Result{Stdout: `{"isDraft":false,"mergeable":"MERGEABLE","mergeStateStatus":"CLEAN",` +
			`"body":"A description that is long enough to pass the thin check easily here.",` +
			`"state":"OPEN","headRefOid":"` + c.head + `"}`}, nil
	case strings.Contains(joined, "graphql"):
		return core.Result{Stdout: `{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[]}}}}}`}, nil
	}
	return core.Result{Stderr: "unstubbed", ExitCode: 1}, nil
}

// writeReadyBundle writes the PR bundle and the typed capture for one run.
func writeReadyBundle(t *testing.T, rs *config.RunState) string {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	bundle := filepath.Join(".verdict", "state", "5201")
	mkdir(t, bundle)
	head := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	write(t, filepath.Join(bundle, "PR.json"),
		`{"state":"OPEN","headRefOid":"`+head+`","baseRefOid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`)
	if err := config.WriteRunState(bundle, rs); err != nil {
		t.Fatalf("WriteRunState: %v", err)
	}
	if _, ref := controls.Resolve(t.Context(), nil, &controls.Request{
		BundleDir: bundle, Root: ".", BaseSHA: rs.BaseSHA, AllowHead: true,
	}); ref != nil {
		t.Fatalf("controls.Resolve: %v", ref)
	}
	return bundle
}

func mkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// complianceResolved builds a genuine resolution carrying one critical
// security clause and the head-current required check.
func complianceResolved(t *testing.T) (*config.Resolved, *config.RunState) {
	t.Helper()
	repo := &core.Config{
		SchemaVersion: "1",
		Requirements: &core.ConfigRequirements{
			BlockClauses: []core.BlockClause{{
				Name: "security-critical", Threshold: "critical", Categories: []string{"security"},
			}},
			RequireChecks: []string{"head-current"},
		},
	}
	resolved, ref := config.Resolve(&config.Inputs{
		Repo:  repo,
		Files: []config.FileInput{{Path: "notes.txt"}},
	})
	if ref != nil {
		t.Fatalf("Resolve: %v", ref)
	}
	rs := &config.RunState{
		BaseSHA:  "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Trusted:  true,
		Resolved: resolved,
		Root:     config.RootSet(repo, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"),
	}
	return resolved, rs
}

// TestReadyCapturedComplianceBlocks proves ready reflects captured policy in
// its boolean and blockers: a pending required check and an open critical
// finding block readiness, and --check fails.
func TestReadyCapturedComplianceBlocks(t *testing.T) {
	_, rs := complianceResolved(t)
	bundle := writeReadyBundle(t, rs)
	write(t, filepath.Join(bundle, "findings.json"), `[`+securityFinding+`]`)
	req := &readycore.Request{Check: true}
	req.ReadRequest = core.ReadRequest{
		Runtime: &core.Runtime{Commander: &readyCommander{head: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}},
		Repo:    "acme/app", Pr: "5201",
	}
	resp, ref := readycore.Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("ready refusal: %v", ref)
	}
	if resp.Ready {
		t.Fatal("ready passed with a blocking captured finding and pending check")
	}
	if !resp.GateFailed {
		t.Fatal("ready --check passed while captured requirements are unmet")
	}
	kinds := make([]string, 0, len(resp.Data.Blockers))
	for _, blocker := range resp.Data.Blockers {
		kinds = append(kinds, blocker.Kind)
	}
	if !slices.Contains(kinds, "compliance") {
		t.Fatalf("blockers = %v, want a compliance blocker", kinds)
	}
}

// TestReadySatisfiedCaptureStaysReady proves a fully observed capture with no
// blocking finding stays ready and --check does not fail.
func TestReadySatisfiedCaptureStaysReady(t *testing.T) {
	resolved, rs := complianceResolved(t)
	rs.Outcomes = []config.CheckObservation{{
		Check: "head-current", Group: resolved.Plan.Groups[0].ID,
		Head: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Passed: true,
	}}
	bundle := writeReadyBundle(t, rs)
	write(t, filepath.Join(bundle, "findings.json"), `[]`)
	req := &readycore.Request{Check: true}
	req.ReadRequest = core.ReadRequest{
		Runtime: &core.Runtime{Commander: &readyCommander{head: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}},
		Repo:    "acme/app", Pr: "5201",
	}
	resp, ref := readycore.Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("ready refusal: %v", ref)
	}
	if !resp.Ready || resp.GateFailed {
		t.Fatalf("ready = %v gateFailed = %v blockers = %+v warnings = %v, want ready and passing",
			resp.Ready, resp.GateFailed, resp.Data.Blockers, resp.Warnings)
	}
}

const securityFinding = `{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,` +
	`"anchored":true,"body":"Body.","suggestion":null,"inBounds":true,"rigor":"walked",` +
	`"severity":"critical","category":"security","sig":"c111111111111111111111111111111111111111","sig_version":"1",` +
	`"firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}`

// resolveReadyCapture resolves one repository configuration over notes.txt.
func resolveReadyCapture(t *testing.T, repo *core.Config, personal *config.PersonalSettings) *config.Resolved {
	t.Helper()
	resolved, ref := config.Resolve(&config.Inputs{
		Repo: repo, Personal: personal,
		Files: []config.FileInput{{Path: "notes.txt"}},
	})
	if ref != nil {
		t.Fatalf("Resolve: %v", ref)
	}
	return resolved
}

// readyWithCapture runs ready over a real captured run with no findings.
func readyWithCapture(t *testing.T, resolved *config.Resolved) *readycore.Response {
	t.Helper()
	rs := &config.RunState{
		BaseSHA: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Trusted: true, Resolved: resolved,
	}
	bundle := writeReadyBundle(t, rs)
	write(t, filepath.Join(bundle, "findings.json"), `[]`)
	req := &readycore.Request{Check: true}
	req.ReadRequest = core.ReadRequest{
		Runtime: &core.Runtime{Commander: &readyCommander{head: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}},
		Repo:    "acme/app", Pr: "5201",
	}
	resp, ref := readycore.Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("ready refusal: %v", ref)
	}
	return resp
}

// TestReadyDeepGroupWithoutReceiptBlocks proves a deep-only group is required
// work: readiness fails with a profile-work blocker instead of passing.
func TestReadyDeepGroupWithoutReceiptBlocks(t *testing.T) {
	deep := "deep"
	resolved := resolveReadyCapture(t, &core.Config{
		SchemaVersion: "1",
		Profiles:      map[string]core.ConfigProfile{"deep": {Depth: &deep}},
		Scopes:        []core.ConfigScope{{Name: "deep", Include: []string{"notes.txt"}, Profile: &deep}},
	}, nil)
	resp := readyWithCapture(t, resolved)
	if resp.Ready || !resp.GateFailed {
		t.Fatalf("deep gap passed: ready=%v gateFailed=%v blockers=%+v", resp.Ready, resp.GateFailed, resp.Data.Blockers)
	}
	if !hasBlocker(resp.Data.Blockers, "profile-work") {
		t.Fatalf("blockers = %+v, want profile-work", resp.Data.Blockers)
	}
}

// TestReadyPrivacyConflictBlocks proves withheld required review blocks ready.
func TestReadyPrivacyConflictBlocks(t *testing.T) {
	resolved := resolveReadyCapture(t, &core.Config{SchemaVersion: "1", Requirements: &core.ConfigRequirements{
		BlockClauses: []core.BlockClause{{Name: "secret", Threshold: "critical", Paths: []string{"notes.txt"}}},
	}}, &config.PersonalSettings{Privacy: config.PersonalPrivacy{Exclusions: []string{"notes.txt"}}})
	resp := readyWithCapture(t, resolved)
	if resp.Ready {
		t.Fatal("ready passed while required review is withheld")
	}
	if !hasBlocker(resp.Data.Blockers, "compliance-conflict") {
		t.Fatalf("blockers = %+v, want compliance-conflict", resp.Data.Blockers)
	}
}

func hasBlocker(blockers []readycore.Blocker, kind string) bool {
	for i := range blockers {
		if blockers[i].Kind == kind {
			return true
		}
	}
	return false
}
