//go:build darwin

package run

import (
	"encoding/pem"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/hostsandbox"
)

// serviceDomain is the preference domain the hostile agent tries to write.
const serviceDomain = "com.stainless.verdict.sandbox-test"

// serviceScript is a prompt-injected agent with a shell reaching past the
// filesystem rules through macOS services: a `defaults write` (cfprefsd
// writes the plist for it) and the pasteboard.
const serviceScript = "#!/bin/sh\n" +
	"{ /usr/bin/defaults write " + serviceDomain + " k v && echo defaults=written || echo defaults=denied\n" +
	"  printf verdict-sandbox | /usr/bin/pbcopy && echo pasteboard=written || echo pasteboard=denied\n" +
	"} > \"$HOME/.pi/agent/sessions/services.log\" 2>&1\n" +
	"printf '%s' '" + reviewDoc + "' > .verdict/state/42/review.json\n"

// TestReviewSandboxClosesMacOSServices proves a review agent with a live
// shell can neither write a preference through cfprefsd nor set the
// pasteboard, and the domain stays absent.
func TestReviewSandboxClosesMacOSServices(t *testing.T) {
	t.Parallel()
	requireReviewSandbox(t)
	t.Cleanup(func() {
		// The delete fails when the domain is absent, which is the passing case.
		if out, err := exec.Command("/usr/bin/defaults", "delete", serviceDomain).CombinedOutput(); err != nil { //nolint:noctx // cleanup outlives t.Context
			t.Logf("defaults delete %s: %v: %s", serviceDomain, err, out)
		}
	})
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".pi", "agent", "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, ref := sandboxedRun(t, confinedRoot(t), home, serviceScript, "1"); ref != nil {
		t.Fatalf("review refused: %v", ref)
	}
	log := string(readFile(t, filepath.Join(home, ".pi", "agent", "sessions", "services.log")))
	if !strings.Contains(log, "defaults=denied") || !strings.Contains(log, "pasteboard=denied") {
		t.Errorf("services log:\n%s\nwant both denied", log)
	}
	if out, err := exec.Command("/usr/bin/defaults", "read", serviceDomain).CombinedOutput(); err == nil { //nolint:noctx // a read
		t.Errorf("the preference domain exists after the review: %s", out)
	}
}

// lsregisterPath is LaunchServices' registration tool.
const lsregisterPath = "/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/" +
	"Support/lsregister"

// windowServerProbe is a JXA script that lists the session's windows, which
// takes a window-server connection — the one synthetic input would use.
const windowServerProbe = "ObjC.import('CoreGraphics'); ObjC.import('stdlib');" +
	"var l = $.CGWindowListCopyWindowInfo(1, 0); var n = l ? $.CFArrayGetCount(l) : -1;" +
	"console.log('windows: ' + n); $.exit(n > 0 ? 0 : 3)"

// machScript is a prompt-injected agent reaching for Mach services past the
// allowlist — registering a planted app with LaunchServices (it would run
// outside the sandbox later), Shortcuts, the window server — beside what
// every agent needs: a DNS lookup of localhost, a TLS handshake, and a trust
// evaluation through trustd (`security verify-cert` reports the
// self-signed stub untrusted only when trustd answers).
const machScript = "#!/bin/sh\n" +
	"app=\"$HOME/.pi/agent/sessions/Planted.app\"\n" +
	"/bin/mkdir -p \"$app/Contents/MacOS\"\n" +
	"printf '<?xml version=\"1.0\"?><plist version=\"1.0\"><dict><key>CFBundleIdentifier</key>" +
	"<string>com.stainless.verdict.planted</string><key>CFBundleExecutable</key><string>x</string></dict></plist>' " +
	"> \"$app/Contents/Info.plist\"\n" +
	"{ " + lsregisterPath + " -f \"$app\" && echo lsregister=registered || echo lsregister=denied\n" +
	"  /usr/bin/shortcuts list && echo shortcuts=listed || echo shortcuts=denied\n" +
	"  /usr/bin/osascript -l JavaScript -e \"" + windowServerProbe + "\" && echo windowserver=reached " +
	"|| echo windowserver=denied\n" +
	"  /usr/bin/curl -sS -o /dev/null \"http://localhost:$STUB_PORT/\" && echo dns=resolved || echo dns=failed\n" +
	"  /usr/bin/curl -sS -o /dev/null --cacert \"$STUB_CERT\" \"$STUB_TLS\" && echo tls=done || echo tls=failed\n" +
	"  /usr/bin/security verify-cert -c \"$STUB_CERT\"\n" +
	"} > \"$HOME/.pi/agent/sessions/mach.log\" 2>&1\n" +
	"printf '%s' '" + reviewDoc + "' > .verdict/state/42/review.json\n"

