package verifycore_test

import (
	"testing"

	verifycore "github.com/stainless-code/verdict/internal/core/verify"
)

type staticHardeningCase struct {
	name string
	cmd  string
	code string
}

var staticHardeningRefusals = []staticHardeningCase{
	{"wget remote-name", `wget --remote-name http://127.0.0.1:9/f`, "verify-networked"},
	{"curl config", `curl -K /tmp/verify-fuzz-curlcfg http://127.0.0.1:9/`, "verify-networked"},
	{"curl config long", `curl --config /tmp/verify-fuzz-curlcfg http://127.0.0.1:9/`, "verify-networked"},
	{"wget input-file", `wget -i /tmp/verify-fuzz-wgeturls http://127.0.0.1:9/f`, "verify-networked"},
	{"git remote archive", `git archive --remote=ssh://127.0.0.1:9/x HEAD`, "verify-networked"},
	{"busybox wget", `busybox wget http://127.0.0.1:9/f`, "verify-networked"},
	{"pip download URL", `pip3 download http://127.0.0.1:9/evil.tgz`, "verify-networked"},
	{"telnet", `telnet 127.0.0.1 9`, "verify-networked"},
	{"curl telnet scheme", `curl telnet://127.0.0.1:9/`, "verify-networked"},
	{"secret var in URL", `curl http://127.0.0.1:9/$AWS_KEY`, "verify-credential-read"},
	{"secret var in header", `curl -s -H "X: $AWS_KEY" http://127.0.0.1:9/`, "verify-credential-read"},
	{"secret var braced", `curl http://127.0.0.1:9/${GITHUB_TOKEN}`, "verify-credential-read"},
	{"token var", `wget http://127.0.0.1:9/$NPM_TOKEN`, "verify-credential-read"},
	{"secret var in ssh", `ssh 127.0.0.1 "echo $MY_SECRET"`, "verify-credential-read"},
	{"git exec path", `git --exec-path=/tmp/verify-fuzz-gitexec status`, "verify-destructive"},
	{"git exec path spaced", `git --exec-path /tmp/verify-fuzz-gitexec status`, "verify-destructive"},
	{"tar -I", `tar -I touch-fz -cf /tmp/verify-fuzz.tar fz-notes.txt`, "verify-destructive"},
	{"tar -I uppercase", `TAR -I touch-fz -cf /tmp/verify-fuzz.tar fz-notes.txt`, "verify-destructive"},
	{"tar -I attached", `tar -Itouch-fz -cf /tmp/verify-fuzz.tar fz-notes.txt`, "verify-destructive"},
	{"tar -I clustered", `tar -cIf touch-fz /tmp/verify-fuzz.tar fz-notes.txt`, "verify-destructive"},
	{"wget input-file attached", `wget -i/tmp/verify-fuzz-wgeturls http://127.0.0.1:9/f`, "verify-networked"},
	{"wget input-file clustered", `wget -qi /tmp/verify-fuzz-wgeturls http://127.0.0.1:9/f`, "verify-networked"},
}

// TestCheckStaticHardeningRefuses pins the codes of the static gate arms that
// close the STA-315 corpus gaps.
func TestCheckStaticHardeningRefuses(t *testing.T) {
	t.Parallel()
	for _, tc := range staticHardeningRefusals {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertRefusalCode(t, tc.cmd, tc.code)
		})
	}
}

func assertRefusalCode(t *testing.T, cmd, code string) {
	t.Helper()
	ref := verifycore.Check(cmd)
	if ref == nil {
		t.Fatalf("Check(%q) = nil, want %s", cmd, code)
	}
	if ref.Code != code {
		t.Fatalf("Check(%q) = %s, want %s", cmd, ref.Code, code)
	}
}

// TestCheckStaticHardeningBenignStaysClean pins the false-positive boundary
// beside every new arm: the benign spelling executes.
func TestCheckStaticHardeningBenignStaysClean(t *testing.T) {
	t.Parallel()
	for _, cmd := range []string{
		`wget --version`,
		`wget -qO- http://127.0.0.1:9/f`,
		`curl -k http://127.0.0.1:9/fixture`,
		`curl http://127.0.0.1:9/fixture`,
		`git archive HEAD`,
		`git ls-remote https://127.0.0.1:9/x.git`,
		`busybox echo ok`,
		`busybox sh -c 'printf ok'`,
		`pip3 --version`,
		`pip3 install -r /dev/null`,
		`printf telnet-ok`,
		`curl http://127.0.0.1:9/$PORT/fixture`,
		`curl http://127.0.0.1:9/$BRANCH/fixture`,
		`curl http://127.0.0.1:9/$PROXY_PORT/fixture`,
		`echo $AWS_KEY`,
		`printf '%s' $GITHUB_TOKEN`,
		`git --exec-path`,
		`git --version`,
		`tar -czf /tmp/verify-fuzz-ok.tar fz-notes.txt`,
		`tar -cf /tmp/verify-fuzz-plain.tar fz-notes.txt`,
		`tar -tzf missing.tar.gz`,
		`wget -q --version`,
	} {
		if ref := verifycore.Check(cmd); ref != nil {
			t.Errorf("Check(%q) = %s: %q, want clean", cmd, ref.Code, ref.Message)
		}
	}
}
