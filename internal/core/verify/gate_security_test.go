package verifycore_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	verifycore "github.com/stainless-code/verdict/internal/core/verify"
)

// wantRefusal asserts the gate refuses cmd with the exact code and message.
func wantRefusal(t *testing.T, cmd, code, message string) {
	t.Helper()
	ref := verifycore.Check(cmd)
	if ref == nil {
		t.Fatalf("Check(%q) = nil, want refusal %s", cmd, code)
	}
	if ref.Code != code || ref.Message != message {
		t.Fatalf("Check(%q) = %s: %q\nwant      %s: %q", cmd, ref.Code, ref.Message, code, message)
	}
}

// TestCheckRefusalMessages pins the matched fragment each arm quotes — the
// text an operator reads to see WHY a command was refused. Expected strings
// are literals (the manual's "quotes the matched fragment" contract), not
// derived from the gate.
//
// Mutants killed (gremlins, internal/core/verify):
//   - gate_destructive.go:23 CONDITIONALS_NEGATION: row "mid-word"
//     (mutant overwrites the real fragment with "mid-word-substitution").
//   - gate_destructive.go:65 CONDITIONALS_NEGATION: row "glob first word".
//   - gate_destructive.go:92 CONDITIONALS_NEGATION: row "glob later word"
//     (mutant stops at the non-glob first word and falls back to the generic
//     "globbed-command-word").
//   - normalize.go:170 INVERT_NEGATIVES / ARITHMETIC_BASE (grepO limit
//     -1 -> 1): row "two matches" (second match line is dropped).
func TestCheckRefusalMessages(t *testing.T) {
	t.Parallel()
	const midWord = "mid-word substitution concatenates into a hidden tool word — write the tool word plainly"
	const glob = "glob metacharacters in command position expand into a hidden tool word — write the tool word plainly"
	tests := []struct {
		name, cmd, code, message string
	}{
		{
			name: "mid-word", cmd: `s$(echo ud)o -n true`, code: "verify-destructive",
			message: "refusing verifyCmd: matched 's?o -n true' (" + midWord + ")",
		},
		{
			name: "glob first word", cmd: `r? -rf x`, code: "verify-destructive",
			message: "refusing verifyCmd: matched 'r?' (" + glob + ")",
		},
		{
			name: "glob later word", cmd: `echo a; r* -rf x`, code: "verify-destructive",
			message: "refusing verifyCmd: matched 'r*' (" + glob + ")",
		},
		{
			name: "two matches", cmd: `git -c protocol.ext.allow=always ls-remote ext::x`, code: "verify-destructive",
			message: "refusing verifyCmd: matched 'protocol.ext.allow\next::x' " +
				"(git ext transport executes a local command — fetch fixtures first, run offline)",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			wantRefusal(t, tc.cmd, tc.code, tc.message)
		})
	}
}

// nestedSubst builds r$(echo $(echo … m)) -rf x with depth $(…) levels: a
// bash word that expands to `rm -rf x` (the shell evaluates the innermost
// echo first, so the glued r + m = rm).
func nestedSubst(depth int) string {
	return "r" + strings.Repeat("$(echo ", depth) + "m" + strings.Repeat(")", depth) + " -rf x"
}

// TestCheckNestedSubstitutionHidesToolWord pins that a tool word hidden
// behind nested command substitution is refused at every nesting depth, not
// only the first few. Before the fail-closed fix, depth 9+ left unreplaced
// $(…) spans in the scanned text and the gate returned CLEAN for a command
// bash expands to `rm -rf x`.
//
// Mutants / breaks killed:
//   - normalize.go:142 CONDITIONALS_NEGATION (`nxt == s` -> `!=`, only one
//     substitution pass): depth 2 stays `r$(echo ?)` with no glued sentinel.
//   - deleting the deepSubst arm in checkSentinel (manual break): depths 9
//     and 10 return CLEAN again.
func TestCheckNestedSubstitutionHidesToolWord(t *testing.T) {
	t.Parallel()
	const midWord = "mid-word substitution concatenates into a hidden tool word — write the tool word plainly"
	const deep = "substitution nesting too deep to scan for a hidden tool word — write the tool word plainly"
	tests := []struct {
		name    string
		message string
		depth   int
	}{
		{"one level", "refusing verifyCmd: matched 'r? -rf x' (" + midWord + ")", 1},
		{"two levels", "refusing verifyCmd: matched 'r? -rf x' (" + midWord + ")", 2},
		{"eight levels, the scan bound", "refusing verifyCmd: matched 'r? -rf x' (" + midWord + ")", 8},
		{"nine levels, past the bound", "refusing verifyCmd: matched 'nested-substitution' (" + deep + ")", 9},
		{"ten levels", "refusing verifyCmd: matched 'nested-substitution' (" + deep + ")", 10},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			wantRefusal(t, nestedSubst(tc.depth), "verify-destructive", tc.message)
		})
	}
}

