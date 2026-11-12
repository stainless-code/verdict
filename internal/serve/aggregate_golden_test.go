package serve

import (
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/query"
	"github.com/stainless-code/verdict/internal/golden"
	"github.com/stainless-code/verdict/internal/jq"
)

// TestAggregateGoldens pins composed cost and precision responses.
func TestAggregateGoldens(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFixtureAt(t, root, map[string]string{
		".verdict/state/1234/PR.json": `{"url":"https://github.com/acme/app/pull/1234","number":1234}`,
		".verdict/state/1234/post.json": `{"review":{"id":7},"head":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",` +
			`"posted_at":"2026-11-12T12:00:00Z"}`,
		".verdict/state/1234/cost.json": `[{"models":[{"name":"alpha","input_tokens":10,"output_tokens":5}],"est_usd":0.25}]`,
		".verdict/state/1234/findings.json": `[{"kind":"inline","path":"src/main.go","side":"RIGHT","line":1,` +
			`"body":"Usable body here with enough substance.","suggestion":null,"severity":"critical",` +
			`"category":"security","sig":"f111111111111111111111111111111111111111",` +
			`"status":"open","rigor":"walked"}]`,
		".verdict/state/1234/threads.json": `{"schema_version":"1","provider":"github","pr":1234,"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",` +
			`"fetched_at":"2026-11-13T12:00:00Z","truncated":false,"threads":[{"id":"T_1","path":"src/main.go",` +
			`"line":1,"start_line":null,"side":"RIGHT","resolved":true,"outdated":false,"url":null,` +
			`"author":"reviewer","author_kind":"human","finding_sig":"f111111111111111111111111111111111111111",` +
			`"kind":"review-comment","comments":[{"id":"C_1","author":"reviewer","author_kind":"human",` +
			`"body":"Usable body here with enough substance.","truncated":false,"created_at":"2026-11-13T12:00:00Z"}]}]}`,
	})
	layer := testLayer(root, nil)
	rootID := query.RootID(mustAbs(t, root))
	for _, tc := range []struct {
		view func() (*query.View, error)
		name string
	}{
		{name: "cost-repo", view: func() (*query.View, error) { return layer.Cost(t.Context(), "repo") }},
		{name: "precision-category", view: func() (*query.View, error) { return layer.Precision(t.Context(), "category") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			view, err := tc.view()
			if err != nil {
				t.Fatal(err)
			}
			body := jq.PrettyOrdered(core.RenderEnvelope("serve", view.Repo, view.Pr, view.At,
				view.Next, view.Warnings, view.Data, nil))
			body = strings.ReplaceAll(body, rootID, "root-id")
			golden.Check(t, tc.name, strings.TrimSuffix(body, "\n")+"\n")
		})
	}
}
