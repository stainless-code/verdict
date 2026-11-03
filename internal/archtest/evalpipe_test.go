package archtest

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// earlyExitGrep matches `grep -q` at the end of a pipeline. Under the evals'
// `set -o pipefail`, grep -q exits at the first match and a producer still
// writing takes SIGPIPE, failing an assertion whose text did match. Cases use
// the `matches` helper from evals/lib/common.sh instead, which reads all input.
var earlyExitGrep = regexp.MustCompile(`\|\s*grep\s+-[A-Za-z]*q`)

// TestEvalsAvoidEarlyExitGrep keeps the SIGPIPE flake class out of the eval
// suite: no shell file under evals/ may pipe into `grep -q`.
func TestEvalsAvoidEarlyExitGrep(t *testing.T) {
	root := moduleRoot(t)
	files, err := filepath.Glob(filepath.Join(root, "evals", "*", "*.sh"))
	if err != nil {
		t.Fatal(err)
	}
	top, err := filepath.Glob(filepath.Join(root, "evals", "*.sh"))
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, top...)
	if len(files) == 0 {
		t.Fatal("no eval shell files found; the guard would pass vacuously")
	}
	for _, path := range files {
		for _, line := range earlyExitLines(t, path) {
			t.Errorf("%s: pipes into grep -q (use `matches` from evals/lib/common.sh): %s",
				filepath.ToSlash(strings.TrimPrefix(path, root+string(filepath.Separator))), line)
		}
	}
}

// earlyExitLines returns the non-comment lines of one file that pipe into
// grep -q.
func earlyExitLines(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var hits []string
	for line := range strings.SplitSeq(string(raw), "\n") {
		code := strings.TrimSpace(line)
		if strings.HasPrefix(code, "#") {
			continue
		}
		if earlyExitGrep.MatchString(code) {
			hits = append(hits, code)
		}
	}
	return hits
}

// TestEarlyExitGrepPattern pins what the guard flags and what it leaves alone.
func TestEarlyExitGrepPattern(t *testing.T) {
	tests := []struct {
		line string
		want bool
	}{
		{`out | grep -q foo`, true},
		{`out |grep -qi foo`, true},
		{`out | grep -Fq foo`, true},
		{`out | matches -i foo`, false},
		{`grep -q foo "$file"`, false},
		{`grep -qxF "$want" <<<"$inventory"`, false},
		{`out | grep -c foo`, false},
	}
	for _, tt := range tests {
		if got := earlyExitGrep.MatchString(tt.line); got != tt.want {
			t.Errorf("match(%q) = %v, want %v", tt.line, got, tt.want)
		}
	}
}
