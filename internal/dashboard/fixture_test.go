package dashboard

import (
	"context"
	"sync"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/query"
	"github.com/stainless-code/verdict/internal/jq"
)

// fixtureSource hands back hand-written layer views, so view tests reach the
// states real bundles make hard to produce: withheld totals, a refused row, a
// stranger's escape bytes in a field. calls counts every layer read, and the
// hooks let a test slow, fail, or panic one of them.
type fixtureSource struct {
	roots, bundles *query.View
	calls          *callLog
	changes        chan query.Change
	onBundles      func(ctx context.Context)
	onDetail       func()
	prompt         string // the fix prompt Copy assembles; empty falls back to triage
}

// callLog counts layer reads by method; the loop goroutine and one job
// goroutine never read the same field at once (jobs run one at a time and
// hand results over a channel), and -race checks it.
type callLog struct {
	counts map[string]int
	mu     sync.Mutex
}

func (c *callLog) add(name string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.counts == nil {
		c.counts = map[string]int{}
	}
	c.counts[name]++
}

func (c *callLog) count(name string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.counts[name]
}

func (f fixtureSource) Roots(context.Context) *query.View {
	f.calls.add("Roots")
	return f.roots
}

func (f fixtureSource) Bundles(ctx context.Context, _ string) (*query.View, error) {
	f.calls.add("Bundles")
	if f.onBundles != nil {
		f.onBundles(ctx)
	}
	return f.bundles, nil
}

func (f fixtureSource) Detail(_ context.Context, rootID, bundle string) (*query.View, error) {
	f.calls.add("Detail")
	if f.onDetail != nil {
		f.onDetail()
	}
	if bundle == "77" {
		return nil, &core.RefusalError{Code: "bundle-unreadable", Message: "findings.json is a FIFO"}
	}
	return detailFixture(rootID, bundle), nil
}

func (f fixtureSource) Diff(context.Context, string, string) (*query.View, error) {
	f.calls.add("Diff")
	return diffFixture(), nil
}

func (f fixtureSource) Compare(_ context.Context, _, _, baseline string) (*query.View, error) {
	f.calls.add("Compare")
	return compareFixture(baseline), nil
}

func (f fixtureSource) Copy(_ context.Context, _, bundle, sig string) (*query.View, error) {
	f.calls.add("Copy")
	return &query.View{Data: jq.NewObj("sig", sig, "prompt", f.prompt,
		"triage", "verdict triage acme/api "+bundle+" --sig "+sig+" --status addressed")}, nil
}

func (f fixtureSource) Cost(_ context.Context, by string) (*query.View, error) {
	f.calls.add("Cost")
	return costFixture(by), nil
}

func (f fixtureSource) Precision(_ context.Context, by string) (*query.View, error) {
	f.calls.add("Precision")
	return precisionFixture(by), nil
}

// Changes is the scripted stream, or an already-closed one (the --no-watch
// shape) when the test set none.
func (f fixtureSource) Changes(context.Context) <-chan query.Change {
	if f.changes != nil {
		return f.changes
	}
	closed := make(chan query.Change)
	close(closed)
	return closed
}

func (f fixtureSource) Invalidate() { f.calls.add("Invalidate") }

func bundleObj(bundle, repo, model, head string, open, deferred, addressed, terminal int) *jq.Obj {
	return jq.NewObj("root", "a1b2c3d4e5f6", "bundle", bundle, "dir", ".verdict/state/"+bundle,
		"repo", repo, "pr", nil, "model", model, "head", head,
		"open", open, "deferred", deferred, "addressed", addressed, "terminal", terminal)
}

