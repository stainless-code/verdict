package trigger

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

func predicate() *core.ConfigTrigger {
	draft := true
	return &core.ConfigTrigger{
		Authors:      []string{"ana", "bo"},
		PathsInclude: []string{"src/**", "go.mod"},
		PathsExclude: []string{"**/generated/**"},
		Labels:       []string{"security", "deps"},
		TitleMatches: "^fix: (retry|auth)",
		BaseBranches: []string{"main", "release/*"},
		Draft:        &draft,
	}
}

func facts() Facts {
	return Facts{
		Author:     "ana",
		Paths:      []string{"src/retry.go", "docs/README.md"},
		Labels:     []string{"deps"},
		Title:      "fix: retry count",
		BaseBranch: "main",
		Draft:      true,
	}
}

func TestEvaluate(t *testing.T) {
	for _, tc := range []struct {
		name       string
		mutate     func(*core.ConfigTrigger, *Facts)
		wantRun    bool
		wantReason string
	}{
		{name: "all match", wantRun: true, wantReason: "matched"},
		{name: "no predicates", mutate: func(p *core.ConfigTrigger, _ *Facts) { *p = core.ConfigTrigger{} }, wantRun: true, wantReason: "no-trigger"},
		{
			name: "author OR second value matches",
			mutate: func(p *core.ConfigTrigger, f *Facts) {
				f.Author = "bo"
				p.Authors = []string{"ana", "bo"}
			},
			wantRun: true, wantReason: "matched",
		},
		{name: "author fails", mutate: func(_ *core.ConfigTrigger, f *Facts) { f.Author = "mallory" }, wantReason: "authors"},
		{name: "path include fails", mutate: func(_ *core.ConfigTrigger, f *Facts) { f.Paths = []string{"docs/README.md"} }, wantReason: "paths_include"},
		{
			name: "path exclude wins over include",
			mutate: func(_ *core.ConfigTrigger, f *Facts) {
				f.Paths = []string{"src/generated/retry.go"}
			},
			wantReason: "paths_exclude",
		},
		{name: "labels fail", mutate: func(_ *core.ConfigTrigger, f *Facts) { f.Labels = []string{"docs"} }, wantReason: "labels"},
		{name: "title fails", mutate: func(_ *core.ConfigTrigger, f *Facts) { f.Title = "chore: retry" }, wantReason: "title_matches"},
		{name: "base branch fails", mutate: func(_ *core.ConfigTrigger, f *Facts) { f.BaseBranch = "dev" }, wantReason: "base_branches"},
		{name: "base branch glob matches", mutate: func(_ *core.ConfigTrigger, f *Facts) { f.BaseBranch = "release/1.2" }, wantRun: true, wantReason: "matched"},
		{name: "draft fails", mutate: func(_ *core.ConfigTrigger, f *Facts) { f.Draft = false }, wantReason: "draft"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, f := predicate(), facts()
			if tc.mutate != nil {
				tc.mutate(p, &f)
			}
			run, reason := Evaluate(f, p)
			if run != tc.wantRun || reason != tc.wantReason {
				t.Errorf("Evaluate = (%v, %q), want (%v, %q)", run, reason, tc.wantRun, tc.wantReason)
			}
		})
	}
	run, reason := Evaluate(facts(), nil)
	if !run || reason != "no-trigger" {
		t.Errorf("Evaluate with nil predicates = (%v, %q), want (true, no-trigger)", run, reason)
	}
}

func TestReadFacts(t *testing.T) {
	dir := t.TempDir()
	raw := `{"provider":"github","title":"fix: retry","baseRefName":"main","isDraft":true,
		"author":{"login":"ana"},"labels":[{"name":"deps"},{"name":"security"}],
		"files":[{"path":"src/retry.go"},{"path":"docs/README.md"}]}`
	if err := os.WriteFile(filepath.Join(dir, "PR.json"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadFacts(dir)
	if err != nil {
		t.Fatalf("ReadFacts: %v", err)
	}
	want := Facts{
		Author:     "ana",
		Paths:      []string{"src/retry.go", "docs/README.md"},
		Labels:     []string{"deps", "security"},
		Title:      "fix: retry",
		BaseBranch: "main",
		Draft:      true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ReadFacts = %#v, want %#v", got, want)
	}
}