// TestCheckNestedSubstitutionBenignStaysClean guards the fix against
// over-refusal: ordinary nested substitution that does not glue onto a word
// still executes.
//
// Break killed: making deepSubst true unconditionally (manual break) refuses
// these commands.
func TestCheckNestedSubstitutionBenignStaysClean(t *testing.T) {
	t.Parallel()
	for _, cmd := range []string{
		`echo $(echo $(date))`,
		`echo $(echo $(echo $(echo $(echo $(echo $(echo $(echo hi)))))))`,
		`printf '%s' "${FOO:-${BAR:-x}}"`,
	} {
		if ref := verifycore.Check(cmd); ref != nil {
			t.Errorf("Check(%q) = %s: %q, want clean", cmd, ref.Code, ref.Message)
		}
	}
}

// TestCheckEveryAbsolutePathIsJudged pins that the absolute-path arm judges
// every candidate in the command, not just the first one: an allowed
// /tmp scratch path in front must not shield an escaping path behind it.
//
// Mutants killed: gate_escape.go:43 INVERT_NEGATIVES and ARITHMETIC_BASE
// (FindAllString limit -1 -> 1: only the first path is examined, so every
// row below is CLEAN under the mutant).
func TestCheckEveryAbsolutePathIsJudged(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, cmd, matched string }{
		{"tmp then etc", `cat /tmp/a /etc/passwd`, "/etc/passwd"},
		{"two tmp then var", `cat /tmp/a /tmp/b /var/log/x`, "/var/log/x"},
		{"redirect after tmp", `cp /tmp/a /tmp/b; cat < /etc/shadow`, "/etc/shadow"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			wantRefusal(t, tc.cmd, "verify-repo-escape", "refusing verifyCmd: matched '"+tc.matched+"'"+absPathDetail)
		})
	}
}

// TestCheckAbsolutePathRoot pins the invocation-root comparison from a known
// working directory: paths under the cwd execute, look-alike siblings and
// foreign paths refuse, and a cwd of "/" never turns the whole filesystem
// into "inside the repo". It chdirs, so it is not parallel (service_test.go
// chdirs the same way).
//
// Mutants killed:
//   - gate_escape.go:48 CONDITIONALS_NEGATION (`err == nil` -> `!=`: the cwd
//     is never read, lroot stays "", the in-root rows refuse).
//   - gate_escape.go:75:18 CONDITIONALS_NEGATION (`lroot != ""` -> `==`).
//   - gate_escape.go:75:33 CONDITIONALS_NEGATION (`lroot != "/"` -> `==`):
//     the in-root rows refuse.
//   - dropping the `lroot != "/"` guard (manual break): the "/" cwd subtest
//     lets `ls /` through (a cwd of "/" would equal the candidate "/").
func TestCheckAbsolutePathRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the gate matches POSIX absolute paths; Getwd is drive-lettered on Windows")
	}
	t.Run("repo cwd", func(t *testing.T) {
		wd := chdirIntoRepoDir(t)
		wantInsideRootClean(t, wd)
		wantOutsideRootRefused(t, wd)
	})
	t.Run("slash cwd", func(t *testing.T) {
		t.Chdir("/")
		wantRefusal(t, `cat /etc/passwd`, "verify-repo-escape", "refusing verifyCmd: matched '/etc/passwd'"+absPathDetail)
		// The filesystem root itself is never "the repo", even when it is the cwd.
		wantRefusal(t, `ls /`, "verify-repo-escape", "refusing verifyCmd: matched '/'"+absPathDetail)
		if ref := verifycore.Check(`cat /tmp/x`); ref != nil {
			t.Errorf("Check(/tmp/x) from /: %s, want clean (scratch carve-out)", ref.Code)
		}
	})
}

// chdirIntoRepoDir moves into the package's testdata (a repo dir, not under
// /tmp) and returns the working directory the gate will read.
func chdirIntoRepoDir(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(wd, "/tmp/") {
		t.Skipf("checkout under /tmp (%s) is indistinguishable from the scratch carve-out", wd)
	}
	return wd
}

// absPathDetail is the absolute-path refusal's parenthesised hint.
const absPathDetail = " (absolute path escapes the repo — use repo-relative paths, /tmp for scratch)"

// wantInsideRootClean asserts paths under the invocation root execute.
func wantInsideRootClean(t *testing.T, wd string) {
	t.Helper()
	for _, cmd := range []string{`cat ` + wd + `/gate.golden`, `ls ` + wd, `cat "` + wd + `/gate.golden"`} {
		if ref := verifycore.Check(cmd); ref != nil {
			t.Errorf("Check(%q) = %s: %q, want clean (inside the invocation root)", cmd, ref.Code, ref.Message)
		}
	}
}

// wantOutsideRootRefused asserts look-alike siblings and foreign paths refuse.
func wantOutsideRootRefused(t *testing.T, wd string) {
	t.Helper()
	for _, frag := range []string{wd + `-evil/x`, wd + `_sibling`} {
		wantRefusal(t, `cat `+frag, "verify-repo-escape", "refusing verifyCmd: matched '"+strings.ToLower(frag)+"'"+absPathDetail)
	}
	wantRefusal(t, `cat /etc/passwd`, "verify-repo-escape", "refusing verifyCmd: matched '/etc/passwd'"+absPathDetail)
}
