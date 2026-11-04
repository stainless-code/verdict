package brief_test

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/brief"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/hunks"
	"github.com/stainless-code/verdict/internal/jq"
)

// rows is the pinned hunk snapshot both sides of one changed file use.
func rows() []hunks.Hunk {
	return []hunks.Hunk{
		{File: "notes.txt", Side: "OLD", Start: 1, End: 7},
		{File: "notes.txt", Side: "NEW", Start: 1, End: 7},
		{File: "notes.txt", Side: "NEW", Start: 20, End: 26},
	}
}

// keep is a brief outcome long enough to pass the length gate.
const keep = "Reject duplicate deliveries before anything is enqueued."

// obj decodes one ordered brief object for a test.
func obj(t *testing.T, raw string) any {
	t.Helper()
	doc, err := jq.DecodeOrdered([]byte(raw))
	if err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return doc
}

// objOf decodes one ordered record object for a test.
func objOf(t *testing.T, raw string) *jq.Obj {
	t.Helper()
	m, ok := obj(t, raw).(*jq.Obj)
	if !ok {
		t.Fatalf("%s decoded to a non-object", raw)
	}
	return m
}

// dropCase is one documented shape failure: wantBrief says whether the brief
// survives, and wantSites (nil to skip) pins the kept sites.
type dropCase struct {
	name      string
	raw       string
	wantSites []string
	wantWarns []string
	wantBrief bool
}

var dropCases = []dropCase{
	{
		name:      "not an object",
		raw:       `"` + keep + `"`,
		wantWarns: []string{"brief is not an object — dropped"},
	},
	{
		name:      "missing outcome",
		raw:       `{"sites":[{"path":"notes.txt"}]}`,
		wantWarns: []string{"brief outcome is missing — dropped the brief"},
	},
	{
		name:      "short outcome",
		raw:       `{"outcome":"too short"}`,
		wantWarns: []string{"brief outcome is 9 chars (want 10-500) — dropped the brief"},
	},
	{
		name:      "whole file site",
		raw:       `{"sites":[{"path":"b/notes.txt"}],"outcome":"` + keep + `"}`,
		wantBrief: true,
		wantSites: []string{"b/notes.txt"},
	},
	{
		name: "line inside a hunk",
		raw: `{"sites":[{"path":"notes.txt","line":22},{"path":"notes.txt","line":3}],` +
			`"outcome":"` + keep + `"}`,
		wantBrief: true,
		wantSites: []string{"notes.txt:22", "notes.txt:3"},
	},
	{
		name: "outside sites drop",
		raw: `{"sites":[{"path":"unknown.txt"},{"path":"notes.txt","line":99},` +
			`{"path":"notes.txt","line":"two"},{"path":true},{}],"outcome":"` + keep + `"}`,
		wantBrief: true,
		wantSites: []string{},
		wantWarns: []string{
			"brief site unknown.txt is not a changed file with hunks — dropped",
			"brief site notes.txt:99 is not a changed file with hunks — dropped",
			"brief site notes.txt has a non-integer line — dropped",
			"brief site has no path — dropped",
			"brief site has no path — dropped",
		},
	},
	{
		name: "duplicate sites dedupe",
		raw: `{"sites":[{"path":"notes.txt","line":3},{"path":"notes.txt","line":3}],` +
			`"outcome":"` + keep + `"}`,
		wantBrief: true,
		wantSites: []string{"notes.txt:3"},
	},
	{
		name:      "empty approach reads absent",
		raw:       `{"outcome":"` + keep + `","approach":""}`,
		wantBrief: true,
	},
	{
		name:      "short approach drops",
		raw:       `{"outcome":"` + keep + `","approach":"tiny"}`,
		wantBrief: true,
		wantWarns: []string{"brief approach is 4 chars (want 10-1000) — dropped"},
	},
	{
		name:      "non-array questions drop",
		raw:       `{"outcome":"` + keep + `","questions":"why"}`,
		wantBrief: true,
		wantWarns: []string{"brief questions is not an array — dropped"},
	},
	{
		name:      "non-array sites drop",
		raw:       `{"outcome":"` + keep + `","sites":"notes.txt"}`,
		wantBrief: true,
		wantWarns: []string{"brief sites is not an array — dropped"},
	},
	{
		name:      "unknown keys warn",
		raw:       `{"outcome":"` + keep + `","sites_typo":[],"zzz":1}`,
		wantBrief: true,
		wantWarns: []string{
			"brief.sites_typo is not a known brief key — dropped",
			"brief.zzz is not a known brief key — dropped",
		},
	},
}

