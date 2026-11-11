package backtest

import (
	"slices"
	"testing"
)

// TestParseFixHunks pins the old-side line numbers and the header/body split.
func TestParseFixHunks(t *testing.T) {
	diff := `diff --git a/a.go b/a.go
index 1111111..2222222 100644
--- a/a.go
+++ b/a.go
@@ -3,2 +3,3 @@ func main() {
-old one
-old two
+new one
+new two
+new three
diff --git a/new.go b/new.go
new file mode 100644
--- /dev/null
+++ b/new.go
@@ -0,0 +1 @@
+added
diff --git a/"odd name.go" b/"odd name.go"
--- "a/odd name.go"
+++ "b/odd name.go"
@@ -1 +1 @@
--- keep
-++ keep
+x
`
	got := parseFixHunks(diff)
	want := []diffLine{
		{Path: "a.go", Line: 3, Text: "old one"},
		{Path: "a.go", Line: 4, Text: "old two"},
		{Path: "odd name.go", Line: 1, Text: "-- keep"},
		{Path: "odd name.go", Line: 2, Text: "++ keep"},
	}
	if !slices.Equal(got.Deleted, want) {
		t.Errorf("deleted = %+v; want %+v", got.Deleted, want)
	}
	wantAdded := []string{"new one", "new two", "new three", "added", "x"}
	if !slices.Equal(got.Added, wantAdded) {
		t.Errorf("added = %v; want %v", got.Added, wantAdded)
	}
	if got.Binary {
		t.Error("binary = true; want false")
	}
}

// TestParseFixHunksMultiHunk pins old-side numbering across hunks, the CRLF
// byte a Windows file keeps, and the no-newline marker that carries no line.
func TestParseFixHunksMultiHunk(t *testing.T) {
	diff := "diff --git a/a.txt b/a.txt\n--- a/a.txt\n+++ b/a.txt\n" +
		"@@ -2 +2 @@\n-old one\n+new one\n" +
		"@@ -9,2 +9,2 @@\n-old two\r\n-old three\n\\ No newline at end of file\n+new two\n+new three\n"
	got := parseFixHunks(diff)
	want := []diffLine{
		{Path: "a.txt", Line: 2, Text: "old one"},
		{Path: "a.txt", Line: 9, Text: "old two\r"},
		{Path: "a.txt", Line: 10, Text: "old three"},
	}
	if !slices.Equal(got.Deleted, want) {
		t.Errorf("deleted = %+v; want %+v", got.Deleted, want)
	}
}

// TestParseFixHunksBinary pins the binary marker.
func TestParseFixHunksBinary(t *testing.T) {
	got := parseFixHunks("diff --git a/x.bin b/x.bin\nBinary files a/x.bin and b/x.bin differ\n")
	if !got.Binary {
		t.Error("binary = false; want true")
	}
	if len(got.Deleted) != 0 {
		t.Errorf("deleted = %+v; want none", got.Deleted)
	}
}

// TestClassifyKind pins every subject word and every diff rule.
func TestClassifyKind(t *testing.T) {
	tests := []struct { //nolint:govet // test table readability
		name    string
		subject string
		deleted []string
		added   []string
		want    string
	}{
		{"auth subject", "fix: check authorization", nil, nil, "auth-bypass"},
		{"path subject", "fix: confine the upload", nil, nil, "path-traversal"},
		{"injection subject", "fix: sanitize the SQL", nil, nil, "injection"},
		{"race subject", "fix: data race on the counter", nil, nil, "race"},
		{"nil subject", "fix: nil pointer on close", nil, nil, "nil-deref"},
		{"off-by-one subject", "fix: off-by-one loop bound", nil, nil, "off-by-one"},
		{"index subject", "fix: index out of range", nil, nil, "index-out-of-range"},
		{"error subject", "fix: handle the error", nil, nil, "missing-error-check"},
		{"word boundary", "fix: trace logging", nil, nil, "unclassified"},
		{"diff error check", "fix: guard", []string{"os.Remove(name)"}, []string{"if err != nil {"}, "missing-error-check"},
		{"diff nil", "fix: guard", []string{"return nil"}, nil, "nil-deref"},
		{"diff off-by-one", "fix: adjust", []string{"i <= len(xs)"}, []string{"i < len(xs)"}, "off-by-one"},
		{"diff index", "fix: adjust", nil, []string{"if i >= len(xs) {"}, "index-out-of-range"},
		{"diff race", "fix: adjust", nil, []string{"mu sync.Mutex"}, "race"},
		{"diff path", "fix: adjust", nil, []string{"p = filepath.Clean(p)"}, "path-traversal"},
		{"diff injection", "fix: adjust", nil, []string{`q := "SELECT * FROM t WHERE id = ?"`}, "injection"},
		{"diff auth", "fix: adjust", nil, []string{"if !authorized {"}, "auth-bypass"},
		{"unclassified", "fix: tweak", []string{"a"}, []string{"b"}, "unclassified"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyKind(tc.subject, tc.deleted, tc.added); got != tc.want {
				t.Errorf("classifyKind(%q) = %q; want %q", tc.subject, got, tc.want)
			}
		})
	}
}

// TestPathClass pins the vendored/generated path rules.
func TestPathClass(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"vendor/lib/x.go", "vendor"},
		{"a/node_modules/pkg/index.js", "node_modules"},
		{"third_party/z/base.c", "third_party"},
		{"api.pb.go", "generated"},
		{"zz_generated.deepcopy.go", "generated"},
		{"x.gen.go", "generated"},
		{"go.sum", "generated"},
		{"package-lock.json", "generated"},
		{"main.go", ""},
		{"docs/guide.md", ""},
	}
	for _, tc := range tests {
		if got := pathClass(tc.path); got != tc.want {
			t.Errorf("pathClass(%q) = %q; want %q", tc.path, got, tc.want)
		}
	}
}

// TestDocsTestOnly pins the whole-fix documentation/test rule.
func TestDocsTestOnly(t *testing.T) {
	tests := []struct {
		paths []string
		want  bool
	}{
		{[]string{"docs/guide.md"}, true},
		{[]string{"README.md"}, true},
		{[]string{"pkg/x_test.go"}, true},
		{[]string{"tests/e2e.py"}, true},
		{[]string{"web/app.test.ts"}, true},
		{[]string{"docs/guide.md", "main.go"}, false},
		{[]string{"main.go"}, false},
		{nil, false},
	}
	for _, tc := range tests {
		if got := docsTestOnly(tc.paths); got != tc.want {
			t.Errorf("docsTestOnly(%v) = %v; want %v", tc.paths, got, tc.want)
		}
	}
}
