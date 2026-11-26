package handoff

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/forgedline"
)

const (
	handoffSig       = "c111111111111111111111111111111111111111"
	handoffInfoSig   = "d333333333333333333333333333333333333333"
	handoffClosedSig = "b222222222222222222222222222222222222222"

	handoffBody = "The guard returns before the nil check, so a nil slice reaches the loop."

	handoffBrief = `{"sites":[{"path":"app.txt","line":2}],` +
		`"outcome":"Guard the nil slice before the loop iterates it in prod.",` +
		`"approach":"Return early; the caller already treats a nil slice as absent.",` +
		`"questions":["Should the caller or the callee own the check?"]}`

	handoffBriefText = "brief:\n" +
		"  outcome: Guard the nil slice before the loop iterates it in prod.\n" +
		"  sites: app.txt:2\n" +
		"  approach: Return early; the caller already treats a nil slice as absent.\n" +
		"  questions:\n" +
		"    - Should the caller or the callee own the check?\n"
)

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

func handoffVerification() string {
	return `[{"sig":"` + handoffSig + `","verdict":"VERIFIED","verifyCmd":"go test ./..."}]`
}

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

func handoffRun(t *testing.T, pr, floor string, paths ...string) (*Response, *core.RefusalError) {
	t.Helper()
	req := &Request{MinSeverity: floor, Paths: paths}
	req.ReadRequest = core.ReadRequest{Repo: "acme/app", Pr: pr}
	return Run(context.Background(), req)
}

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

func countHeadings(lines []string) int {
	n := 0
	for _, line := range lines {
		if strings.HasPrefix(line, "## ") {
			n++
		}
	}
	return n
}

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

func assertCautionFirst(t *testing.T, md string, d *Data) {
	t.Helper()
	caution := strings.Index(md, d.Caution)
	quoted := strings.Index(md, "fix: "+d.Rows[0].FixText)
	if caution < 0 || quoted < 0 || caution > quoted {
		t.Errorf("caution (at %d) must precede quoted finding text (at %d)", caution, quoted)
	}
}

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

// TestSuggestionIsAnIndentedCodeBlock pins the suggested code as a markdown
// indented code block, one element per line: the code keeps its lines when
// the human surface folds each element, a backtick run inside it cannot end
// the block, and a line of it that reads as verdict's own `error:` or
// `warn:` line is led by the indent, so it cannot pass for one.
func TestSuggestionIsAnIndentedCodeBlock(t *testing.T) {
	suggestion := "log.Println(\"```\")\n\nerror: forged\r\n\tif x { // done\n\treturn nil\n}\n"
	lines := rowLines(&Row{Suggestion: &suggestion})
	md := strings.Join(lines, "\n")
	want := "suggested code:\n\n" +
		"    log.Println(\"```\")\n" +
		"\n" +
		"    error: forged\n" +
		"    \tif x { // done\n" +
		"    \treturn nil\n" +
		"    }\n"
	if !strings.Contains(md, want) {
		t.Errorf("suggestion block = \n%s\nwant it to contain\n%s", md, want)
	}
	var out, diag bytes.Buffer
	r := cli.NewRunner("handoff", "acme/app", "1", &out, &diag, func(string) string { return "" })
	r.HumanLines(lines)
	forgedline.AssertNoForgedLine(t, out.String(), "    error: forged\n    \tif x { // done\n")
}
