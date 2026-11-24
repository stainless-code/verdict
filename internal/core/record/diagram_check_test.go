package recordcore_test

import (
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
)

// diagramExtra is one diagram block over checkDiff's changed line, ready to
// append to checkReview's tail.
const diagramExtra = `,"diagram":{"type":"flow","nodes":[{"id":"a","label":"Entry",` +
	`"evidence":{"path":"notes.txt","line":2,"quote":"new line"}}]}`

// TestCheckReviewDiagramWarnings pins every diagram shape defect as the one
// warning code, at the exact JSONPath, and proves none of them is an error: a
// diagram is decoration, so a defect in it never refuses the review.
func TestCheckReviewDiagramWarnings(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		extra string
		path  string
	}{
		{
			name:  "out-of-scope type",
			extra: `,"diagram":{"type":"gantt","nodes":[]}`,
			path:  "$.diagram.type",
		},
		{
			name:  "type is not a string",
			extra: `,"diagram":{"type":7,"nodes":[]}`,
			path:  "$.diagram.type",
		},
		{
			name:  "nodes is not an array",
			extra: `,"diagram":{"type":"flow","nodes":{}}`,
			path:  "$.diagram.nodes",
		},
		{
			name:  "node is not an object",
			extra: `,"diagram":{"type":"flow","nodes":["a"]}`,
			path:  "$.diagram.nodes[0]",
		},
		{
			name:  "node misses its evidence",
			extra: `,"diagram":{"type":"flow","nodes":[{"id":"a","label":"A"}]}`,
			path:  "$.diagram.nodes[0].evidence",
		},
		{
			name: "anchor misses the quote",
			extra: `,"diagram":{"type":"flow","nodes":[{"id":"a","label":"A",` +
				`"evidence":{"path":"notes.txt","line":2}}]}`,
			path: "$.diagram.nodes[0].evidence.quote",
		},
		{
			// evidence is the spec_checks shape exactly: a finding's side and
			// start_line are not part of it.
			name: "anchor carries a finding-only side",
			extra: `,"diagram":{"type":"flow","nodes":[{"id":"a","label":"A",` +
				`"evidence":{"path":"notes.txt","line":2,"quote":"new line","side":"RIGHT"}}]}`,
			path: "$.diagram.nodes[0].evidence.side",
		},
		{
			name: "anchor carries a finding-only start_line",
			extra: `,"diagram":{"type":"flow","nodes":[{"id":"a","label":"A",` +
				`"evidence":{"path":"notes.txt","line":2,"start_line":1,"quote":"new line"}}]}`,
			path: "$.diagram.nodes[0].evidence.start_line",
		},
		{
			name: "anchor line is not an integer",
			extra: `,"diagram":{"type":"flow","nodes":[{"id":"a","label":"A",` +
				`"evidence":{"path":"notes.txt","line":"2","quote":"new line"}}]}`,
			path: "$.diagram.nodes[0].evidence.line",
		},
		{
			name:  "diagram is not an object",
			extra: `,"diagram":"sequenceDiagram"`,
			path:  "$.diagram",
		},
		{
			name: "node label past its ceiling",
			extra: `,"diagram":{"type":"flow","nodes":[{"id":"a","label":"` + strings.Repeat("x", 121) + `",` +
				`"evidence":{"path":"notes.txt","line":2,"quote":"new line"}}]}`,
			path: "$.diagram.nodes[0].label",
		},
		{
			name:  "unknown diagram key",
			extra: `,"diagram":{"type":"flow","motto":"ship it","nodes":[]}`,
			path:  "$.diagram.motto",
		},
		{
			name:  "missing type",
			extra: `,"diagram":{"nodes":[]}`,
			path:  "$.diagram.type",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertDiagramWarningOnly(t, recordcore.CheckReview([]byte(checkReview(tc.extra)), checkDiff), tc.path)
		})
	}
}

// TestCheckReviewAcceptsAWellFormedDiagram proves a complete diagram draws no
// problem at all, so the warning code tracks the defect and not the feature.
func TestCheckReviewAcceptsAWellFormedDiagram(t *testing.T) {
	t.Parallel()
	res := recordcore.CheckReview([]byte(checkReview(diagramExtra)), checkDiff)
	for _, p := range res.Problems {
		if p.Code == core.ProblemCodeDiagramInvalid {
			t.Fatalf("well-formed diagram reported %+v", p)
		}
	}
}

// TestCheckReviewDiagramNullIsAbsent proves the normalizer drops an explicit
// null, the way it does for every other optional root key.
func TestCheckReviewDiagramNullIsAbsent(t *testing.T) {
	t.Parallel()
	raw := []byte(checkReview(`,"diagram":null`))
	normalized, repairs := recordcore.NormalizeReview(raw)
	if len(repairs) != 1 || repairs[0].Code != core.ProblemCodeNormalizedTrivia {
		t.Fatalf("repairs = %+v, want one normalized-trivia repair", repairs)
	}
	if strings.Contains(string(normalized), `"diagram"`) {
		t.Fatalf("normalized review still carries the null diagram:\n%s", normalized)
	}
	res := recordcore.CheckReview(normalized, checkDiff)
	for _, p := range res.Problems {
		if p.Code == core.ProblemCodeDiagramInvalid {
			t.Fatalf("dropped null diagram still reported %+v", p)
		}
	}
}

// assertDiagramWarningOnly proves the review carries diagram-invalid at path,
// that every diagram problem is a warning, and that no shape error rode along.
func assertDiagramWarningOnly(t *testing.T, res recordcore.CheckResult, path string) {
	t.Helper()
	if !hasProblem(res, path, core.ProblemCodeDiagramInvalid) {
		t.Fatalf("problems = %+v, want diagram-invalid at %s", res.Problems, path)
	}
	for _, p := range res.Problems {
		if p.Code == core.ProblemCodeDiagramInvalid && p.Level != core.ProblemLevelWarning {
			t.Errorf("%s level = %q, want warning", p.Path, p.Level)
		}
	}
	if got := res.ShapeErrors(); got != 0 {
		t.Errorf("ShapeErrors() = %d, want 0: a diagram defect is a warning", got)
	}
}
