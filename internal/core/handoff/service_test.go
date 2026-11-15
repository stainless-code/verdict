package handoff

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

const (
	// handoffSig is the open critical finding every leg projects.
	handoffSig = "c111111111111111111111111111111111111111"
	// handoffInfoSig is open but informational: below the default floor.
	handoffInfoSig = "d333333333333333333333333333333333333333"
	// handoffClosedSig is important but already fixed.
	handoffClosedSig = "b222222222222222222222222222222222222222"

	// handoffBody is the fix text the projection carries.
	handoffBody = "The guard returns before the nil check, so a nil slice reaches the loop."

	// handoffBrief is the stored machine brief, as review.json carries it.
	handoffBrief = `{"sites":[{"path":"app.txt","line":2}],` +
		`"outcome":"Guard the nil slice before the loop iterates it in prod.",` +
		`"approach":"Return early; the caller already treats a nil slice as absent.",` +
		`"questions":["Should the caller or the callee own the check?"]}`

	// handoffBriefText is exactly what fix prints for that brief.
	handoffBriefText = "brief:\n" +
		"  outcome: Guard the nil slice before the loop iterates it in prod.\n" +
		"  sites: app.txt:2\n" +
		"  approach: Return early; the caller already treats a nil slice as absent.\n" +
		"  questions:\n" +
		"    - Should the caller or the callee own the check?\n"
)

// handoffFindings is a four-entry store: one critical open inline finding with
// a brief, plus three entries the default projection leaves out (informational
// inline, closed inline, open section).
func handoffFindings() string {
	return `[` +
		`{"sig":"` + handoffSig + `","kind":"inline","path":"app.txt","line":2,` +
		`"side":"RIGHT","anchored":true,"body":"` + handoffBody + `","status":"open",` +
		`"severity":"critical","rigor":"pointed","kb":["await-err"],` +
		`"suggestion":"if items == nil { return }","brief":` + handoffBrief + `},` +
		`{"sig":"` + handoffInfoSig + `","kind":"inline","path":"app.txt","line":9,` +
		`"status":"open","severity":"informational","rigor":"said","kb":[],` +
		`"body":"Nit: the name repeats the type."},` +
		`{"sig":"` + handoffClosedSig + `","kind":"inline","path":"web.go","line":4,` +
		`"status":"fixed","severity":"important","rigor":"said","kb":[],` +
		`"body":"Already patched."},` +
		`{"sig":"e444444444444444444444444444444444444444","kind":"section",` +
		`"heading":"Testing gaps","status":"open","severity":"critical","rigor":null,` +
		`"kb":[],"body":"No test covers the retry path."}` +
		`]`
}

// handoffVerification records the rerun command for the critical finding, the
// same claim fix expects a pass to write.
func handoffVerification() string {
	return `[{"sig":"` + handoffSig + `","verdict":"VERIFIED","verifyCmd":"go test ./...",` +
		`"resultHead":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]`
}

