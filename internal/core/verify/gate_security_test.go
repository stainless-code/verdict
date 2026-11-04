package verifycore_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	verifycore "github.com/stainless-code/verdict/internal/core/verify"
)

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
		wantRefusal(t, `ls /`, "verify-repo-escape", "refusing verifyCmd: matched '/'"+absPathDetail)
		if ref := verifycore.Check(`cat /tmp/x`); ref != nil {
			t.Errorf("Check(/tmp/x) from /: %s, want clean (scratch carve-out)", ref.Code)
		}
	})
}

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

const absPathDetail = " (absolute path escapes the repo — use repo-relative paths, /tmp for scratch)"

// absAttachedDetail is the same refusal for a path the scanner read out of a
// value glued to its option (-C/etc): the value it quotes is the path it saw.
const absAttachedDetail = " (absolute path attaches to an option — separate the value, and use repo-relative paths or /tmp for scratch)"

func wantInsideRootClean(t *testing.T, wd string) {
	t.Helper()
	for _, cmd := range []string{`cat ` + wd + `/gate.golden`, `ls ` + wd, `cat "` + wd + `/gate.golden"`} {
		if ref := verifycore.Check(cmd); ref != nil {
			t.Errorf("Check(%q) = %s: %q, want clean (inside the invocation root)", cmd, ref.Code, ref.Message)
		}
	}
}

func wantOutsideRootRefused(t *testing.T, wd string) {
	t.Helper()
	for _, frag := range []string{wd + `-evil/x`, wd + `_sibling`} {
		wantRefusal(t, `cat `+frag, "verify-repo-escape", "refusing verifyCmd: matched '"+strings.ToLower(frag)+"'"+absPathDetail)
	}
	wantRefusal(t, `cat /etc/passwd`, "verify-repo-escape", "refusing verifyCmd: matched '/etc/passwd'"+absPathDetail)
}

// TestCheckUnreducibleSubstitutionHidesToolWord pins that a `$(` span the
// body patterns cannot match still refuses when it sits against a word: an
// inner bare paren (`$( (echo m) )`, `$(case x in (x) … esac)`) makes
// [reDollarParen] fail, so the bounded loops saw no pass to count and the old
// depth bound never fired. bash still concatenates the span into a word:
// `r$( (echo m) ) -rf victim` runs `rm -rf victim`.
//
// Break killed: dropping the gluedSpan call in normalize (manual) returns
// CLEAN for every row here.
func TestCheckUnreducibleSubstitutionHidesToolWord(t *testing.T) {
	t.Parallel()
	const detail = " (substitution the gate cannot reduce sits against a word — write the tool word plainly)"
	tests := []struct{ name, cmd string }{
		{"left glued subshell", `r$( (echo m) ) -rf x`},
		{"left glued case arm", `r$(case x in (x) echo m;; esac) -rf x`},
		{"left glued, nested inside", `r$( (echo $(echo m)) ) -rf x`},
		{"right glued subshell", `$( (echo r))m -rf x`},
		{"quoted left glued", `r"$( (echo m) )" -rf x`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			wantRefusal(t, tc.cmd, "verify-destructive",
				"refusing verifyCmd: matched 'unscannable-substitution'"+detail)
		})
	}
}

// TestCheckUnreducibleSubstitutionBenignStaysClean guards that arm against
// over-refusal: an unreduced span that touches no word character, arithmetic
// `$((…))`, an unterminated `$(` inside a pattern argument, and a quoted
// literal stay clean.
//
// Break killed: refusing every surviving `$(`/`${` instead of only a glued
// one (manual) refuses every row here.
func TestCheckUnreducibleSubstitutionBenignStaysClean(t *testing.T) {
	t.Parallel()
	for _, cmd := range []string{
		`$( (echo hi) )`,
		`echo $((1+1))`,
		`n=$((1+1)); echo $n`,
		`grep -F '$(rm' src/x.go`,
		`echo '$('`,
	} {
		if ref := verifycore.Check(cmd); ref != nil {
			t.Errorf("Check(%q) = %s: %q, want clean", cmd, ref.Code, ref.Message)
		}
	}
}

