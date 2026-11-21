package budget

import (
	"slices"
	"testing"
)

// defaultFiles and defaultBytes are the built-in walkthrough thresholds, the
// numbers an overrides set without a threshold fragment resolves.
const (
	defaultFiles = 8
	defaultBytes = 24576
)

// classifyCase is one rule invocation with every expectation written as a
// literal, so a defect cannot reproduce on both sides of the comparison. The
// field order is the reading order, not the GC's.
type classifyCase struct { //nolint:govet // a case reads name, input, then expectations
	name        string
	input       Inputs
	wantTier    string
	wantReasons []string
	wantFinders int
	wantCross   string
}

// source and docs rows, named once so each case reads as the tree it grades.
func src(path string) File  { return File{Path: path, Role: "source"} }
func docs(path string) File { return File{Path: path, Role: "docs"} }

// defaultInputs is the smallest well-formed input: built-in thresholds, the
// built-in globs, and no rows.
func defaultInputs(files []File, count, bytes int) Inputs {
	return Inputs{
		Files: files, FileCount: count, DiffBytes: bytes,
		MaxFiles: defaultFiles, MaxBytes: defaultBytes, Sensitive: BuiltinSensitive(),
	}
}

func TestClassifyRules(t *testing.T) {
	cases := []classifyCase{
		{
			name:        "R5 a tiny source diff is the light pass",
			input:       defaultInputs([]File{src("main.go")}, 1, 900),
			wantTier:    TierLight,
			wantReasons: []string{"tiny-diff: 1/900"},
			wantFinders: 1,
			wantCross:   CrossOff,
		},
		{
			name:        "R5 boundary: exactly three files and 4096 bytes is still tiny",
			input:       defaultInputs([]File{src("a.go"), src("b.go"), src("c.go")}, 3, 4096),
			wantTier:    TierLight,
			wantReasons: []string{"tiny-diff: 3/4096"},
			wantFinders: 1,
			wantCross:   CrossOff,
		},
		{
			name:        "R5 boundary: one file past the tiny count is standard",
			input:       defaultInputs([]File{src("a.go"), src("b.go"), src("c.go"), src("d.go")}, 4, 4096),
			wantTier:    TierStandard,
			wantReasons: []string{ReasonStandardSize},
			wantFinders: 2,
			wantCross:   CrossOpposite,
		},
		{
			name:        "R5 boundary: one byte past the tiny budget is standard",
			input:       defaultInputs([]File{src("main.go")}, 1, 4097),
			wantTier:    TierStandard,
			wantReasons: []string{ReasonStandardSize},
			wantFinders: 2,
			wantCross:   CrossOpposite,
		},
		{
			name:        "R5 an unmeasured diff (no full.diff) is standard, never tiny",
			input:       defaultInputs([]File{src("main.go")}, 1, 0),
			wantTier:    TierStandard,
			wantReasons: []string{ReasonStandardSize},
			wantFinders: 2,
			wantCross:   CrossOpposite,
		},
		{
			name: "R1 a rename out of a sensitive tree still selects deep",
			input: defaultInputs([]File{
				{Path: "lib/token.go", PreviousPath: "auth/token.go", Role: "source"},
			}, 1, 120),
			wantTier:    TierDeep,
			wantReasons: []string{"sensitive-path: **/auth/**"},
			wantFinders: 2,
			wantCross:   CrossOpposite,
		},
		{
			name:        "R2 docs-only under the threshold is the light pass",
			input:       defaultInputs([]File{docs("docs/a.md"), docs("docs/b.md")}, 2, 300),
			wantTier:    TierLight,
			wantReasons: []string{ReasonDocsOnly},
			wantFinders: 1,
			wantCross:   CrossOff,
		},
		{
			name: "R2 tests and generated output count as cheap too",
			input: defaultInputs([]File{
				{Path: "app/main_test.go", Role: "test"},
				{Path: "package-lock.json", Role: "generated"},
			}, 2, 300),
			wantTier:    TierLight,
			wantReasons: []string{ReasonDocsOnly},
			wantFinders: 1,
			wantCross:   CrossOff,
		},
		{
			name:        "R3 docs-only over the file threshold stays standard, never deep",
			input:       defaultInputs(docsRows(9), 9, 900),
			wantTier:    TierStandard,
			wantReasons: []string{ReasonDocsOnly, "large-docs-only: 8/24576"},
			wantFinders: 2,
			wantCross:   CrossOpposite,
		},
		{
			name:        "R3 docs-only over the byte threshold stays standard",
			input:       defaultInputs([]File{docs("CHANGELOG.md"), docs("docs/a.md")}, 2, 24577),
			wantTier:    TierStandard,
			wantReasons: []string{ReasonDocsOnly, "large-docs-only: 8/24576"},
			wantFinders: 2,
			wantCross:   CrossOpposite,
		},
		{
			name:        "R4 boundary: exactly eight files and 24576 bytes is not over",
			input:       defaultInputs(srcRows(8, "a.go"), 8, 24576),
			wantTier:    TierStandard,
			wantReasons: []string{ReasonStandardSize},
			wantFinders: 2,
			wantCross:   CrossOpposite,
		},
		{
			name:        "R4 one file over the threshold is deep",
			input:       defaultInputs(srcRows(9, "a.go"), 9, 900),
			wantTier:    TierDeep,
			wantReasons: []string{"walkthrough-threshold: 8/24576"},
			wantFinders: 2,
			wantCross:   CrossOpposite,
		},
		{
			name:        "R4 one byte over the threshold is deep",
			input:       defaultInputs([]File{src("main.go")}, 1, 24577),
			wantTier:    TierDeep,
			wantReasons: []string{"walkthrough-threshold: 8/24576"},
			wantFinders: 2,
			wantCross:   CrossOpposite,
		},
		{
			name: "R4 names a custom override's numbers, not the built-in ones",
			input: Inputs{
				Files: srcRows(3, "old.go"), FileCount: 3, DiffBytes: 4000,
				MaxFiles: 2, MaxBytes: 2000, Sensitive: BuiltinSensitive(),
			},
			wantTier:    TierDeep,
			wantReasons: []string{"walkthrough-threshold: 2/2000"},
			wantFinders: 2,
			wantCross:   CrossOpposite,
		},
		{
			name: "R1 a sensitive glob outranks the tiny-diff rule",
			input: defaultInputs([]File{
				{Path: "migrations/0001_init.sql", Role: "source"},
			}, 1, 120),
			wantTier:    TierDeep,
			wantReasons: []string{"sensitive-path: **/migrations/**"},
			wantFinders: 2,
			wantCross:   CrossOpposite,
		},
		{
			name: "R1 a sensitive glob outranks the docs-only rule",
			input: defaultInputs([]File{
				{Path: "internal/auth/README.md", Role: "docs"},
			}, 1, 80),
			wantTier:    TierDeep,
			wantReasons: []string{"sensitive-path: **/auth/**"},
			wantFinders: 2,
			wantCross:   CrossOpposite,
		},
		{
			name: "R1 a repository glob extends the built-ins",
			input: Inputs{
				Files: []File{src("app/payments/charge.go")}, FileCount: 1, DiffBytes: 200,
				MaxFiles: defaultFiles, MaxBytes: defaultBytes,
				Sensitive: append(BuiltinSensitive(), "**/payments/**"),
			},
			wantTier:    TierDeep,
			wantReasons: []string{"sensitive-path: **/payments/**"},
			wantFinders: 2,
			wantCross:   CrossOpposite,
		},
		{
			name: "R1 the first glob in configuration order names the reason",
			input: defaultInputs([]File{
				{Path: "internal/auth/migrations/0002.sql", Role: "source"},
			}, 1, 90),
			wantTier:    TierDeep,
			wantReasons: []string{"sensitive-path: **/auth/**"},
			wantFinders: 2,
			wantCross:   CrossOpposite,
		},
		{
			name: "R1 paths are scanned in sorted order, not bundle order",
			input: defaultInputs([]File{
				{Path: "src/billing/invoice.go", Role: "source"},
				{Path: "internal/auth/session.go", Role: "source"},
			}, 2, 300),
			wantTier:    TierDeep,
			wantReasons: []string{"sensitive-path: **/auth/**"},
			wantFinders: 2,
			wantCross:   CrossOpposite,
		},
		{
			name:        "R1 no configured globs never matches",
			input:       Inputs{Files: []File{src("internal/auth/session.go")}, FileCount: 1, DiffBytes: 900, MaxFiles: defaultFiles, MaxBytes: defaultBytes},
			wantTier:    TierLight,
			wantReasons: []string{"tiny-diff: 1/900"},
			wantFinders: 1,
			wantCross:   CrossOff,
		},
		{
			name:        "R6 empty rows read standard: missing evidence never drops risk",
			input:       defaultInputs(nil, 0, 0),
			wantTier:    TierStandard,
			wantReasons: []string{ReasonStandardSize},
			wantFinders: 2,
			wantCross:   CrossOpposite,
		},
		{
			name:        "R6 a count without rows never reads docs-only",
			input:       defaultInputs(nil, 5, 100),
			wantTier:    TierStandard,
			wantReasons: []string{ReasonStandardSize},
			wantFinders: 2,
			wantCross:   CrossOpposite,
		},
		{
			name:        "R6 a config role is not a cheap role",
			input:       defaultInputs([]File{{Path: ".github/dependabot.yml", Role: "config"}}, 1, 200),
			wantTier:    TierLight,
			wantReasons: []string{"tiny-diff: 1/200"},
			wantFinders: 1,
			wantCross:   CrossOff,
		},
		{
			name:        "R6 one source row in a docs tree defeats the cheap rule",
			input:       defaultInputs([]File{docs("docs/a.md"), docs("docs/b.md"), docs("docs/c.md"), src("main.go")}, 4, 500),
			wantTier:    TierStandard,
			wantReasons: []string{ReasonStandardSize},
			wantFinders: 2,
			wantCross:   CrossOpposite,
		},
		{
			name: "R6 a role override beats the docs path heuristic",
			input: defaultInputs([]File{
				{Path: "docs/guide.md", Role: "source"},
			}, 2, 30000),
			wantTier:    TierDeep,
			wantReasons: []string{"walkthrough-threshold: 8/24576"},
			wantFinders: 2,
			wantCross:   CrossOpposite,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { assertClassifyCase(t, &tc) })
	}
}