// partialFixture is one root with four bundles, one of them refused, so the
// layer withheld the totals.
func partialFixture() fixtureSource {
	refused := jq.NewObj("root", "a1b2c3d4e5f6", "bundle", "77", "dir", ".verdict/state/77",
		"repo", "", "pr", 77, "model", nil, "head", nil,
		"open", nil, "deferred", nil, "addressed", nil, "terminal", nil)
	return fixtureSource{
		roots: &query.View{Data: jq.NewObj("roots", []any{
			jq.NewObj("id", "a1b2c3d4e5f6", "path", "/work/acme/api", "bundles", 4),
		})},
		bundles: &query.View{
			At:       "2026-11-25T10:00:00Z",
			Warnings: []string{"serve: bundle cap 500 reached under /work/acme/api; listing truncated"},
			Data: jq.NewObj(
				"root", "a1b2c3d4e5f6",
				"rows", []any{
					bundleObj("4232", "acme/api", "claude-opus-5-5", "9f8e7d6c5b4a39281706", 3, 1, 2, 0),
					refused,
					bundleObj("local-feature-branch-with-a-very-long-name", "acme/api\x1b[2Jforged", "gpt-5", "0123456789abcdef", 0, 0, 5, 1),
					bundleObj("4301", "acme/api", "", "", 12, 0, 0, 0),
				},
				"counts", nil,
				"partial", true,
				"errors", []any{jq.NewObj("root", "a1b2c3d4e5f6", "bundle", "77",
					"code", "bundle-unreadable", "message", "findings.json is a FIFO\x1b]52;c;cGF5bG9hZA==\x07")},
			),
		},
	}
}

// completeFixture is two roots whose bundles all composed, so the totals
// stand.
func completeFixture() fixtureSource {
	return fixtureSource{
		roots: &query.View{Data: jq.NewObj("roots", []any{
			jq.NewObj("id", "a1b2c3d4e5f6", "path", "/work/acme/api", "bundles", 1),
			jq.NewObj("id", "0f1e2d3c4b5a", "path", "/work/acme/日本語-web", "bundles", 1),
		})},
		bundles: &query.View{
			At: "2026-11-25T10:00:00Z",
			Data: jq.NewObj(
				"root", "",
				"rows", []any{
					bundleObj("4232", "acme/api", "claude-opus-5-5", "9f8e7d6c5b4a", 3, 1, 2, 0),
					bundleObj("88", "acme/日本語-web", "gpt-5", "abcdef012345", 0, 0, 4, 0),
				},
				"counts", jq.NewObj("bundles", 2, "open", 3, "deferred", 1, "addressed", 6, "terminal", 0),
				"partial", false,
				"errors", []any{},
			),
		},
	}
}

func mustLoad(src source) *snapshot {
	snap, err := load(context.Background(), src)
	if err != nil {
		panic(err)
	}
	return snap
}

func findingObj(sev, status, rigor, quote, locator, body, sig string) *jq.Obj {
	o := jq.NewObj("kind", "inline", "severity", sev, "status", status, "rigor", rigor,
		"locator", locator, "body", body, "sig", sig)
	if quote != "" {
		o.Set("verify", jq.NewObj("quote", quote))
	}
	return o
}

// detailFixture is one bundle's Detail view: three findings across the
// tiers and evidence axes, one body carrying a stranger's escape bytes.
func detailFixture(rootID, bundle string) *query.View {
	return &query.View{
		Repo: "acme/api", Pr: bundle,
		Data: jq.NewObj(
			"report", jq.NewObj(
				"head", jq.NewObj("fetched", "9f8e7d6c5b4a39281706"),
				// The report wire is built by reflection, so its counts are int64.
				"verdict", jq.NewObj("open", int64(2), "deferred", int64(0), "addressed", int64(1), "terminal", int64(0)),
				"items", []any{
					findingObj("critical", "open", "reproduced", "verified", "src/auth.go:42",
						"Token compared with == leaks timing.\nUse subtle.ConstantTimeCompare.", "c0"+bundle+rootID),
					findingObj("important", "addressed", "walked", "unverified", "src/db.go:7",
						"Query built by concatenation\x1b]52;c;eA==\x07 is injectable.", "c1"+bundle),
					findingObj("informational", "open", "said", "", "README.md:3", "Typo in the install line.", "c2"+bundle),
				},
			),
			"gate", jq.NewObj("passed", false, "dimensions", []any{
				jq.NewObj("id", "findings", "status", "fail", "detail", "1 open finding(s) at-or-above critical"),
				jq.NewObj("id", "spec", "status", "pass", "detail", "no violated spec check"),
				jq.NewObj("id", "evidence", "status", "pass", "detail", "every hunk receipted"),
			}),
		),
	}
}

