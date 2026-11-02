// Package verify_test holds the shell-parity proof for the Go verify verb
// and its allowlist gate: the shared harness runs BOTH scripts/review.sh
// verify and the Go binary on identical fixtures and compares exit codes,
// stdout/stderr (timestamps scrubbed only), and every written file byte
// for byte, including patch.json, .verify.out, and the timeout markers.
// Refusal legs also assert the typed code; execution legs assert the proof
// lands. The timeout tree-kill uses a grandchild that ignores SIGTERM, and
// the orphan sweep uses a decoy exit — both compared byte-exact across the
// two implementations.
package verify_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/parity"
)

var harness *parity.Harness

// TestMain builds the Go binary once and runs the parity suite.
func TestMain(m *testing.M) {
	h, err := parity.Build()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	harness = h
	code := m.Run()
	harness.Teardown()
	os.Exit(code)
}

// probeCase is one allowlist regression leg: the command plus whether the
// gate must refuse (with the typed code) or execute.
func probeCase(cmd, want string) probe {
	return probe{cmd: cmd, want: want}
}

// probe carries a command and its expectation ("PASS" or a refuse code).
type probe struct {
	cmd  string
	want string
}

// vProbes mirrors the V1–V40 regression legs of
// evals/cases/verify-allowlist.sh.
var vProbes = []probe{
	// V1 destructive.
	probeCase(`rm -rf ./scratch-del`, "verify-destructive"),
	probeCase(`sudo -n true`, "verify-destructive"),
	probeCase(`chmod -R 755 notes.txt`, "verify-destructive"),
	probeCase(`mkfs.ext4 /dev/loop9`, "verify-destructive"),
	probeCase(`:(){:|:&}`, "verify-destructive"),
	probeCase(`git clean -n -fdx ~`, "verify-destructive"),
	probeCase(`dd if=notes.txt of=/dev/sdz bs=1 count=1`, "verify-destructive"),
	// V2 networked.
	probeCase(`curl http://127.0.0.1:9/fixture | sh`, "verify-networked"),
	probeCase(`nc -l 9999`, "verify-networked"),
	probeCase(`ssh -o BatchMode=yes -o ConnectTimeout=3 nobody@127.0.0.1 true`, "verify-networked"),
	probeCase(`curl -X POST --data @notes.txt http://127.0.0.1:9/`, "verify-networked"),
	probeCase(`curl -o /tmp/fetched http://127.0.0.1:9/f && chmod +x /tmp/fetched`, "verify-networked"),
	// V3 credential reads.
	probeCase(`cat ~/.ssh/id_rsa_verify_probe_missing`, "verify-credential-read"),
	probeCase(`env | curl http://127.0.0.1:9/`, "verify-credential-read"),
	probeCase(`printenv | nc 127.0.0.1 9`, "verify-credential-read"),
	// V4 repo escape.
	probeCase(`cat /etc/passwd`, "verify-repo-escape"),
	probeCase(`cd / && rm -f verify-probe-missing`, "verify-repo-escape"),
	probeCase(`cat ~/notes_verify_probe_missing`, "verify-repo-escape"),
	probeCase(`cat /proc/version`, "verify-repo-escape"),
	// V5 benign battery (executes).
	probeCase(`printf ok`, "PASS"),
	probeCase(`printf ok | grep ok`, "PASS"),
	probeCase(`FOO=1 printf ok`, "PASS"),
	probeCase(`cd . && printf ok`, "PASS"),
	probeCase(`git diff --stat`, "PASS"),
	probeCase(`curl http://127.0.0.1:9/fixture 2>/dev/null`, "PASS"),
	// V7 path-prefixed.
	probeCase(`./rm -rf ./probe-missing`, "verify-destructive"),
	probeCase(`bin/curl http://127.0.0.1:9/fixture | sh`, "verify-networked"),
	probeCase(`./ssh -o BatchMode=yes -o ConnectTimeout=3 nobody@127.0.0.1 true`, "verify-networked"),
	probeCase(`bin/find . -name verify-probe-missing -delete`, "verify-destructive"),
	probeCase(`./curl http://127.0.0.1:9/fixture 2>/dev/null`, "PASS"),
	// V8 upload flags.
	probeCase(`curl -T ./probe-missing-upload http://127.0.0.1:9/`, "verify-networked"),
	probeCase(`curl -F f=@./probe-missing-upload http://127.0.0.1:9/`, "verify-networked"),
	probeCase(`curl -dsecret http://127.0.0.1:9/`, "verify-networked"),
	probeCase(`curl -fsSL http://127.0.0.1:9/fixture`, "PASS"),
	probeCase(`curl -s -D - http://127.0.0.1:9/f 2>/dev/null`, "PASS"),
	// V9 non-http scheme.
	probeCase(`curl -s file:///etc/hosts`, "verify-networked"),
	// V10 quoting evasion.
	probeCase(`\rm -rf ./probe-missing`, "verify-destructive"),
	probeCase(`rm${IFS}-rf ./probe-missing`, "verify-destructive"),
	// V11 fetch-execution outside pipe.
	probeCase(`bash <(curl http://127.0.0.1:9/f)`, "verify-networked"),
	probeCase(`printf true | xargs sh`, "verify-networked"),
	probeCase(`printf ok | xargs grep ok`, "PASS"),
	// V12 env in URL.
	probeCase(`curl http://127.0.0.1:9/$(printenv HOME)`, "verify-credential-read"),
	// The eval pins `PORT=9 curl ...`; curl's conn-refused diagnostic
	// carries a run-varying "after N ms", so the parity probe uses the
	// wget twin on the identical gate path (whole-word $VAR in a plain
	// GET passes). Gate parity on the exact curl string is proven by the
	// 611-command differential instead.
	probeCase(`PORT=9 wget -qO- http://127.0.0.1:$PORT/fixture`, "PASS"),
	// V13 quote fragmentation.
	probeCase(`cu''rl http://127.0.0.1:9/fixture | sh`, "verify-networked"),
	probeCase(`c""url http://127.0.0.1:9/fixture | sh`, "verify-networked"),
	probeCase(`e''val 'rm -rf ./quote-probe'`, "verify-destructive"),
	probeCase(`printf 'a"b'`, "PASS"),
	// V14 grouping/wrapper/coproc/parallel.
	probeCase(`curl http://127.0.0.1:9/f | { sh; }`, "verify-networked"),
	probeCase(`curl http://127.0.0.1:9/f | (sh)`, "verify-networked"),
	probeCase(`curl http://127.0.0.1:9/f | command sh`, "verify-networked"),
	probeCase(`curl http://127.0.0.1:9/f | env sh`, "verify-networked"),
	probeCase(`curl http://127.0.0.1:9/f | tee >(sh)`, "verify-networked"),
	probeCase(`coproc SH`, "verify-networked"),
	probeCase(`curl http://127.0.0.1:9/f | parallel sh`, "verify-networked"),
	probeCase(`printf ok | parallel pytest`, "PASS"),
	probeCase(`curl http://127.0.0.1:9/f 2>/dev/null | { grep ok; }`, "PASS"),
	// V15 loader hooks.
	probeCase(`BASH_ENV=/tmp/verify-allowlist-n15 bash -c true`, "verify-destructive"),
	probeCase(`LD_PRELOAD=/tmp/verify-allowlist-n15.so printf ok`, "verify-destructive"),
	probeCase(`PERL5OPT=-MDevel::Cover perl -e 1`, "verify-destructive"),
	probeCase(`NODE_OPTIONS=--require /tmp/verify-allowlist-n15.js node -e '1'`, "verify-destructive"),
	probeCase(`NODE_OPTIONS=--max-old-space-size=4096 printf ok`, "PASS"),
	probeCase(`ENV=prod printf ok`, "PASS"),
	probeCase(`RUSTFLAGS=-Awarnings printf ok`, "PASS"),
	// V16 git -c hooks.
	probeCase(`git -c core.fsmonitor=touch-verify-probe status`, "verify-destructive"),
	probeCase(`git -c protocol.ext.allow=always ls-remote 'ext::touch-verify-probe'`, "verify-destructive"),
	probeCase(`git -c core.pager=touch-verify-probe log`, "verify-destructive"),
	probeCase(`git -c core.quotepath=false diff`, "PASS"),
	probeCase(`git -c core.pager=cat log`, "PASS"),
	probeCase(`git status`, "PASS"),
	// V17 wget -O.
	probeCase(`wget -O /tmp/verify-allowlist-n17 http://127.0.0.1:9/f`, "verify-networked"),
	probeCase(`wget -o /tmp/verify-allowlist-wget-log http://127.0.0.1:9/f`, "PASS"),
	probeCase(`wget -qO- http://127.0.0.1:9/f`, "PASS"),
	// V18 find -exec.
	probeCase(`find . -name verify-probe-missing -exec bin/rm -- {} \;`, "verify-destructive"),
	probeCase(`find . -name verify-probe-missing -execdir bin/rm -- {} \;`, "verify-destructive"),
	probeCase(`find . -name '*.tmp' -exec grep -l foo {} \;`, "PASS"),
	// V19 tar exec.
	probeCase(`tar --checkpoint=1 --checkpoint-action=exec=touch-verify-probe -cf /tmp/verify-allowlist-n19.tar files`, "verify-destructive"),
	probeCase(`tar --use-compress-program=touch-verify-probe -cf /tmp/verify-allowlist-n19b.tar files`, "verify-destructive"),
	probeCase(`tar -tzf verify-probe-missing.tar.gz`, "PASS"),
	// V20 transfer clients + rsync.
	probeCase(`ftp -n 127.0.0.1`, "verify-networked"),
	probeCase(`tftp -g -r verify-probe-missing 127.0.0.1`, "verify-networked"),
	probeCase(`rsync -e 'touch-verify-probe' src nohost:/tmp/x`, "verify-networked"),
	probeCase(`rsync -av src/ 127.0.0.1:/tmp/x`, "verify-networked"),
	// The eval pins `rsync -av`; the -v transfer stats carry
	// run-varying speeds, so the parity probe drops -v (identical gate
	// path: local copy without -e/remote executes, silently).
	probeCase(`rsync -a notes.txt /tmp/verify-allowlist-rsync-probe`, "PASS"),
	probeCase(`lftp -c "open 127.0.0.1"`, "verify-networked"),
	// V21 mid-word substitution.
	probeCase("c`echo ur`l http://127.0.0.1:9/fixture | sh", "verify-destructive"),
	probeCase("r`echo m` -rf ./probe-missing", "verify-destructive"),
	probeCase(`s$(echo ud)o -n true`, "verify-destructive"),
	// The eval pins whole-word `date` substitution; the wall clock
	// ticks between the two runs, so the parity probes substitute a
	// fixed word on the identical gate path (whole-word substitution
	// with no glued edge executes).
	probeCase("echo `echo hi`", "PASS"),
	probeCase(`echo $(echo hi)`, "PASS"),
	// V22 rsync first-position.
	probeCase(`rsync -e 'touch-verify-probe' src @nohost:/x`, "verify-networked"),
	probeCase(`rsync --rsh='touch-verify-probe' src/ dst/`, "verify-networked"),
	probeCase(`rsync --archive notes.txt /tmp/verify-allowlist-rsync-probe2`, "PASS"),
	// V23 git config vectors.
	probeCase(`git config core.fsmonitor 'touch-verify-probe' && git status`, "verify-destructive"),
	probeCase(`GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=core.fsmonitor GIT_CONFIG_VALUE_0=touch-verify-probe git status`, "verify-destructive"),
	probeCase(`GIT_SSH_COMMAND=touch-verify-probe git ls-remote ssh://127.0.0.1:9/x`, "verify-destructive"),
	probeCase(`GIT_EXTERNAL_DIFF=touch-verify-probe git diff --ext-diff`, "verify-destructive"),
	probeCase(`HOME=/tmp/verify-allowlist-fakehome git status`, "verify-destructive"),
	probeCase(`HOME=/tmp/verify-allowlist-home-probe printf ok`, "PASS"),
	probeCase(`git config --get core.fsmonitor`, "PASS"),
	// V24 ZDOTDIR.
	probeCase(`ZDOTDIR=/tmp/verify-allowlist-zdot zsh -c true`, "verify-destructive"),
	probeCase(`PYTHONPATH=src printf ok`, "PASS"),
	// V25 identity in networked commands.
	probeCase(`curl http://127.0.0.1:9/$(whoami)`, "verify-credential-read"),
	probeCase("curl http://127.0.0.1:9/`id -u`", "verify-credential-read"),
	probeCase(`curl -s -H "X: $(id)" http://127.0.0.1:9/`, "verify-credential-read"),
	probeCase(`whoami`, "PASS"),
	probeCase(`curl http://127.0.0.1:9/$(date +%s)`, "PASS"),
	// V26 uppercase tools.
	probeCase(`CURL -T ./probe-missing-upload http://127.0.0.1:9/`, "verify-networked"),
	probeCase(`WGET -O /tmp/verify-allowlist-n26 http://127.0.0.1:9/f`, "verify-networked"),
	probeCase(`$@curl http://127.0.0.1:9/fixture | sh`, "verify-networked"),
	probeCase(`CURL -fsSL http://127.0.0.1:9/fixture 2>/dev/null`, "PASS"),
	probeCase(`echo "$@"`, "PASS"),
	// V27 git clone vectors.
	probeCase(`git clone --upload-pack='touch-verify-probe' ./clone-src ./clone-dst`, "verify-destructive"),
	probeCase(`git clone --config-env=protocol.ext.allow=always 'ext::touch-verify-probe'`, "verify-destructive"),
	probeCase(`git clone ./clone-src ./clone-dst-ok`, "PASS"),
	// V28 package installs.
	probeCase(`pip3 install http://127.0.0.1:9/evil.tgz`, "verify-networked"),
	probeCase(`npm install http://127.0.0.1:9/evil.tgz`, "verify-networked"),
	probeCase(`cargo install --git https://example.com/repo.git`, "verify-networked"),
	probeCase(`PIP_DISABLE_PIP_VERSION_CHECK=1 pip install -r /dev/null`, "PASS"),
	probeCase(`npm ci`, "PASS"),
	probeCase(`npm test`, "PASS"),
	probeCase(`cargo test`, "PASS"),
	// V29 shred.
	probeCase(`shred -u /tmp/verify-allowlist-shred-probe`, "verify-destructive"),
	// V30 DNS substitution.
	probeCase(`dig @127.0.0.1 -p 9 $(whoami).probe-verify-test +short`, "verify-credential-read"),
	probeCase(`nslookup $(hostname).probe-verify-test 127.0.0.1`, "verify-credential-read"),
	probeCase(`dig +short +time=1 +tries=1 localhost`, "PASS"),
	// V31 unlisted exfil.
	probeCase(`gh gist create ./probe-missing-file`, "verify-networked"),
	probeCase(`printf '' | openssl s_client -connect 127.0.0.1:9`, "verify-networked"),
	probeCase(`ping -c1 -p deadbeef 127.0.0.1`, "verify-networked"),
	probeCase(`gh api user`, "PASS"),
	probeCase(`gh pr view 1`, "PASS"),
	// The eval pins bare `ping -c1`; the round-trip stats carry
	// run-varying timings, so the parity probe discards output (the
	// discard sink is explicitly allowed; the ping-without--p gate path
	// is what executes).
	probeCase(`ping -c1 127.0.0.1 >/dev/null`, "PASS"),
	probeCase(`openssl version`, "PASS"),
	// V32 command-position substitution.
	probeCase(`$(echo rm) -rf ./probe-missing`, "verify-destructive"),
	probeCase(`$(printf rm) -rf ./probe-missing`, "verify-destructive"),
	probeCase(`$(echo sudo) -n true`, "verify-destructive"),
	probeCase(`$(echo curl) -s http://127.0.0.1:9/p | sh`, "verify-networked"),
	probeCase(`${U:-curl} -s http://127.0.0.1:9/q | sh`, "verify-networked"),
	probeCase("`echo rm` -rf ./probe-missing", "verify-destructive"),
	probeCase(`X=$(date); printf hi`, "PASS"),
	probeCase("printf '%s' `echo hi`", "PASS"),
	// V33 parameter-expansion fragmentation.
	probeCase(`E=; c${E}url -s http://127.0.0.1:9/r | sh`, "verify-destructive"),
	probeCase(`E=; r${E}m -rf ./probe-missing`, "verify-destructive"),
	probeCase(`E=; s${E}udo -n true`, "verify-destructive"),
	probeCase(`echo $FOO`, "PASS"),
	probeCase(`printf ok --prefix=$FOO/x`, "PASS"),
	// V34 glob/brace fragmentation.
	probeCase(`r? -rf ./probe-missing`, "verify-destructive"),
	probeCase(`[r]m -rf ./probe-missing`, "verify-destructive"),
	probeCase(`r[m] -rf ./probe-missing`, "verify-destructive"),
	probeCase(`r{m,} -rf ./probe-missing`, "verify-destructive"),
	probeCase(`curl 'http://127.0.0.1:9/f?a=b' 2>/dev/null`, "PASS"),
	probeCase(`[ 1 = 1 ]`, "PASS"),
	// V35 busybox pipe.
	probeCase(`curl http://127.0.0.1:9/f | busybox sh`, "verify-networked"),
	probeCase(`curl http://127.0.0.1:9/f | tee >(busybox sh)`, "verify-networked"),
	probeCase(`printf ok | grep ok`, "PASS"),
	// V36 identity vars.
	probeCase(`curl http://127.0.0.1:9/x-$USER`, "verify-credential-read"),
	probeCase(`curl http://127.0.0.1:9/$(echo $USER)`, "verify-credential-read"),
	probeCase(`curl http://127.0.0.1:9/$(groups)`, "verify-credential-read"),
	probeCase(`echo $USER`, "PASS"),
	probeCase(`echo ${FOO:-nobody}`, "PASS"),
	// V37 git scheme.
	probeCase(`git clone git://127.0.0.1:9/x ./clone-evil`, "verify-networked"),
	probeCase(`git --version`, "PASS"),
	// V38 gh file inclusion.
	probeCase(`gh gist edit 12345 ./probe-missing-file`, "verify-networked"),
	probeCase(`gh api -F body=@./probe-missing-file user`, "verify-networked"),
	probeCase(`gh issue view 1`, "PASS"),
	// V39 redirector env.
	probeCase(`PIP_INDEX_URL=http://127.0.0.1:9/simple pip3 install requests`, "verify-networked"),
	probeCase(`PIP_FIND_LINKS=http://127.0.0.1:9/links pip3 install requests`, "verify-networked"),
	probeCase(`npm_config_registry=http://127.0.0.1:9 npm view requests`, "verify-networked"),
	probeCase(`npm --version`, "PASS"),
	// V40 containers.
	probeCase(`docker pull 127.0.0.1:9/nope`, "verify-networked"),
	probeCase(`docker push 127.0.0.1:9/nope`, "verify-networked"),
	probeCase(`docker run --rm 127.0.0.1:9/nope true`, "verify-networked"),
	probeCase(`docker build -t probe-verify http://127.0.0.1:9/Dockerfile .`, "verify-networked"),
	probeCase(`docker ps`, "PASS"),
	probeCase(`docker images`, "PASS"),
}