// assertClassifyCase grades one case. The cross-check mode is read off the
// reported finder count, so a dispatch the record would publish is the one
// graded here.
func assertClassifyCase(t *testing.T, tc *classifyCase) {
	t.Helper()
	in := tc.input
	got := Classify(&in)
	if got.Tier != tc.wantTier {
		t.Errorf("tier = %q; want %q", got.Tier, tc.wantTier)
	}
	if !slices.Equal(got.Reasons, tc.wantReasons) {
		t.Errorf("reasons = %q; want %q", got.Reasons, tc.wantReasons)
	}
	if got.Finders != tc.wantFinders {
		t.Errorf("finders = %d; want %d", got.Finders, tc.wantFinders)
	}
	if cross := CrossForCount(got.Finders); cross != tc.wantCross {
		t.Errorf("cross = %q; want %q", cross, tc.wantCross)
	}
}

// TestClassifyReasonsAreNeverNil proves the wire shape never renders null for
// reasons: every branch, including an explicitly requested tier, sets a slice.
func TestClassifyReasonsAreNeverNil(t *testing.T) {
	for _, in := range []Inputs{
		defaultInputs(nil, 0, 0),
		defaultInputs([]File{docs("a.md")}, 1, 10),
		defaultInputs([]File{src("main.go")}, 1, 10),
		defaultInputs(srcRows(20, "a.go"), 20, 999),
	} {
		if got := Classify(&in); got.Reasons == nil {
			t.Fatalf("Classify(%+v) returned nil reasons", in)
		}
	}
	for _, tier := range []string{TierLight, TierStandard, TierDeep} {
		if got := ForTier(tier); got.Reasons == nil {
			t.Fatalf("ForTier(%q) returned nil reasons", tier)
		}
	}
}

