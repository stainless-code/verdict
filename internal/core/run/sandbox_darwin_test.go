//go:build darwin

package run

import (
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/stainless-code/verdict/internal/hostsandbox"
)

// serviceScript is a prompt-injected agent with a shell reaching past the
// filesystem rules through macOS services: a `defaults write` into
// SERVICE_DOMAIN (cfprefsd writes the plist for it) and the pasteboard.
const serviceScript = "#!/bin/sh\n" +
	"{ /usr/bin/defaults write \"$SERVICE_DOMAIN\" k v && echo defaults=written || echo defaults=denied\n" +
	"  printf verdict-sandbox | /usr/bin/pbcopy && echo pasteboard=written || echo pasteboard=denied\n" +
	"} 2>&1\n" +
	"printf '%s' '" + reviewDoc + "' > .verdict/state/42/review.json\n"

// TestReviewSandboxClosesMacOSServices proves a review agent with a live
// shell can neither write a preference through cfprefsd nor set the
// pasteboard, and the domain stays absent.
func TestReviewSandboxClosesMacOSServices(t *testing.T) {
	t.Parallel()
	requireReviewSandbox(t)
	// A domain of this run's own, so neither the check nor the cleanup
	// touches preferences that existed before.
	serviceDomain := fmt.Sprintf("com.stainless.verdict.sandbox-test.%d.%d", os.Getpid(), time.Now().UnixNano())
	t.Cleanup(func() {
		// The delete fails when the domain is absent, which is the passing case.
		del := exec.Command("/usr/bin/defaults", "delete", serviceDomain) //nolint:noctx // cleanup outlives t.Context
		del.WaitDelay = commandWait
		if out, err := del.CombinedOutput(); err != nil {
			t.Logf("defaults delete %s: %v: %s", serviceDomain, err, out)
		}
	})
	_, ref, log := sandboxedRun(t, confinedRoot(t), t.TempDir(), serviceScript, "SERVICE_DOMAIN="+serviceDomain)
	if ref != nil {
		t.Fatalf("review refused: %v", ref)
	}
	if !strings.Contains(log, "defaults=denied") || !strings.Contains(log, "pasteboard=denied") {
		t.Errorf("services log:\n%s\nwant both denied", log)
	}
	read := exec.CommandContext(t.Context(), "/usr/bin/defaults", "read", serviceDomain)
	read.WaitDelay = commandWait
	if out, err := read.CombinedOutput(); err == nil {
		t.Errorf("the preference domain exists after the review: %s", out)
	}
}

// commandWait bounds how long a captured command's pipes may outlive it.
const commandWait = 5 * time.Second

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
	"app=\"$TMPDIR/Planted.app\"\n" +
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
	"} 2>&1\n" +
	"printf '%s' '" + reviewDoc + "' > .verdict/state/42/review.json\n"

// TestReviewSandboxDeniesMachServicesPastTheAllowlist proves the macOS
// profile closes every Mach service but its allowlist: a review agent with a
// live shell cannot register a planted app with LaunchServices, reach
// Shortcuts, or connect to the window server, yet still resolves localhost,
// completes a TLS handshake, and has trustd evaluate a certificate.
func TestReviewSandboxDeniesMachServicesPastTheAllowlist(t *testing.T) {
	t.Parallel()
	requireReviewSandbox(t)
	// The planted app lives in the launch's private TMPDIR, removed with it:
	// a registration that got through names a bundle that is gone.
	plain, tlsSrv, cert := machStubs(t, t.TempDir())
	_, ref, log := sandboxedRun(t, confinedRoot(t), t.TempDir(), machScript,
		"STUB_PORT="+stubPort(t, plain),
		"STUB_TLS="+tlsSrv.URL, "STUB_CERT="+cert)
	if ref != nil {
		t.Fatalf("review refused: %v", ref)
	}
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
	stat.WaitDelay = commandWait
	if out, err := stat.CombinedOutput(); err != nil {
		t.Errorf("stat of the temp root on the way to the wrapper failed: %v: %s", err, out)
	}
	list := exec.CommandContext(t.Context(), argv[0], append(argv[1:], "/bin/ls", "/private/tmp")...)
	list.WaitDelay = commandWait
	if out, err := list.CombinedOutput(); err == nil {
		t.Errorf("the temp root was listed:\n%s", out)
	}
}

// ttyHelperEnv marks the re-executed test process that plays the operator's
// shell: the leader of a session whose controlling terminal is a pty.
const ttyHelperEnv = "VERDICT_TTY_HELPER"

// ttyMarker is the command the hostile agent pushes into the terminal.
const ttyMarker = "echo VERDICT_TTY_MARK"