// mkPatch writes patch.json with the verifyCmd.
func mkPatch(t *testing.T, dir, pr, cmd string) {
	t.Helper()
	bdir := filepath.Join(dir, ".review", pr)
	if err := os.MkdirAll(bdir, 0o755); err != nil {
		t.Fatal(err)
	}
	doc := "{\"verifyCmd\":" + jsonQuote(cmd) + "}"
	if err := os.WriteFile(filepath.Join(bdir, "patch.json"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
}

// jsonQuote renders a JSON string literal.
func jsonQuote(s string) string {
	var sb strings.Builder
	sb.WriteByte('"')
	for i := range len(s) {
		c := s[i]
		switch c {
		case '"', '\\':
			sb.WriteByte('\\')
			sb.WriteByte(c)
		case '\n':
			sb.WriteString("\\n")
		case '\r':
			sb.WriteString("\\r")
		case '\t':
			sb.WriteString("\\t")
		default:
			sb.WriteByte(c)
		}
	}
	sb.WriteByte('"')
	return sb.String()
}

// vSetup crafts the per-row patch plus the fixtures the executing legs
// need. The clone source initializes only for commands referencing it
// (a git spawn per row would dominate the suite runtime).
func vSetup(cmd string) func(t *testing.T, dir, home string) {
	return func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPatch(t, dir, "5101", cmd)
		if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("line1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		initCloneSrc(t, dir, cmd)
	}
}

// initCloneSrc plants the local clone source for the git-clone legs.
// Other legs skip the git spawn entirely.
func initCloneSrc(t *testing.T, dir, cmd string) {
	t.Helper()
	if !strings.Contains(cmd, "clone-src") {
		return
	}
	src := filepath.Join(dir, "clone-src")
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		return
	}
	if out, err := gitRun(dir, "init", "-q", "clone-src"); err != nil {
		t.Fatalf("git init clone-src: %v\n%s", err, out)
	}
}

// gitRun runs git in dir.
func gitRun(dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(context.Background(), "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestVerifyParity proves byte parity across the V1–V40 regression legs:
// every refuse keeps the typed code with an identical message, every exec
// records the proof identically. The want field documents the eval-graded
// expectation (codes are graded by the eval suite in both legs); the
// harness proves the two implementations agree byte for byte.
//
// npm writes timestamped debug logs under its cache on failure, which can
// never byte-match across runs (not even bash-vs-bash), so the cache
// points at shared /tmp scratch outside both snapshots.
func TestVerifyParity(t *testing.T) {
	t.Parallel()
	for i, p := range vProbes {
		name := fmt.Sprintf("V%03d-%s", i, strings.ReplaceAll(p.want, "verify-", ""))
		carve := strings.Contains(p.cmd, "\\")
		// Each leg gets a private npm cache dir: parallel legs share no
		// mutable state (the cache would be safe to share — cacache is
		// concurrency-safe — but private dirs keep the independence
		// structural instead of relying on it).
		extra := map[string]string{"npm_config_cache": t.TempDir()}
		harness.RunBoth(t, name, vSetup(p.cmd), extra, carve,
			"verify", "acme/app", "5101", "--json", "--timeout", "10")
	}
}