func diffLineObj(kind, text string, oldNo, newNo int, sev string) *jq.Obj {
	o := jq.NewObj("kind", kind, "text", text)
	if oldNo > 0 {
		o.Set("old_no", oldNo)
	}
	if newNo > 0 {
		o.Set("new_no", newNo)
	}
	if sev != "" {
		o.Set("findings", []any{jq.NewObj("severity", sev)})
	}
	return o
}

// diffFixture is two files in the source and tests lanes, one change run
// pairing a deletion with two additions and an anchored critical finding.
func diffFixture() *query.View {
	return &query.View{Data: jq.NewObj(
		"lanes", []any{
			jq.NewObj("lane", "source", "file_count", 1, "additions", 2, "deletions", 1, "findings", 1),
			jq.NewObj("lane", "tests", "file_count", 1, "additions", 1, "deletions", 0, "findings", 0),
		},
		"files", []any{
			jq.NewObj("path", "src/auth.go", "lane", "source", "additions", 2, "deletions", 1, "hunks", []any{
				jq.NewObj("header", "@@ -40,3 +40,4 @@ func check", "lines", []any{
					diffLineObj("context", "func check(t string) bool {", 40, 40, ""),
					diffLineObj("del", "\treturn t == secret", 41, 0, ""),
					diffLineObj("add", "\tok := t == secret", 0, 41, "critical"),
					diffLineObj("add", "\treturn ok", 0, 42, ""),
					diffLineObj("context", "}", 42, 43, ""),
				}),
			}),
			jq.NewObj("path", "src/auth_test.go", "lane", "tests", "additions", 1, "deletions", 0, "hunks", []any{
				jq.NewObj("header", "@@ -1 +1,2 @@", "lines", []any{
					diffLineObj("context", "package src", 1, 1, ""),
					diffLineObj("add", "// TestCheck covers the token path.", 0, 2, ""),
				}),
			}),
		},
	)}
}

func compareRowObj(sev, status, path string, line int, claim string) *jq.Obj {
	return jq.NewObj("severity", sev, "status", status, "path", path, "line", line, "claim", claim)
}

func compareFixture(baseline string) *query.View {
	return &query.View{Data: jq.NewObj("compare", jq.NewObj(
		"baseline", jq.NewObj("name", baseline, "findings", 2),
		"new", []any{compareRowObj("critical", "open", "src/auth.go", 41, "Token compared with ==.")},
		"persisting", []any{compareRowObj("important", "addressed", "src/db.go", 7, "Query built by concatenation.")},
		"resolved", []any{compareRowObj("informational", "addressed", "README.md", 3, "Typo.")},
		"not_reviewed", []any{},
	))}
}

func costFixture(by string) *query.View {
	row := func(dim string, runs int, usd any) *jq.Obj {
		return jq.NewObj("dimension", dim, "runs", runs, "model_runs", runs, "input_tokens", 1200*runs,
			"output_tokens", 300*runs, "total_tokens", 1500*runs, "est_usd", usd)
	}
	return &query.View{
		Next: "write measured spend to the bundle's cost.json, then re-run",
		Data: jq.NewObj("cost", jq.NewObj("by", by,
			"rows", []any{row("acme/api", 3, 0.75), row("unattributed", 1, nil)},
			"totals", jq.NewObj("runs", 4, "model_runs", 4, "input_tokens", 4800, "output_tokens", 1200,
				"total_tokens", 6000, "est_usd", 0.75),
		)),
	}
}

func precisionFixture(by string) *query.View {
	return &query.View{Data: jq.NewObj("precision", jq.NewObj("by", by,
		"outcomes", []any{
			jq.NewObj("dimension", "security", "open", 1, "resolved_unfixed", 2, "unknown", 0, "fixed", nil),
			jq.NewObj("dimension", "logic", "open", 0, "resolved_unfixed", 1, "unknown", 1, "fixed", nil),
		},
		"totals", jq.NewObj("open", 1, "resolved_unfixed", 3, "unknown", 1, "fixed", nil),
		"as_of", "2026-11-25T09:00:00Z",
		"next", "run verdict calibrate <repo> <pr> to measure fixed",
	))}
}