// TestValidateDrops pins every documented shape failure: the brief survives
// when the outcome does, and each bad part warns instead of refusing.
func TestValidateDrops(t *testing.T) {
	for i := range dropCases {
		tc := &dropCases[i]
		t.Run(tc.name, func(t *testing.T) {
			checkDrop(t, tc)
		})
	}
}

// checkDrop pins one case: survival, warns, outcome, and kept sites.
func checkDrop(t *testing.T, tc *dropCase) {
	t.Helper()
	got, warns := brief.Validate(obj(t, tc.raw), rows())
	if (got != nil) != tc.wantBrief {
		t.Fatalf("brief = %+v, want present=%t (warns %v)", got, tc.wantBrief, warns)
	}
	if !slices.Equal(warns, tc.wantWarns) {
		t.Errorf("warns = %v, want %v", warns, tc.wantWarns)
	}
	if got == nil {
		return
	}
	if got.Outcome != keep {
		t.Errorf("outcome = %q", got.Outcome)
	}
	if tc.wantSites != nil && !slices.Equal(siteList(got.Sites), tc.wantSites) {
		t.Errorf("sites = %v, want %v", siteList(got.Sites), tc.wantSites)
	}
}

// siteList renders sites as path[:line] for compact assertions.
func siteList(sites []core.BriefSite) []string {
	out := make([]string, 0, len(sites))
	for _, site := range sites {
		if site.Line == nil {
			out = append(out, site.Path)
			continue
		}
		out = append(out, fmt.Sprintf("%s:%d", site.Path, *site.Line))
	}
	return out
}

// capSites builds sites over n distinct changed files, each naming line 1.
func capSites(n int) (string, []hunks.Hunk) {
	sites := make([]string, 0, n)
	capRows := make([]hunks.Hunk, 0, n)
	for i := range n {
		name := fmt.Sprintf("f%02d.txt", i)
		capRows = append(capRows, hunks.Hunk{File: name, Side: "NEW", Start: 1, End: 2})
		sites = append(sites, `{"path":`+strconv.Quote(name)+`,"line":1}`)
	}
	return `{"outcome":"` + keep + `","sites":[` + strings.Join(sites, ",") + `]}`, capRows
}

// TestValidateSiteCap pins the site cap: the first ten survive in order, the
// rest warn.
func TestValidateSiteCap(t *testing.T) {
	raw, capRows := capSites(12)
	got, warns := brief.Validate(obj(t, raw), capRows)
	if len(got.Sites) != 10 {
		t.Errorf("sites = %d, want 10", len(got.Sites))
	}
	if len(warns) != 2 || !strings.Contains(warns[0], "site 11 is past the 10-site cap") {
		t.Errorf("site cap warns = %v", warns)
	}
}

// TestValidateQuestionCap pins the question cap the same way.
func TestValidateQuestionCap(t *testing.T) {
	questions := make([]string, 0, 12)
	for range 12 {
		questions = append(questions, `"A question long enough to keep"`)
	}
	raw := `{"outcome":"` + keep + `","questions":[` + strings.Join(questions, ",") + `]}`
	got, warns := brief.Validate(obj(t, raw), rows())
	if len(got.Questions) != 10 {
		t.Errorf("questions = %d, want 10", len(got.Questions))
	}
	if len(warns) != 2 || !strings.Contains(warns[0], "question 11 is past the 10-question cap") {
		t.Errorf("question cap warns = %v", warns)
	}
}

// TestFromRecordDecodes proves a stored brief reads back without re-validating
// hunks: its sites and an empty question list survive as stored.
func TestFromRecordDecodes(t *testing.T) {
	stored := `{"brief":{"sites":[{"path":"moved.txt","line":900}],"outcome":"` + keep + `",` +
		`"questions":[]},"body":"x"}`
	got := brief.FromRecord(objOf(t, stored))
	if got == nil {
		t.Fatal("stored brief read as absent")
	}
	if len(got.Sites) != 1 || got.Sites[0].Path != "moved.txt" || got.Sites[0].Line == nil {
		t.Errorf("stored sites = %+v", got.Sites)
	}
	if got.Questions == nil || len(got.Questions) != 0 {
		t.Errorf("stored questions = %v, want empty non-nil", got.Questions)
	}
}

// TestFromRecordAbsent proves a missing or non-object brief reads as nil.
func TestFromRecordAbsent(t *testing.T) {
	for _, raw := range []string{`{"body":"x"}`, `{"brief":null,"body":"x"}`, `{"brief":"text"}`} {
		if brief.FromRecord(objOf(t, raw)) != nil {
			t.Errorf("%s read a brief where none is stored", raw)
		}
	}
}