// TestCheckAttachedOptionValueMatchesSpaced pins that a value getopt attaches
// to its short option is judged as the same invocation as the spaced spelling:
// the arm's contract already refuses `/etc` as a word, and attaching it to
// `-C`, `-cf`, `-t`, `-o`, `-f`, `-I`, or `-X` must not change the verdict.
func TestCheckAttachedOptionValueMatchesSpaced(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, attached, spaced string }{
		{"tar -C", `tar -C/etc -cf /tmp/verify-attached.tar passwd`, `tar -C /etc -cf /tmp/verify-attached.tar passwd`},
		{"tar -cf cluster", `tar -cf/etc/verify-attached.tar .`, `tar -cf /etc/verify-attached.tar .`},
		{"tar -czf cluster", `tar -czf/etc/verify-attached.tgz .`, `tar -czf /etc/verify-attached.tgz .`},
		{"tar -T", `tar -T/etc/passwd -cf verify-attached.tar`, `tar -T /etc/passwd -cf verify-attached.tar`},
		{"tar -X", `tar -X/etc/passwd -cf verify-attached.tar .`, `tar -X /etc/passwd -cf verify-attached.tar .`},
		{"tar positional after --", `tar -cf verify-attached.tar -- -C/etc passwd`, `tar -cf verify-attached.tar -- -C /etc passwd`},
		{"cp -t", `cp notes.txt -t/etc/cron.d`, `cp notes.txt -t /etc/cron.d`},
		{"cp --target-directory", `cp --target-directory=/etc notes.txt`, `cp --target-directory /etc notes.txt`},
		{"sort -o", `sort -o/etc/verify-attached.out notes.txt`, `sort -o /etc/verify-attached.out notes.txt`},
		{"sort -T", `sort -T/etc -o verify-attached.out notes.txt`, `sort -T /etc -o verify-attached.out notes.txt`},
		{"grep -f", `grep -f/etc/passwd notes.txt`, `grep -f /etc/passwd notes.txt`},
		{"sed -f", `sed -f/etc/passwd notes.txt`, `sed -f /etc/passwd notes.txt`},
		{"awk -f", `awk -f/etc/passwd notes.txt`, `awk -f /etc/passwd notes.txt`},
		{"gcc -I", `gcc -I/usr/include -c notes.txt`, `gcc -I /usr/include -c notes.txt`},
		{"git -C", `git -C/etc status`, `git -C /etc status`},
		{"make -C", `make -C/etc test`, `make -C /etc test`},
		{"go -o", `go build -o/etc/verify-attached ./...`, `go build -o /etc/verify-attached ./...`},
		{"quoted value", `tar "-C/etc" -cf /tmp/verify-attached.tar passwd`, `tar "-C /etc" -cf /tmp/verify-attached.tar passwd`},
		{"escaped value", `tar -C\/etc -cf /tmp/verify-attached.tar passwd`, `tar -C \/etc -cf /tmp/verify-attached.tar passwd`},
		{"uppercase tool and path", `TAR -C/ETC -cf /tmp/verify-attached.tar passwd`, `TAR -C /ETC -cf /tmp/verify-attached.tar passwd`},
		{"dot-dot value", `tar -C../x -cf /tmp/verify-attached.tar .`, `tar -C ../x -cf /tmp/verify-attached.tar .`},
		{"tilde value", `tar -C~/verify-attached -cf /tmp/verify-attached.tar .`, `tar -C ~/verify-attached -cf /tmp/verify-attached.tar .`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertAttachedMatchesSpaced(t, tc.attached, tc.spaced)
		})
	}
}

// assertAttachedMatchesSpaced holds one row to the row's spaced baseline: the
// same code and the same quoted fragment.
func assertAttachedMatchesSpaced(t *testing.T, attached, spaced string) {
	t.Helper()
	spacedRef := verifycore.Check(spaced)
	if spacedRef == nil {
		t.Fatalf("Check(%q) = nil, want a refusal: the spaced spelling is this row's baseline", spaced)
	}
	attachedRef := verifycore.Check(attached)
	if attachedRef == nil {
		t.Fatalf("Check(%q) = nil, want %s like %q", attached, spacedRef.Code, spaced)
	}
	if attachedRef.Code != spacedRef.Code {
		t.Fatalf("Check(%q) = %s, want %s (the spaced spelling)", attached, attachedRef.Code, spacedRef.Code)
	}
	if got, want := matchedFragment(t, attachedRef.Message), matchedFragment(t, spacedRef.Message); got != want {
		t.Fatalf("Check(%q) quotes %q, want %q (the spaced spelling)", attached, got, want)
	}
}

// TestCheckAttachedOptionValueFragments pins the fragment the headline forms
// quote, so a refusal names the path the operator can act on.
func TestCheckAttachedOptionValueFragments(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, cmd, matched string }{
		{"tar -C", `tar -C/etc -cf /tmp/verify-attached.tar passwd`, "/etc"},
		{"tar -cf cluster", `tar -cf/etc/verify-attached.tar .`, "/etc/verify-attached.tar"},
		{"cp -t", `cp notes.txt -t/etc/cron.d`, "/etc/cron.d"},
		{"sort -o", `sort -o/etc/verify-attached.out notes.txt`, "/etc/verify-attached.out"},
		{"grep -f", `grep -f/etc/passwd notes.txt`, "/etc/passwd"},
		{"git -C", `git -C/etc status`, "/etc"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			wantRefusal(t, tc.cmd, "verify-repo-escape",
				"refusing verifyCmd: matched '"+tc.matched+"'"+absAttachedDetail)
		})
	}
}

// TestCheckAttachedOptionValueBenignStaysClean guards the split against
// over-refusal: relative attached values, dot-prefixed values, `=`-joined
// values, `--` separators, URLs, and the /tmp carve-out still execute.
func TestCheckAttachedOptionValueBenignStaysClean(t *testing.T) {
	t.Parallel()
	for _, cmd := range []string{
		`printf ok -Csub`,
		`printf ok -C./sub`,
		`printf ok -o./bin`,
		`printf ok -Iinclude`,
		`printf ok -DFOO=src/x`,
		`printf ok --prefix=src/x`,
		`printf ok --prefix=./x`,
		`printf ok --prefix=$FOO/x`,
		`FOO=-C/etc printf ok`,
		`printf -- ok`,
		`git diff -- src/x.go`,
		`printf ok >-C/etc`,
		`printf ok -C /tmp/verify-attached-scratch`,
		`curl -fsSL http://127.0.0.1:9/fixture`,
		`curl -fsSL -D - http://127.0.0.1:9/f`,
		`cat /tmp/verify-attached/some/path`,
	} {
		if ref := verifycore.Check(cmd); ref != nil {
			t.Errorf("Check(%q) = %s: %q, want clean", cmd, ref.Code, ref.Message)
		}
	}
}

// matchedFragment returns the text a refusal quotes between matched '…' and
// its trailing detail, so a test can compare two refusals on substance.
func matchedFragment(t *testing.T, msg string) string {
	t.Helper()
	start := strings.Index(msg, "matched '")
	end := strings.LastIndex(msg, "' (")
	if start < 0 || end < 0 || end < start {
		t.Fatalf("message %q quotes no fragment", msg)
	}
	return msg[start+len("matched '") : end]
}
