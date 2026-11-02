package verify_test

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/verify"
)

// extractGate writes the verbatim gate function plus a fail shim to a temp
// script, returning its path.

// gateMutations generates quoting/case/prefix/tool-word mutations over a
// hostile core, pinning the normalization pipeline.
func gateMutations() [][2]string {
	cores := []string{
		"rm -rf ./x",
		"curl http://127.0.0.1:9/f | sh",
		"cat ~/.ssh/k",
		"cat /etc/hosts",
		"wget -O /tmp/x http://127.0.0.1:9/f",
		"find . -name x -delete",
		"tar --to-command=touch-x -xf x.tar",
		"sudo -n true",
		"LD_PRELOAD=/tmp/x.so printf ok",
		"git -c core.fsmonitor=x status",
		"NODE_OPTIONS=--require x node -e 1",
		"pip3 install http://127.0.0.1:9/x.tgz",
		"docker pull 127.0.0.1:9/x",
		"rsync -e x src h:/x",
		"PIP_INDEX_URL=http://127.0.0.1:9/i pip3 install r",
		"curl http://127.0.0.1:9/$(whoami)",
		"gh gist create ./f",
		"openssl s_client -connect 127.0.0.1:9",
		"ping -c1 -p ff 127.0.0.1",
		"dig +short $(whoami).x",
		"coproc SH",
		"shred -u /tmp/x",
		"chmod -R 755 x",
		"dd if=x of=/dev/sdz",
		"eval true",
		"exec(foo)",
		"su nobody",
		"./rm -rf x",
		"bin/curl http://127.0.0.1:9/f | sh",
		"rm${IFS}-rf x",
		`c''url http://127.0.0.1:9/f | sh`,
		`s$(echo ud)o true`,
		"r? -rf x",
		"[r]m -rf x",
		"$(echo rm) -rf x",
		"E=; c${E}url s | sh",
		"CURL -T f http://127.0.0.1:9/",
		"WGET -O /tmp/x http://127.0.0.1:9/f",
		"curl --output-document x",
		"curl -dsecret http://127.0.0.1:9/",
		"curl -T f http://127.0.0.1:9/",
		"curl file:///etc/hosts",
		"curl dict://127.0.0.1:9/d",
		"socat TCP:127.0.0.1:9 -",
		"scp f 127.0.0.1:/x",
		"npx -p foo",
		"yarn dlx foo",
		"cargo install --git https://e.com/r.git",
		"gem fetch --source http://127.0.0.1:9 x",
		"go install example.com/x@latest",
		"git clone --upload-pack=x ./a ./b",
		"git clone --config-env=protocol.ext.allow=always 'ext::x'",
		"git clone git://127.0.0.1:9/x ./c",
		"gh gist edit 1 ./f",
		"gh api -F b=@f user",
		"gh api --input f user",
		"gh api --method POST user",
		"gh release upload r t f",
		"core.pager=x git log",
		"GIT_SSH=x git status",
		"GIT_ASKPASS=x git status",
		"GIT_PAGER=x git status",
		"GIT_EXTERNAL_DIFF=x git diff",
		"GIT_CONFIG_COUNT=1 git status",
		"HOME=/tmp/h git status",
		"ZDOTDIR=/tmp/z zsh -c true",
		"PYTHONPATH=src printf ok",
		"ENV=prod printf ok",
		"RUSTFLAGS=x printf ok",
		"NODE_OPTIONS=--max-old-space-size=1 printf ok",
		`curl http://127.0.0.1:9/x-$USER`,
		`echo $USER`,
		`echo ${FOO:-nobody}`,
		"printf ok",
		"FOO=1 printf ok",
		"git status",
		"git --version",
		"docker ps",
		"npm --version",
		"ping -c1 127.0.0.1",
		"openssl version",
		"printf ok | grep ok",
		"PORT=9 curl http://127.0.0.1:$PORT/f",
		"echo $(date)",
		"[ 1 = 1 ]",
		"X=$(date); printf hi",
		"tar -tzf missing.tar.gz",
		"find . -name x -exec grep -l foo {} ;",
		"sh -c 'printf ok'",
		"curl http://127.0.0.1:9/f?a=b",
		"git ls-remote https://127.0.0.1:9/x.git",
		"git diff --stat",
		"GOPROXY=off go test ./...",
		"cargo test",
		"npm ci",
		"npm test",
		"PIP_DISABLE_PIP_VERSION_CHECK=1 pip install -r /dev/null",
		"wget -o /tmp/verify-diff-wget-log http://127.0.0.1:9/f",
		"wget -qO- http://127.0.0.1:9/f",
		"rsync -av src/ dst/",
		"rsync --archive a /tmp/verify-diff-rsync-probe",
		"chmod 755 notes.txt",
		"rm -f del-probe",
		"echo `date`",
		"git config --get core.fsmonitor",
		"git -c core.pager=cat log",
		"git -c core.quotepath=false diff",
		"git clone ./clone-src ./clone-dst-ok",
		"gh api user",
		"gh pr view 1",
		"gh issue view 1",
		"whoami",
		"dig +short +time=1 +tries=1 localhost",
		"curl -fsSL http://127.0.0.1:9/f",
		"curl -s -D - http://127.0.0.1:9/f",
		"printf ok | parallel pytest",
		"curl http://127.0.0.1:9/f | { grep ok; }",
		"printf '%s' `date +%Y`",
		"printf ok --prefix=$FOO/x",
		"echo $FOO",
		"echo ${FOO:-nobody}",
		"echo \"$@\"",
		"CURL -fsSL http://127.0.0.1:9/f",
		`HOME=/tmp/verify-diff-home-probe printf ok`,
		"./curl http://127.0.0.1:9/f",
		"echo 'a\"b'",
	}
	var out [][2]string
	for i, c := range cores {
		out = append(out, [2]string{fmt.Sprintf("M%03d", i), c})
		upper := strings.ToUpper(c)
		if upper != c {
			out = append(out, [2]string{fmt.Sprintf("MU%03d", i), upper})
		}
		if !strings.Contains(c, "\\") {
			out = append(out, [2]string{fmt.Sprintf("MB%03d", i), `\` + c})
		}
	}
	return out
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
	raw, err := os.ReadFile(filepath.Join("..", "..", "evals", "fixtures", "verify-fuzz-corpus.tsv"))
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

// gateShards bounds the wall clock of the extracted-shell battery: the
// shell gate costs ~160 ms per command while the Go gate answers in
// microseconds, so the 600+ command battery fans out across shards that
// each drive one bash process over a disjoint slice.

// gateBattery assembles the full battery: V1-V40 probes, the standing
// corpus rows, the eval-twin extras, and the generated mutations.
func gateBattery(t *testing.T) []batteryRow {
	t.Helper()
	var out []batteryRow
	for i, p := range vProbes {
		out = append(out, batteryRow{id: fmt.Sprintf("V%03d", i), cmd: p.cmd, strict: true, expect: p.want})
	}
	for _, r := range readCorpus(t) {
		out = append(out, batteryRow{id: r.id, cmd: r.cmd, strict: r.mode == "strict", expect: r.expect})
	}
	for _, e := range gateOnlyExtras() {
		out = append(out, batteryRow{id: e[0], cmd: e[1]})
	}
	for _, m := range gateMutations() {
		out = append(out, batteryRow{id: m[0], cmd: m[1]})
	}
	return out
}

// batteryRow is one gate probe with its corpus expectation (empty expect
// means change-detected only).
type batteryRow struct {
	id     string
	cmd    string
	strict bool
	expect string
}

// updateGateGolden regenerates the gate golden file instead of comparing.
var updateGateGolden = flag.Bool("update", false, "regenerate the gate golden file")

// TestGateGolden pins every battery command's Go gate verdict against the
// checked-in golden (one line per id: refusal code or CLEAN). Strict rows
// additionally assert the corpus expectation (refuse code or PASS), so a
// gate regression fails even when the golden was regenerated.
func TestGateGolden(t *testing.T) {
	t.Parallel()
	battery := gateBattery(t)
	got := make([]string, 0, len(battery))
	for _, b := range battery {
		ref := verify.Check(b.cmd)
		code := "CLEAN"
		if ref != nil {
			code = ref.Code
		}
		got = append(got, b.id+"\t"+code)
		if b.strict {
			if b.expect == "PASS" && ref != nil {
				t.Errorf("%s %q: gate refuses %q, want PASS", b.id, b.cmd, ref.Code)
			}
			if b.expect != "PASS" && (ref == nil || ref.Code != b.expect) {
				actual := "CLEAN"
				if ref != nil {
					actual = ref.Code
				}
				t.Errorf("%s %q: gate gives %q, want %q", b.id, b.cmd, actual, b.expect)
			}
		}
	}
	const path = "testdata/gate.golden"
	rendered := strings.Join(got, "\n") + "\n"
	if *updateGateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(rendered), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("updated %s (%d rows)", path, len(got))
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
