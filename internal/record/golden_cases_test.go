package record_test

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/golden"
	"github.com/stainless-code/verdict/internal/jq"
)

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func marshalJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// genDiff builds a diff with H hunks over notes.txt plus a small second file.
func genDiff(hunks int) string {
	var sb strings.Builder
	sb.WriteString("diff --git a/notes.txt b/notes.txt\nindex 1111111..2222222 100644\n--- a/notes.txt\n+++ b/notes.txt\n")
	for h := range hunks {
		start := h*10 + 1
		fmt.Fprintf(&sb, "@@ -%d,7 +%d,7 @@\n", start, start)
		for i := range 7 {
			fmt.Fprintf(&sb, " ctx %d\n", start+i)
		}
	}
	sb.WriteString("diff --git a/other.txt b/other.txt\nindex 3333333..4444444 100644\n--- a/other.txt\n+++ b/other.txt\n@@ -1,3 +1,3 @@\n top\n-old\n+new\n bottom\n")
	return sb.String()
}

// hunkLine returns a line inside hunk h (0-based) of the genDiff layout.
func hunkLine(h int) int {
	return h*10 + 3
}

// genComments builds n deterministic comments over the genDiff layout.
func genComments(n, hunks int) []any {
	out := make([]any, 0, n)
	for i := range n {
		c := map[string]any{}
		switch i % 20 {
		case 10:
			c["path"] = "notes.txt"
			c["line"] = hunkLine(i % hunks)
		case 11:
			c["path"] = "notes.txt"
			c["line"] = "two"
		case 12:
			c["path"] = "notes.txt"
			c["line"] = 99991
		case 13:
			c["path"] = "notes.txt"
			c["line"] = hunkLine(i % hunks)
			c["side"] = "LEFT"
		case 14:
			c["path"] = "notes.txt"
			c["line"] = hunkLine(i % hunks)
			c["suggestion"] = "```suggestion\ncode\n```"
		case 15:
			c["path"] = "notes.txt"
			c["line"] = hunkLine(i % hunks)
			c["side"] = "LEFT"
		default:
			c["path"] = "notes.txt"
			c["line"] = hunkLine((i * 7) % hunks)
			c["side"] = "RIGHT"
		}
		c["body"] = fmt.Sprintf("Comment %d body with enough substance to read as a finding.", i)
		rigors := []string{"said", "pointed", "walked", "ran", "reproduced"}
		c["rigor"] = rigors[i%5]
		if i%3 == 0 {
			sevs := []string{"critical", "important", "informational"}
			c["severity"] = sevs[i%3]
		}
		if i%4 == 0 {
			c["kb"] = []string{"rec-rule"}
		}
		out = append(out, c)
	}
	return out
}

