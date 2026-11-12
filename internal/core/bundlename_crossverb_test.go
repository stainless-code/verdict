package core_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	adoptcore "github.com/stainless-code/verdict/internal/core/adopt"
	calibratecore "github.com/stainless-code/verdict/internal/core/calibrate"
	fetchcore "github.com/stainless-code/verdict/internal/core/fetch"
	fixcore "github.com/stainless-code/verdict/internal/core/fix"
	indexcore "github.com/stainless-code/verdict/internal/core/index"
	postcore "github.com/stainless-code/verdict/internal/core/post"
	readycore "github.com/stainless-code/verdict/internal/core/ready"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
	reportcore "github.com/stainless-code/verdict/internal/core/report"
	runcore "github.com/stainless-code/verdict/internal/core/run"
	triagecore "github.com/stainless-code/verdict/internal/core/triage"
	verifycore "github.com/stainless-code/verdict/internal/core/verify"
)

// bundleNameRefusal is the one line every bundle verb renders for a positional
// that is not a single path element.
const bundleNameRefusal = "bundle name must be one path element (a PR number or fetch-local name), not a path"

// spawnlessCommander fails any spawn or look-path: a bundle verb must refuse
// the name before it reaches a host.
type spawnlessCommander struct{}

// Run answers any spawn with a loud error.
func (spawnlessCommander) Run(_ context.Context, cmd *core.Command) (core.Result, error) {
	return core.Result{}, fmt.Errorf("unexpected spawn: %q", cmd.Argv)
}

// LookPath answers any tool probe with a loud error.
func (spawnlessCommander) LookPath(tool string) error {
	return fmt.Errorf("unexpected look-path: %s", tool)
}

// bundleRead builds the invocation every bundle verb shares: a fixed clock, a
// HOME with nothing under it, and a commander that fails any host call.
func bundleRead(pr string) core.ReadRequest {
	return core.ReadRequest{
		Runtime: &core.Runtime{
			Now:       func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) },
			Env:       map[string]string{"HOME": "/nonexistent"},
			Commander: spawnlessCommander{},
		},
		Repo: "acme/app",
		Pr:   pr,
	}
}

// bundleVerb is one verb's bundle-resolution entry point.
type bundleVerb struct {
	run  func(ctx context.Context, pr string) *core.RefusalError
	name string
	// emptyIsNoInput marks a verb that reads an empty positional as "no
	// selection given": it refuses before resolving, under its own message.
	emptyIsNoInput bool
}

// bundleVerbs lists every verb that resolves a bundle by name, one entry per
// call site, so a new reader added without the shared guard fails the table.
func bundleVerbs() []bundleVerb {
	return []bundleVerb{
		{name: "adopt", run: func(ctx context.Context, pr string) *core.RefusalError {
			_, ref := adoptcore.Run(ctx, &adoptcore.Request{ReadRequest: bundleRead(pr), From: "twin"})
			return ref
		}},
		{name: "calibrate", run: func(ctx context.Context, pr string) *core.RefusalError {
			_, ref := calibratecore.Run(ctx, &calibratecore.Request{ReadRequest: bundleRead(pr)})
			return ref
		}},
		{name: "config explain", run: func(ctx context.Context, pr string) *core.RefusalError {
			_, ref := config.Run(ctx, &core.ConfigRequest{
				ReadRequest: bundleRead(pr), Flags: core.ConfigFlags{Explain: true},
			})
			return ref
		}},
		{name: "config preview", run: func(ctx context.Context, pr string) *core.RefusalError {
			_, ref := config.Run(ctx, &core.ConfigRequest{
				ReadRequest: bundleRead(pr), Flags: core.ConfigFlags{Preview: true},
			})
			return ref
		}},
		{name: "fetch", run: func(ctx context.Context, pr string) *core.RefusalError {
			_, err := fetchcore.Run(ctx, &core.FetchRequest{ReadRequest: bundleRead(pr)})
			return refusalOf(err)
		}},
		{name: "fix", run: func(ctx context.Context, pr string) *core.RefusalError {
			_, ref := fixcore.Run(ctx, &fixcore.Request{ReadRequest: bundleRead(pr)})
			return ref
		}},
		{name: "gate", run: func(ctx context.Context, pr string) *core.RefusalError {
			_, ref := reportcore.Run(ctx, &core.ReportRequest{
				ReadRequest: bundleRead(pr), Flags: reportcore.Flags{Check: "critical"},
			})
			return ref
		}},
		{
			name: "index blast-radius", emptyIsNoInput: true,
			run: func(ctx context.Context, pr string) *core.RefusalError {
				_, ref := indexcore.Run(ctx, &indexcore.Request{
					ReadRequest: bundleRead(pr), Query: indexcore.QueryBlastRadius,
				})
				return ref
			},
		},
		{name: "next", run: func(ctx context.Context, pr string) *core.RefusalError {
			resp, ref := reportcore.RunNext(ctx, &core.NextRequest{ReadRequest: bundleRead(pr), Status: "open"})
			if ref != nil {
				return ref
			}
			return resp.ReportRefusal
		}},
		{name: "next --coverage", run: func(_ context.Context, pr string) *core.RefusalError {
			_, ref := reportcore.RunNextCoverage(&core.NextRequest{ReadRequest: bundleRead(pr), Coverage: true})
			return ref
		}},
		{name: "post", run: func(ctx context.Context, pr string) *core.RefusalError {
			_, ref := postcore.Run(ctx, &core.PostRequest{
				ReadRequest: bundleRead(pr), Flags: core.PostFlags{Approve: "true"},
			})
			return ref
		}},
		{name: "ready", run: func(ctx context.Context, pr string) *core.RefusalError {
			_, ref := readycore.Run(ctx, &readycore.Request{ReadRequest: bundleRead(pr)})
			return ref
		}},
		{name: "record", run: func(ctx context.Context, pr string) *core.RefusalError {
			_, ref := recordcore.Run(ctx, &core.RecordRequest{ReadRequest: bundleRead(pr)})
			return ref
		}},
		{name: "report", run: func(ctx context.Context, pr string) *core.RefusalError {
			_, ref := reportcore.Run(ctx, &core.ReportRequest{ReadRequest: bundleRead(pr)})
			return ref
		}},
		{name: "run", run: func(ctx context.Context, pr string) *core.RefusalError {
			_, ref := runcore.Run(ctx, &core.RunRequest{ReadRequest: bundleRead(pr), Agent: "claude"})
			return ref
		}},
		{name: "show", run: func(ctx context.Context, pr string) *core.RefusalError {
			_, ref := reportcore.RunShow(ctx, &core.ShowRequest{ReadRequest: bundleRead(pr), Sig: "abc"})
			return ref
		}},
		{name: "threads", run: func(ctx context.Context, pr string) *core.RefusalError {
			_, ref := fetchcore.RunThreads(ctx, &fetchcore.ThreadsRequest{ReadRequest: bundleRead(pr)})
			return ref
		}},
		{name: "triage", run: func(ctx context.Context, pr string) *core.RefusalError {
			_, ref := triagecore.Run(ctx, &triagecore.Request{
				ReadRequest: bundleRead(pr), Sig: "abc", Status: "open", Rationale: "rationale text",
			})
			return ref
		}},
		{name: "verify", run: func(ctx context.Context, pr string) *core.RefusalError {
			_, ref := verifycore.Run(ctx, &core.VerifyRequest{ReadRequest: bundleRead(pr)})
			return ref
		}},
	}
}