// TestDispatchMapsThreeNamesOntoTwoShapes pins the honest table: light is one
// finder with no cross-check, and both standard and deep are two finders with
// opposite-finder cross-checks.
func TestDispatchMapsThreeNamesOntoTwoShapes(t *testing.T) {
	for _, tc := range []struct {
		wantCross   string
		tier        string
		wantFinders int
	}{
		{"off", TierLight, 1},
		{"opposite-finder", TierStandard, 2},
		{"opposite-finder", TierDeep, 2},
	} {
		t.Run(tc.tier, func(t *testing.T) { assertDispatch(t, tc.tier, tc.wantFinders, tc.wantCross) })
	}
}

// assertDispatch grades one tier's launch shape and the decision built from it.
func assertDispatch(t *testing.T, tier string, wantFinders int, wantCross string) {
	t.Helper()
	finders, cross := Dispatch(tier)
	if finders != wantFinders || cross != wantCross {
		t.Fatalf("Dispatch(%q) = %d/%q; want %d/%q", tier, finders, cross, wantFinders, wantCross)
	}
	got := ForTier(tier)
	if got.Tier != tier || got.Finders != wantFinders || len(got.Reasons) != 0 {
		t.Fatalf("ForTier(%q) = %+v", tier, got)
	}
}

func TestValidRequest(t *testing.T) {
	for _, raw := range []string{"auto", "light", "standard", "deep"} {
		if !ValidRequest(raw) {
			t.Errorf("ValidRequest(%q) = false", raw)
		}
	}
	for _, raw := range []string{"", "fast", "Auto", "cheap", "3", " deep"} {
		if ValidRequest(raw) {
			t.Errorf("ValidRequest(%q) = true", raw)
		}
	}
	if got := Vocabulary(); got != "auto|light|standard|deep" {
		t.Errorf("Vocabulary() = %q", got)
	}
}

// srcRows builds n source rows with sequential names, so a size case reads as
// a tree instead of a count.
func srcRows(n int, suffix string) []File {
	rows := make([]File, 0, n)
	for i := range n {
		rows = append(rows, File{Path: string(rune('a'+i%26)) + string(rune('a'+i/26)) + "/" + suffix, Role: "source"})
	}
	return rows
}

// docsRows builds n documentation rows.
func docsRows(n int) []File {
	rows := make([]File, 0, n)
	for i := range n {
		rows = append(rows, File{Path: "docs/" + string(rune('a'+i%26)) + string(rune('a'+i/26)) + ".md", Role: "docs"})
	}
	return rows
}