// handoffBundle writes one local bundle under the state home.
func handoffBundle(t *testing.T, name string, files map[string]string) {
	t.Helper()
	dir := filepath.Join(".verdict", "state", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for file, body := range files {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// handoffRun runs the projection over the bundle named by pr.
func handoffRun(t *testing.T, pr, floor string, paths ...string) (*Response, *core.RefusalError) {
	t.Helper()
	req := &Request{MinSeverity: floor, Paths: paths}
	req.ReadRequest = core.ReadRequest{Repo: "acme/app", Pr: pr}
	return Run(context.Background(), req)
}

// TestRunProjectsTheOpenFindingFixWouldTake pins the projection field by
// field against literals: the same open inline pick fix makes, with the brief
// text fix prints and the recorded verify command.
func TestRunProjectsTheOpenFindingFixWouldTake(t *testing.T) {
	t.Chdir(t.TempDir())
	handoffBundle(t, "local-42", map[string]string{
		"findings.json":     handoffFindings(),
		"verification.json": handoffVerification(),
	})

	resp, ref := handoffRun(t, "42", "")
	if ref != nil {
		t.Fatalf("handoff refused: %s: %s", ref.Code, ref.Message)
	}
	if resp.Data.Count != 1 || len(resp.Data.Rows) != 1 {
		t.Fatalf("count = %d, rows = %d, want the one open critical inline finding",
			resp.Data.Count, len(resp.Data.Rows))
	}
	row := resp.Data.Rows[0]
	assertProjection(t, &row)
}

// assertProjection holds one row to the fixture's literals: the fix pick's
// fields, the brief text fix prints, and the recorded verify command.
func assertProjection(t *testing.T, row *Row) {
	t.Helper()
	for _, field := range []struct{ name, got, want string }{
		{"sig", row.Sig, handoffSig},
		{"locator", row.Locator, "app.txt:2"},
		{"severity", row.Severity, "critical"},
		{"rigor", row.Rigor, "pointed"},
		{"fix_text", row.FixText, handoffBody},
		{"brief_text", row.BriefText, handoffBriefText},
	} {
		if field.got != field.want {
			t.Errorf("%s = %q, want %q", field.name, field.got, field.want)
		}
	}
	assertOptional(t, row)
}

// assertOptional pins the three fields the store may leave out.
func assertOptional(t *testing.T, row *Row) {
	t.Helper()
	if want := []string{"await-err"}; !slices.Equal(row.KBRules, want) {
		t.Errorf("kb_rules = %v, want %v", row.KBRules, want)
	}
	if row.Suggestion == nil || *row.Suggestion != "if items == nil { return }" {
		t.Errorf("suggestion = %v, want the stored code suggestion", row.Suggestion)
	}
	if row.VerifyCmd == nil || *row.VerifyCmd != "go test ./..." {
		t.Errorf("verify_cmd = %v, want the recorded claim command", row.VerifyCmd)
	}
}

// TestMarkdownCarriesTheJSONRows proves the two projections agree: every row
// appears once in the markdown with the same brief text and fix text the
// envelope carries, and the caution precedes the quoted prose.
func TestMarkdownCarriesTheJSONRows(t *testing.T) {
	t.Chdir(t.TempDir())
	handoffBundle(t, "local-42", map[string]string{"findings.json": handoffFindings()})

	resp, ref := handoffRun(t, "42", "")
	if ref != nil {
		t.Fatalf("handoff refused: %s: %s", ref.Code, ref.Message)
	}
	md := strings.Join(resp.Human, "\n") + "\n"
	if got, want := countHeadings(resp.Human), len(resp.Data.Rows); got != want {
		t.Errorf("markdown headings = %d, json rows = %d", got, want)
	}
	assertRowsInMarkdown(t, md, resp.Data.Rows)
	assertCautionFirst(t, md, resp.Data)
}

// countHeadings counts the per-finding sections in the markdown.
func countHeadings(lines []string) int {
	n := 0
	for _, line := range lines {
		if strings.HasPrefix(line, "## ") {
			n++
		}
	}
	return n
}

// assertRowsInMarkdown holds each row's sig, fix text, and brief text to a
// literal substring of the markdown.
func assertRowsInMarkdown(t *testing.T, md string, rows []Row) {
	t.Helper()
	for i := range rows {
		row := &rows[i]
		for _, want := range []string{"## " + row.Sig, "fix: " + row.FixText, row.BriefText} {
			if !strings.Contains(md, want) {
				t.Errorf("markdown lacks %q for row %s", want, row.Sig)
			}
		}
	}
}

// assertCautionFirst proves the untrusted-data line comes before any quoted
// finding prose.
func assertCautionFirst(t *testing.T, md string, d *Data) {
	t.Helper()
	caution := strings.Index(md, d.Caution)
	quoted := strings.Index(md, "fix: "+d.Rows[0].FixText)
	if caution < 0 || quoted < 0 || caution > quoted {
		t.Errorf("caution (at %d) must precede quoted finding text (at %d)", caution, quoted)
	}
}

// TestRefusalNamesTheFloor proves an empty projection refuses and says which
// floor emptied it, and that lowering the floor to off projects the same
// bundle.
func TestRefusalNamesTheFloor(t *testing.T) {
	t.Chdir(t.TempDir())
	handoffBundle(t, "local-7", map[string]string{
		"findings.json": `[{"sig":"` + handoffInfoSig + `","kind":"inline","path":"app.txt",` +
			`"line":9,"status":"open","severity":"informational","rigor":"said",` +
			`"kb":[],"body":"Nit: the name repeats the type."}]`,
	})

	_, ref := handoffRun(t, "7", "")
	if ref == nil {
		t.Fatal("handoff over an informational-only bundle must refuse")
	}
	if ref.Code != "no-open-findings" {
		t.Errorf("code = %q, want no-open-findings", ref.Code)
	}
	if !strings.Contains(ref.Message, "at or above important") {
		t.Errorf("message %q does not name the floor", ref.Message)
	}
	resp, ref := handoffRun(t, "7", FloorOff)
	if ref != nil {
		t.Fatalf("floor off refused: %s: %s", ref.Code, ref.Message)
	}
	if resp.Data.Count != 1 || resp.Data.Floor != FloorOff {
		t.Errorf("floor off = %d rows at %q, want the informational finding", resp.Data.Count, resp.Data.Floor)
	}
}

// TestPathsScopeProjection proves --paths narrows the same selector: a glob
// matching nothing refuses naming the globs, and one matching the finding
// projects it.
func TestPathsScopeProjection(t *testing.T) {
	t.Chdir(t.TempDir())
	handoffBundle(t, "local-42", map[string]string{"findings.json": handoffFindings()})

	_, ref := handoffRun(t, "42", "", "web/*.go")
	if ref == nil {
		t.Fatal("a glob matching no open finding must refuse")
	}
	if !strings.Contains(ref.Message, "under web/*.go") {
		t.Errorf("message %q does not name the globs", ref.Message)
	}
	resp, ref := handoffRun(t, "42", "", "*.txt")
	if ref != nil {
		t.Fatalf("scoped handoff refused: %s: %s", ref.Code, ref.Message)
	}
	if resp.Data.Count != 1 || !slices.Equal(resp.Data.Paths, []string{"*.txt"}) {
		t.Errorf("scoped projection = %d rows over %v", resp.Data.Count, resp.Data.Paths)
	}
}

// TestRunWritesNothing proves the read-only contract: the bundle tree and the
// working directory are byte-identical after a run.
func TestRunWritesNothing(t *testing.T) {
	t.Chdir(t.TempDir())
	handoffBundle(t, "local-42", map[string]string{"findings.json": handoffFindings()})
	before := treeOf(t, ".")

	if _, ref := handoffRun(t, "42", ""); ref != nil {
		t.Fatalf("handoff refused: %s: %s", ref.Code, ref.Message)
	}
	after := treeOf(t, ".")
	if !slices.Equal(before, after) {
		t.Fatalf("handoff changed the tree:\nbefore %v\nafter  %v", before, after)
	}
}

// treeOf lists every path under dir with its size, sorted, so a created,
// emptied, or rewritten file shows up as a difference.
func treeOf(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		out = append(out, path+" "+strconv.FormatInt(info.Size(), 10))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(out)
	return out
}