// refusalOf maps a fetch error to the refusal it carries, so a case asserts on
// one shape whichever verb served the call.
func refusalOf(err error) *core.RefusalError {
	ref, ok := errors.AsType[*core.RefusalError](err)
	if ok {
		return ref
	}
	if err != nil {
		return &core.RefusalError{Code: "untyped", Message: err.Error()}
	}
	return nil
}

// hostileBundleNames are the spellings no verb may resolve: a parent step, one
// separator then its Windows twin, the dot, dot-dot, and the empty name.
var hostileBundleNames = []string{"../escaped", "a/b", `a\b`, ".", "..", ""}

// TestBundleVerbsRefuseHostileNames proves every bundle verb refuses a
// positional that is not one path element, and that the decoy bundles a
// bypassed guard would resolve are never read.
func TestBundleVerbsRefuseHostileNames(t *testing.T) {
	writeBundleNameFixture(t)
	for _, verb := range bundleVerbs() {
		for _, pr := range hostileBundleNames {
			t.Run(verb.name+"/"+pr, func(t *testing.T) { assertBundleVerbRefused(t, verb, pr) })
		}
	}
}

// assertBundleVerbRefused pins one verb's refusal of one hostile name.
func assertBundleVerbRefused(t *testing.T, verb bundleVerb, pr string) {
	t.Helper()
	ref := verb.run(context.Background(), pr)
	if ref == nil {
		t.Fatalf("%s with pr %q answered; want a bad-flags refusal", verb.name, pr)
	}
	if ref.Code != "bad-flags" {
		t.Fatalf("%s with pr %q refused %s: %s; want bad-flags", verb.name, pr, ref.Code, ref.Message)
	}
	if verb.emptyIsNoInput && pr == "" {
		return
	}
	if ref.Message != bundleNameRefusal {
		t.Fatalf("%s with pr %q refused %q; want %q", verb.name, pr, ref.Message, bundleNameRefusal)
	}
}

// TestBundleVerbsResolveValidNames proves the guard admits the two names a
// bundle really carries — a PR number and a fetch-local slug — and that a read
// verb then reads that bundle's store, not the bundle home.
func TestBundleVerbsResolveValidNames(t *testing.T) {
	writeBundleNameFixture(t)
	for _, pr := range []string{"7", "local-foo"} {
		t.Run(pr, func(t *testing.T) { assertBundleVerbResolved(t, pr) })
	}
}

// assertBundleVerbResolved pins that report reads the named bundle's store.
func assertBundleVerbResolved(t *testing.T, pr string) {
	t.Helper()
	resp, ref := reportcore.Run(context.Background(), &core.ReportRequest{ReadRequest: bundleRead(pr)})
	if ref != nil {
		t.Fatalf("report %s refused %s: %s", pr, ref.Code, ref.Message)
	}
	if resp == nil || resp.Data == nil || resp.Pr != pr {
		t.Fatalf("report %s answered %+v; want the bundle's own findings", pr, resp)
	}
}

// writeBundleNameFixture chdirs into a fresh root and seeds the decoy bundles a
// bypassed guard would resolve: one per hostile spelling that stays a real
// directory, plus the two names a valid positional must still reach.
func writeBundleNameFixture(t *testing.T) {
	t.Helper()
	t.Chdir(t.TempDir())
	for _, dir := range []string{
		".verdict/escaped", ".verdict/state/a/b", ".verdict/state/a\\b",
		".verdict/state/7", ".verdict/state/local-foo",
	} {
		seedBundleStore(t, dir)
	}
}

// seedBundleStore writes the empty findings store a bundle read needs.
func seedBundleStore(t *testing.T, dir string) {
	t.Helper()
	path := filepath.Join(filepath.FromSlash(dir), "findings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("[]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}