func genReview(t *testing.T, n, hunks int) string {
	t.Helper()
	doc := map[string]any{
		"schema_version": "1",
		"body":           "> [!IMPORTANT]\n> Matrix review body.\n\n### Watch the loop\n\n### Check the edges\n",
		"approved":       false,
		"comments":       genComments(n, hunks),
		"sections": []any{
			map[string]any{"heading": "### Watch the loop", "severity": "important"},
			map[string]any{"heading": "### Check the edges", "severity": "informational"},
		},
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// matrixHunks is the fixed diff width for the matrix cases.
const matrixHunks = 220

func matrixSetup(n int) func(t *testing.T, dir string) {
	return func(t *testing.T, dir string) {
		t.Helper()
		writeFiles(t, dir, map[string]string{
			".verdict/state/local-mx/source.json": `{"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","base_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","head_ref":"work"}`,
			".verdict/state/local-mx/full.diff":   genDiff(matrixHunks),
			".verdict/state/local-mx/review.json": genReview(t, n, matrixHunks),
		})
	}
}

func fencedBodyReview() string {
	raw := `{"schema_version":"1","body":"> ✅ ok\n\n### T\n\n<FENCE>\ncode fence longer than three lines here yes\nline2\nline3\nline4\n<FENCE>\n","approved":false,"comments":[]}`
	return strings.ReplaceAll(raw, "<FENCE>", "```")
}

func mutateSecondPass(t *testing.T, dir string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, ".verdict", "state", "local-mx", "findings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var entries []any
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no entries to mutate")
	}
	keepSig := ""
	for _, e := range entries {
		if m, ok := e.(map[string]any); ok && m["kind"] == "inline" {
			keepSig = jq.StrOf(m["sig"])
			break
		}
	}
	doc := map[string]any{
		"schema_version": "1",
		"body":           "> [!IMPORTANT]\n> Second pass with evidence.\n",
		"approved":       false,
		"comments":       []any{},
		"resolved":       []any{keepSig},
		"triage": []any{
			map[string]any{"sig": keepSig, "status": "deferred", "rationale": "deferring this finding with a rationale long enough to pass"},
		},
	}
	writeFiles(t, dir, map[string]string{
		".verdict/state/local-mx/review.json": marshalJSON(t, doc),
	})
}

// compareRuns runs the Go binary on two identical fixtures, proves the
// runs are byte-identical past timestamps (determinism), and pins one
// side against the checked-in golden.
func compareRuns(t *testing.T, name, dirA, dirB, slug string, args []string) {
	t.Helper()
	full := append([]string{"record", "acme/app", slug}, args...)
	a := runOne(t, dirA, t.TempDir(), full...)
	b := runOne(t, dirB, t.TempDir(), full...)
	if a.Status != b.Status || golden.Scrub(a.Stdout) != golden.Scrub(b.Stdout) ||
		golden.Scrub(a.Stderr) != golden.Scrub(b.Stderr) {
		t.Fatalf("same fixtures diverge:\n%s\n%s", a.Stderr, b.Stderr)
	}
	pinGolden(t, name, dirB, b)
}

// TestGoldenMatrix checks the N-comment matrix on both passes.
func TestGoldenMatrix(t *testing.T) {
	for _, n := range []int{10, 50, 200} {
		t.Run(fmt.Sprintf("n%d", n), func(t *testing.T) {
			t.Helper()
			t.Run("first", func(t *testing.T) {
				t.Helper()
				dirA := t.TempDir()
				dirB := t.TempDir()
				matrixSetup(n)(t, dirA)
				matrixSetup(n)(t, dirB)
				compareRuns(t, fmt.Sprintf("matrix/n%d/first", n), dirA, dirB, "local-mx", []string{"--json"})
			})
			t.Run("rereview", func(t *testing.T) {
				t.Helper()
				matrixRereview(t, n)
			})
		})
	}
}

// matrixRereview runs the record/mutate/record sequence for one matrix width.
func matrixRereview(t *testing.T, n int) {
	t.Helper()
	dirA := t.TempDir()
	dirB := t.TempDir()
	matrixSetup(n)(t, dirA)
	matrixSetup(n)(t, dirB)
	a := runOne(t, dirA, t.TempDir(), "record", "acme/app", "local-mx", "--json")
	b := runOne(t, dirB, t.TempDir(), "record", "acme/app", "local-mx", "--json")
	if a.Status != 0 || b.Status != 0 {
		t.Fatalf("first pass failed: go=%d/%d", a.Status, b.Status)
	}
	mutateSecondPass(t, dirA)
	mutateSecondPass(t, dirB)
	compareRuns(t, fmt.Sprintf("matrix/n%d/rereview", n), dirA, dirB, "local-mx", []string{"--json"})
}

// TestGoldenDegenerateInputs checks degenerate bodies refuse identically.
func TestGoldenDegenerateInputs(t *testing.T) {
	base := map[string]string{
		".verdict/state/local-dg/source.json": `{"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","base_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","head_ref":"work"}`,
		".verdict/state/local-dg/full.diff":   genDiff(4),
	}
	cases := []struct {
		name   string
		review string
	}{
		{"short-body", `{"schema_version":"1","body":"too short","approved":false,"comments":[]}`},
		{"placeholder-body", `{"schema_version":"1","body":"this is a temp placeholder sample text here ok","approved":false,"comments":[]}`},
		{"bad-schema", `{"schema_version":"2","body":"> ✅ marker fine and long enough body here.","approved":false,"comments":[]}`},
		{"missing-marker-ok", `{"schema_version":"1","body":"this body is long enough to pass the degenerate guard without any marker at all yes","approved":false,"comments":[]}`},
		{"empty-diff-body", `{"schema_version":"1","body":"> ℹ️ note with marker and enough length to pass.","approved":false,"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"Anchors unchecked.","rigor":"pointed"}]}`},
		{"null-path", `{"schema_version":"1","body":"> [!IMPORTANT]\n> Body with length to pass the guard.","approved":false,"comments":[{"path":null,"line":2,"side":"RIGHT","body":"Null path.","rigor":"pointed"}]}`},
		{"false-anchors", `{"schema_version":"1","body":"> [!IMPORTANT]\n> Body with length to pass the guard.","approved":false,"comments":[{"path":"notes.txt","line":false,"start_line":false,"side":5,"body":"False anchors.","rigor":5,"severity":true,"in-bounds":false}]}`},
		{"false-side", `{"schema_version":"1","body":"> [!IMPORTANT]\n> Body with length to pass the guard.","approved":false,"comments":[{"path":"notes.txt","line":2,"side":false,"body":"False side.","rigor":false}]}`},
		{"fenced-body-section", fencedBodyReview()},
		{"normalized-fenced", "```json\n" + `{"schema_version":"1","body":"> ✅ marker plus enough length to pass the guard ok.","approved":false,"comments":[]}` + "\n```"},
		{"normalized-prose", "Here is the review you asked for:\n\n" + `{"schema_version":"1","body":"> ✅ marker plus enough length to pass the guard ok.","approved":false,"comments":[]}` + "\n\nThanks."},
		{"normalized-weak-shapes", `{"schema_version":"1","body":"> ✅ marker plus enough length to pass the guard ok.","approved":false,"resolved":"x","comments":[{"file":"notes.txt","line":2,"side":"RIGHT","body":"Weak-model finding with enough substance.","rung":"Pointed","severity":"Important","inBounds":true,"suggestion":null}]}`},
		{"brief-valid", `{"schema_version":"1","body":"> ✅ marker plus enough length to pass the guard ok.","approved":false,"comments":[{"path":"notes.txt","line":3,"side":"RIGHT","body":"Brief-carrying finding.","rigor":"walked","severity":"important","brief":{"sites":[{"path":"notes.txt","line":4},{"path":"other.txt"}],"outcome":"Reject duplicate deliveries before anything is enqueued.","approach":"Track delivery ids in a bounded LRU checked at the top of the handler.","questions":["Should the dedupe window be 24 hours or 7 days?"]}}]}`},
		{"brief-invalid", `{"schema_version":"1","body":"> ✅ marker plus enough length to pass the guard ok.","approved":false,"comments":[{"path":"notes.txt","line":3,"side":"RIGHT","body":"Brief with bad parts.","rigor":"pointed","brief":{"sites":[{"path":"unknown/file.txt","line":2},{"path":"notes.txt","line":99},{"path":"other.txt"}],"outcome":"Keep the valid outcome and drop everything else.","approach":"tiny","questions":["A question long enough to keep",42],"outcome_typo":"x"}}]}`},
		{"brief-bad-outcome", `{"schema_version":"1","body":"> ✅ marker plus enough length to pass the guard ok.","approved":false,"comments":[{"path":"notes.txt","line":3,"side":"RIGHT","body":"Brief with a short outcome.","rigor":"pointed","brief":{"sites":[{"path":"other.txt"}],"outcome":"too short"}}]}`},
		{"blob-link-local", `{"schema_version":"1","body":"> ✅ ok with a blob link https://x/blob/aaaaaaa/p#L1 here yes sir.","approved":false,"comments":[]}`},
		{"basis-ok", `{"schema_version":"1","body":"> Confidence: 3/5\n> Basis: 0 0 0 1 0 0 0\n> Some content here to pass length.","approved":false,"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"One pointed.","rigor":"pointed"}]}`},
		{"basis-bad", `{"schema_version":"1","body":"> Confidence: 3/5\n> Basis: 9 9 9 9 9 9 9\n> Some content here to pass length.","approved":false,"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"One pointed.","rigor":"pointed"}]}`},
		{"carried-footer", `{"schema_version":"1","body":"> ✅ marker plus enough length to pass the guard ok.\n\n---\n\n<!-- verdict-footer -->\n> old receipt\n","approved":false,"comments":[]}`},
		{"confidence-valid", `{"schema_version":"1","confidence":4,"security_summary":"nothing alarming","body":"> ✅ marker plus enough length to pass the guard ok.","approved":false,"comments":[]}`},
		{"confidence-invalid", `{"schema_version":"1","confidence":"high","security_summary":5,"body":"> ✅ marker plus enough length to pass the guard ok.","approved":false,"comments":[]}`},
		{"unknown-flag", `{"schema_version":"1","body":"> ✅ marker plus enough length to pass the guard ok.","approved":false,"comments":[]}`},
		{"cost-names-empty-seat", `{"schema_version":"1","body":"> ✅ marker plus enough length to pass the guard ok.","approved":false,"comments":[]}`},
		{"cost-names-no-panel", `{"schema_version":"1","body":"> ✅ marker plus enough length to pass the guard ok.","approved":false,"comments":[]}`},
	}
	for _, tc := range cases {
		extraArgs := []string{"--json"}
		switch tc.name {
		case "unknown-flag":
			extraArgs = []string{"--bogus"}
		case "cost-names-empty-seat":
			extraArgs = []string{"--cost-names=lead,,lens-1"}
		case "cost-names-no-panel":
			extraArgs = []string{"--cost-names"}
		}
		diff := base[".verdict/state/local-dg/full.diff"]
		if tc.name == "empty-diff-body" {
			diff = ""
		}
		files := map[string]string{
			".verdict/state/local-dg/source.json": base[".verdict/state/local-dg/source.json"],
			".verdict/state/local-dg/full.diff":   diff,
			".verdict/state/local-dg/review.json": tc.review,
		}
		goldenBoth(t, "degenerate/"+tc.name, "local-dg", func(t *testing.T, dir string) {
			t.Helper()
			writeFiles(t, dir, files)
		}, extraArgs...)
	}
}

// TestGoldenMissingFiles checks missing bundle files refuse identically.
func TestGoldenMissingFiles(t *testing.T) {
	full := map[string]string{
		".verdict/state/local-mf/source.json": `{"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","base_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","head_ref":"work"}`,
		".verdict/state/local-mf/full.diff":   genDiff(2),
		".verdict/state/local-mf/review.json": `{"schema_version":"1","body":"> ✅ marker plus enough length to pass.","approved":false,"comments":[]}`,
	}
	for _, drop := range []string{"review.json", "full.diff", "source.json"} {
		goldenBoth(t, "missing/"+drop, "local-mf", func(t *testing.T, dir string) {
			t.Helper()
			files := maps.Clone(full)
			delete(files, ".verdict/state/local-mf/"+drop)
			writeFiles(t, dir, files)
		})
	}
	goldenBoth(t, "missing/bundle", "local-nothing", func(_ *testing.T, _ string) {})
	goldenBoth(t, "not-local/github", "4251", func(t *testing.T, dir string) {
		t.Helper()
		writeFiles(t, dir, map[string]string{
			".verdict/state/4251/PR.json":   `{"state":"OPEN","headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`,
			".verdict/state/4251/full.diff": genDiff(2),
		})
	})
	goldenBoth(t, "provider/gitlab", "47", func(t *testing.T, dir string) {
		t.Helper()
		writeFiles(t, dir, map[string]string{
			".verdict/state/47/PR.json":     `{"provider":"gitlab","head_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","base_sha":"cccccccccccccccccccccccccccccccccccccccc"}`,
			".verdict/state/47/full.diff":   genDiff(2),
			".verdict/state/47/review.json": `{"schema_version":"1","body":"> ✅ marker plus enough length to pass.","approved":false,"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"Provider comment.","rigor":"pointed"}]}`,
		})
	})
}

// TestGoldenChunked checks chunked bundles merge identically.
func TestGoldenChunked(t *testing.T) {
	chunkDiff := func(body string) string {
		return "diff --git a/c1.txt b/c1.txt\nindex 1111111..2222222 100644\n--- a/c1.txt\n+++ b/c1.txt\n@@ -1,2 +1,2 @@\n a\n" + body + "\n"
	}
	setup := func(t *testing.T, dir string, manifest, review, c1, c2 string) {
		t.Helper()
		files := map[string]string{
			".verdict/state/local-ch/source.json":          `{"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","base_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","head_ref":"work"}`,
			".verdict/state/local-ch/full.diff":            c1 + c2,
			".verdict/state/local-ch/review.json":          review,
			".verdict/state/local-ch/chunks/manifest.json": manifest,
			".verdict/state/local-ch/chunks/a.diff":        c1,
			".verdict/state/local-ch/chunks/b.diff":        c2,
		}
		writeFiles(t, dir, files)
	}
	c1 := chunkDiff("-old1\n+new1")
	c2 := chunkDiff("-old2\n+new2")
	goodManifest := `{"schema_version":"1","chunks":[{"name":"a","index":0,"diff":".verdict/state/local-ch/chunks/a.diff","files":["c1.txt"]},{"name":"b","index":1,"diff":".verdict/state/local-ch/chunks/b.diff","files":["c1.txt"]}]}`
	goodReview := `{"schema_version":"1","chunks":["a","b"],"body":"> ✅ marker plus enough length to pass.","approved":false,"comments":[{"path":"c1.txt","line":2,"side":"RIGHT","body":"Chunk comment.","rigor":"walked"}]}`
	goldenBoth(t, "chunk/valid", "local-ch", func(t *testing.T, dir string) {
		t.Helper()
		setup(t, dir, goodManifest, goodReview, c1, c2)
	})
	goldenBoth(t, "chunk/unattested", "local-ch", func(t *testing.T, dir string) {
		t.Helper()
		setup(t, dir, goodManifest, `{"schema_version":"1","body":"> ✅ marker plus enough length to pass.","approved":false,"comments":[]}`, c1, c2)
	})
	goldenBoth(t, "chunk/partial", "local-ch", func(t *testing.T, dir string) {
		t.Helper()
		setup(t, dir, goodManifest, `{"schema_version":"1","chunks":["a"],"body":"> ✅ marker plus enough length to pass.","approved":false,"comments":[]}`, c1, c2)
	})
	goldenBoth(t, "chunk/bad-schema", "local-ch", func(t *testing.T, dir string) {
		t.Helper()
		setup(t, dir, `{"schema_version":"2","chunks":[]}`, goodReview, c1, c2)
	})
	goldenBoth(t, "chunk/unknown-chunk-claim", "local-ch", func(t *testing.T, dir string) {
		t.Helper()
		setup(t, dir, goodManifest, `{"schema_version":"1","chunks":["a","b","zzz"],"body":"> ✅ marker plus enough length to pass.","approved":false,"comments":[]}`, c1, c2)
	})
	goldenBoth(t, "chunk/missing-snapshot", "local-ch", func(t *testing.T, dir string) {
		t.Helper()
		setup(t, dir, goodManifest, goodReview, c1, c2)
		if err := os.Remove(filepath.Join(dir, ".verdict", "state", "local-ch", "chunks", "b.diff")); err != nil {
			t.Fatal(err)
		}
	})
	goldenBoth(t, "chunk/malformed-attestation", "local-ch", func(t *testing.T, dir string) {
		t.Helper()
		setup(t, dir, goodManifest, `{"schema_version":"1","chunks":"a","body":"> ✅ marker plus enough length to pass.","approved":false,"comments":[]}`, c1, c2)
	})
}
