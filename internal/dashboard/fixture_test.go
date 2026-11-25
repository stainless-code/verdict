package dashboard

import (
	"context"

	"github.com/stainless-code/verdict/internal/core/query"
	"github.com/stainless-code/verdict/internal/jq"
)

// fixtureSource hands back hand-written layer views, so view tests reach the
// states real bundles make hard to produce: withheld totals, a refused row, a
// stranger's escape bytes in a field.
type fixtureSource struct {
	roots, bundles *query.View
}

func (f fixtureSource) Roots(context.Context) *query.View { return f.roots }

func (f fixtureSource) Bundles(context.Context, string) (*query.View, error) { return f.bundles, nil }

func row(bundle, repo, model, head string, open, deferred, addressed, terminal int) *jq.Obj {
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
					row("4232", "acme/api", "claude-opus-5-5", "9f8e7d6c5b4a39281706", 3, 1, 2, 0),
					refused,
					row("local-feature-branch-with-a-very-long-name", "acme/api\x1b[2Jforged", "gpt-5", "0123456789abcdef", 0, 0, 5, 1),
					row("4301", "acme/api", "", "", 12, 0, 0, 0),
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
					row("4232", "acme/api", "claude-opus-5-5", "9f8e7d6c5b4a", 3, 1, 2, 0),
					row("88", "acme/日本語-web", "gpt-5", "abcdef012345", 0, 0, 4, 0),
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