// TestReviewSandboxDeniesMachServicesPastTheAllowlist proves the macOS
// profile closes every Mach service but its allowlist: a review agent with a
// live shell cannot register a planted app with LaunchServices, reach
// Shortcuts, or connect to the window server, yet still resolves localhost,
// completes a TLS handshake, and has trustd evaluate a certificate.
func TestReviewSandboxDeniesMachServicesPastTheAllowlist(t *testing.T) {
	t.Parallel()
	requireReviewSandbox(t)
	home := t.TempDir()
	sessions := filepath.Join(home, ".pi", "agent", "sessions")
	if err := os.MkdirAll(sessions, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// A registration that got through must not outlive the test.
		_ = exec.Command(lsregisterPath, "-u", filepath.Join(sessions, "Planted.app")).Run() //nolint:noctx,errcheck // cleanup; absent is the passing case
	})
	plain, tlsSrv, cert := machStubs(t, sessions)
	_, ref := sandboxedRun(t, confinedRoot(t), home, machScript, "1",
		"STUB_PORT="+stubPort(t, plain),
		"STUB_TLS="+tlsSrv.URL, "STUB_CERT="+cert)
	if ref != nil {
		t.Fatalf("review refused: %v", ref)
	}
	log := string(readFile(t, filepath.Join(sessions, "mach.log")))
	for _, want := range []string{
		"lsregister=denied", "shortcuts=denied", "windowserver=denied", "dns=resolved", "tls=done",
		"CSSMERR_TP_NOT_TRUSTED",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("mach log lacks %q:\n%s", want, log)
		}
	}
}

// stubPort is the port a stub server listens on.
func stubPort(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	_, port, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return port
}

// machStubs starts a plain and a TLS stub server and writes the TLS one's
// certificate where the agent may read it.
func machStubs(t *testing.T, dir string) (plain, tlsSrv *httptest.Server, cert string) {
	t.Helper()
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	plain, tlsSrv = httptest.NewServer(ok), httptest.NewTLSServer(ok)
	t.Cleanup(plain.Close)
	t.Cleanup(tlsSrv.Close)
	cert = filepath.Join(dir, "stub.pem")
	block := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: tlsSrv.Certificate().Raw})
	if err := os.WriteFile(cert, block, 0o600); err != nil {
		t.Fatal(err)
	}
	return plain, tlsSrv, cert
}

// TestReviewSandboxResolvesAWrapperUnderTmp proves a launch whose snapshot
// sits in /tmp — where it lands when TMPDIR is unset — can still stat the
// temp root on its way to the wrapper (Node's realpath walks every
// ancestor), while listing the root stays denied.
func TestReviewSandboxResolvesAWrapperUnderTmp(t *testing.T) {
	t.Parallel()
	requireReviewSandbox(t)
	wrapper, err := os.MkdirTemp("/tmp", "verdict-snapshot-") //nolint:usetesting // the case is a wrapper in /tmp itself
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(wrapper) })
	box := &reviewBox{mechanism: hostsandbox.SandboxExecTool, runner: hostsandbox.SandboxExecPath, home: t.TempDir()}
	argv, err := box.prefix([]string{wrapper}, "", true)
	if err != nil {
		t.Fatal(err)
	}
	stat := exec.CommandContext(t.Context(), argv[0], append(argv[1:], "/usr/bin/stat", "-f", "%N", "/private/tmp")...)
	if out, err := stat.CombinedOutput(); err != nil {
		t.Errorf("stat of the temp root on the way to the wrapper failed: %v: %s", err, out)
	}
	list := exec.CommandContext(t.Context(), argv[0], append(argv[1:], "/bin/ls", "/private/tmp")...)
	if out, err := list.CombinedOutput(); err == nil {
		t.Errorf("the temp root was listed:\n%s", out)
	}
}