// ttyScript is a prompt-injected agent with a shell: it ignores SIGTTOU,
// opens /dev/tty read-only, and pushes ttyMarker into the terminal's input
// queue one character at a time (TIOCSTI), for the operator's shell to run
// once verdict exits.
const ttyScript = "#!/bin/sh\n" +
	"/usr/bin/perl -e '$SIG{TTOU}=\"IGNORE\"; open(T, \"<\", \"/dev/tty\") or do { print \"tty=closed\\n\"; exit 0 };" +
	" for my $c (split //, \"" + ttyMarker + "\\n\") { ioctl(T, 0x80017472, $c) or do { print \"sti=denied\\n\"; exit 0 } }" +
	" print \"sti=pushed\\n\"'\n" +
	"printf '%s' '" + reviewDoc + "' > .verdict/state/42/review.json\n"

// TestReviewSandboxBlocksTerminalInjection proves a sandboxed review agent
// cannot push input into the operator's terminal: run from a shell whose
// controlling terminal is a pty, the agent's TIOCSTI reaches nothing, and
// the terminal's input queue holds no marker for the shell to run after.
func TestReviewSandboxBlocksTerminalInjection(t *testing.T) {
	if os.Getenv(ttyHelperEnv) == "1" {
		runTTYHelper(t)
		return
	}
	t.Parallel()
	requireReviewSandbox(t)
	master, slave := openPTY(t)
	defer func() { _ = master.Close() }()
	// Drain the terminal's output: a session leader's exit waits for it to
	// empty, and an injected line echoes there.
	go drainPTY(master)
	helper := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestReviewSandboxBlocksTerminalInjection$",
		"-test.count=1", "-test.v")
	helper.Env = append(os.Environ(), ttyHelperEnv+"=1")
	helper.WaitDelay = commandWait
	var out strings.Builder
	helper.Stdin, helper.Stdout, helper.Stderr = slave, &out, &out
	helper.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	err := helper.Run()
	_ = slave.Close()
	if err != nil {
		t.Fatalf("helper: %v\n%s", err, out.String())
	}
	log := out.String()
	t.Logf("helper:\n%s", log)
	// The attack must have run: the agent reported that it found no
	// terminal or could not push into it.
	ran := strings.Contains(log, "tty=closed") || strings.Contains(log, "sti=denied")
	if !ran || strings.Contains(log, "REFUSED ") {
		t.Fatalf("the agent never reached its terminal check:\n%s", log)
	}
	if !strings.Contains(log, "QUEUE[") || strings.Contains(log, "VERDICT_TTY_MARK\n]") ||
		strings.Contains(log, "sti=pushed") {
		t.Errorf("the agent pushed input into the operator's terminal:\n%s", log)
	}
}

// runTTYHelper is the operator's shell: it runs one sandboxed review as pi,
// then reports what waits in its terminal's input queue.
func runTTYHelper(t *testing.T) {
	t.Helper()
	_, ref, log := sandboxedRun(t, confinedRoot(t), t.TempDir(), ttyScript)
	if ref != nil {
		fmt.Printf("REFUSED %v\n", ref)
		t.Fatalf("the review never started: %v", ref)
	}
	if err := syscall.SetNonblock(0, true); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 256)
	n, _ := syscall.Read(0, buf) //nolint:errcheck // EAGAIN is an empty queue
	t.Logf("QUEUE[%s]", buf[:max(n, 0)])
	fmt.Printf("QUEUE[%s]\n%s\n", buf[:max(n, 0)], log)
}

// drainPTY discards a terminal's output until it closes.
func drainPTY(master io.Reader) {
	if _, err := io.Copy(io.Discard, master); err != nil {
		return
	}
}

// openPTY opens a pseudo-terminal pair: the master, and the slave opened
// without becoming this process's controlling terminal.
func openPTY(t *testing.T) (master, slave *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("no pty: %v", err)
	}
	for _, req := range []uintptr{syscall.TIOCPTYGRANT, syscall.TIOCPTYUNLK} {
		if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), req, 0); errno != 0 {
			t.Fatalf("pty ioctl: %v", errno)
		}
	}
	var name [128]byte
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), syscall.TIOCPTYGNAME,
		uintptr(unsafe.Pointer(&name[0]))); errno != 0 {
		t.Fatalf("pty name: %v", errno)
	}
	path := string(name[:bytesIndexZero(name[:])])
	slave, err = os.OpenFile(path, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	return master, slave
}

// bytesIndexZero is the length of a NUL-terminated name.
func bytesIndexZero(b []byte) int {
	for i, c := range b {
		if c == 0 {
			return i
		}
	}
	return len(b)
}
