package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/cli"
)

// hostileFindings is a store whose inline finding names a path, and whose
// section finding a heading, carrying every kind of sequence a PR author could
// plant for the operator's terminal: an OSC 52 clipboard write, a CSI cursor
// move, a C1 CSI (U+009B), a carriage return that overwrites the line, a line
// separator, and a bidi override (U+202E).
const hostileFindings = `[` +
	`{"kind":"inline","path":"app/\u001b]52;c;cHduZWQ=\u0007\u001b[2J\u009b31m\rx\u2028\u202e.go","line":3,"side":"RIGHT",` +
	`"sig":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","status":"open","severity":"critical","body":"A finding."},` +
	`{"kind":"section","heading":"## bad\u001b]52;c;cHduZWQ=\u0007\r\u0085forged: gate passes","path":null,` +
	`"sig":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","status":"open","severity":"important","body":"B."}]`

const prJSON = `{"state":"OPEN","headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",` +
	`"baseRefOid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`

func hostileStore(t *testing.T) {
	t.Helper()
	hermeticEnv(t)
	t.Chdir(t.TempDir())
	dir := filepath.Join(".verdict", "state", "9")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"PR.json": prJSON, "findings.json": hostileFindings} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// TestHumanStdoutIsInertForHostileFindingText proves the runner's stdout
// carries no control rune a PR author planted in a finding's path or heading,
// whichever verb renders it.
func TestHumanStdoutIsInertForHostileFindingText(t *testing.T) {
	hostileStore(t)
	for _, args := range [][]string{
		{"report", "acme/app", "9"},
		{"next", "acme/app", "9"},
		{"show", "acme/app", "9", "--sig", "aaaaaaaa"},
		{"show", "acme/app", "9", "--sig", "bbbbbbbb"},
	} {
		got := runOne(t, args...)
		what := strings.Join(args, " ")
		if got.code != cli.ExitOK || got.out == "" {
			t.Fatalf("%s = %+v; want output and success", what, got)
		}
		assertInert(t, what+" stdout", got.out)
		assertInert(t, what+" stderr", got.err)
	}
	report := runOne(t, "report", "acme/app", "9")
	if !strings.Contains(report.out, "app/ ]52;c;cHduZWQ=") || !strings.Contains(report.out, "forged: gate passes") {
		t.Errorf("report stdout lost the finding text instead of folding it:\n%s", report.out)
	}
}

// TestJSONStdoutIsNotFolded proves --json passes through untouched: the
// envelope escapes its C0 controls itself, a consumer parses the finding's
// path back byte for byte, and the C1 rune Go's encoder writes raw (U+009B)
// is still there, which a fold would have turned into a space.
func TestJSONStdoutIsNotFolded(t *testing.T) {
	hostileStore(t)
	got := runOne(t, "report", "acme/app", "9", "--json")
	if got.code != cli.ExitOK {
		t.Fatalf("report --json = %+v; want success", got)
	}
	var env struct {
		Data struct {
			Items []struct {
				Path string `json:"path"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(got.out), &env); err != nil {
		t.Fatalf("report --json is not one envelope: %v\n%s", err, got.out)
	}
	const want = "app/\x1b]52;c;cHduZWQ=\a\x1b[2J\u009b31m\rx\u2028\u202e.go"
	if len(env.Data.Items) != 2 || env.Data.Items[0].Path != want {
		t.Errorf("--json items = %+v; want the first path %q", env.Data.Items, want)
	}
	if !strings.Contains(got.out, `\u001b]52;c;cHduZWQ=\u0007\u001b[2J`+"\u009b31m") {
		t.Errorf("--json envelope was rewritten:\n%s", got.out)
	}
}

// assertInert fails when text carries a control rune the fold must have
// replaced: every C0 control except the newline and tab, DEL, C1, and the
// U+2028/U+2029 separators, and the bidi controls.
func assertInert(t *testing.T, what, text string) {
	t.Helper()
	for _, r := range text {
		if isControl(r) {
			t.Fatalf("%s carries control rune %U:\n%q", what, r, text)
		}
	}
}

// foldedSeparators are the separator and bidi runes outside C0 and C1.
const foldedSeparators = "\u2028\u2029\u061c\u200e\u200f\u202a\u202b\u202c\u202d\u202e\u2066\u2067\u2068\u2069"

func isControl(r rune) bool {
	if r == '\n' || r == '\t' {
		return false
	}
	return r < ' ' || (r >= 0x7f && r <= 0x9f) || strings.ContainsRune(foldedSeparators, r)
}
