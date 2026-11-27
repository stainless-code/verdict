//go:build !windows

package verifycore

// These tests build the darwin and linux mechanisms from POSIX path
// literals. Neither mechanism exists on Windows, where --sandbox refuses
// sandbox-unavailable before any profile or argv is built; that refusal is
// pinned cross-platform by TestSandboxMechanismRefusesHostsWithoutOne.

import (
	"os"
	"slices"
	"strings"
	"testing"
)

// TestSandboxMechanismBuildsTheSeatbeltProfile pins the macOS shape: the
// network denial survives, the credential table and shared host temp roots
// are denied, and the private TMPDIR plus the reviewed root are allowed.
func TestSandboxMechanismBuildsTheSeatbeltProfile(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	mech, ref := sandboxMechanism("darwin", root, homeInputs(home), false)
	if ref != nil {
		t.Fatalf("sandboxMechanism: %v", ref)
	}
	checkSeatbeltMechanism(t, mech, root, home)
}

// checkSeatbeltMechanism asserts the tool, the profile rules, and the private
// TMPDIR lifecycle of one prepared macOS mechanism.
func checkSeatbeltMechanism(t *testing.T, mech *sandbox, root, home string) {
	t.Helper()
	defer closeSandbox(t, mech)
	requireSandboxExecPrefix(t, mech)
	checkSeatbeltProfileRules(t, mech.prefix[2], root, home)
	private := checkPrivateTempEnv(t, mech)
	if !strings.Contains(mech.prefix[2], `(allow file-read* file-write* (subpath "`+private+`"))`) {
		t.Errorf("profile does not allow the private TMPDIR %q:\n%s", private, mech.prefix[2])
	}
	if info, err := os.Stat(private); err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("private TMPDIR %q = %v, %v; want an existing 0700 directory", private, info, err)
	}
	closeSandbox(t, mech)
	if _, err := os.Stat(private); !os.IsNotExist(err) {
		t.Errorf("private TMPDIR %q survived close (%v)", private, err)
	}
}

// requireSandboxExecPrefix pins the shipped executable and profile argv.
func requireSandboxExecPrefix(t *testing.T, mech *sandbox) {
	t.Helper()
	if mech.tool != "sandbox-exec" || mech.mechanism != sandboxMechanismSandboxExec {
		t.Fatalf("mechanism = %+v, want sandbox-exec", mech)
	}
	if len(mech.prefix) != 3 || mech.prefix[0] != sandboxExecPath || mech.prefix[1] != "-p" {
		t.Fatalf("prefix = %v, want %s -p <profile>", mech.prefix, sandboxExecPath)
	}
}

// wantSeatbeltHeader is the literal start of every generated profile: the
// network denial, the Mach default-deny with its one allow, and the blanket
// write denial the later allows carve out of.
const wantSeatbeltHeader = `(version 1)(allow default)(deny network*)` +
	`(deny mach-lookup)(allow mach-lookup (global-name "com.apple.system.opendirectoryd.libinfo"))` +
	`(deny lsopen)(deny appleevent-send)(deny job-creation)(deny distributed-notification-post)` +
	`(deny signal)(allow signal (target same-sandbox))` +
	`(deny file-write* (subpath "/"))`

// checkSeatbeltProfileRules pins the literal rules the profile must carry.
func checkSeatbeltProfileRules(t *testing.T, profile, root, home string) {
	t.Helper()
	if !strings.HasPrefix(profile, wantSeatbeltHeader) {
		t.Errorf("profile does not open with the network, keychain, and write denials:\n%s", profile)
	}
	for _, want := range []string{
		`(allow file-write* (literal "/dev/null") (literal "/dev/tty") (regex #"^/dev/fd/"))`,
		`(deny file-read* file-write* (subpath "` + home + `/.ssh"))`,
		`(deny file-read* file-write* (subpath "` + home + `/.vault-token"))`,
		`(deny file-read* file-write* (subpath "` + home + `/.cargo/credentials.toml"))`,
		`(deny file-read* file-write* (subpath "` + home + `/.terraform.d"))`,
		`(deny file-read* file-write* (subpath "` + home + `/Library/Keychains"))`,
		`(deny file-read* file-write* (subpath "` + home + `/.aws"))`,
		`(deny file-read* file-write* (subpath "` + home + `/.netrc"))`,
		`(deny file-read* file-write* (subpath "/private/tmp"))`,
		`(deny file-read* file-write* (subpath "/tmp"))`,
		`(allow file-read* file-write* (subpath "` + root + `"))`,
	} {
		if !strings.Contains(profile, want) {
			t.Errorf("profile misses %q:\n%s", want, profile)
		}
	}
}

// checkPrivateTempEnv proves TMPDIR/TMP/TEMP/GOTMPDIR and the toolchain
// caches point into one fresh per-run directory and returns it.
func checkPrivateTempEnv(t *testing.T, mech *sandbox) string {
	t.Helper()
	if len(mech.env) == 0 {
		t.Fatalf("env = %v, want the private TMPDIR overrides", mech.env)
	}
	private := strings.TrimPrefix(mech.env[0], "TMPDIR=")
	wantEnv := []string{
		"TMPDIR=" + private, "TMP=" + private, "TEMP=" + private, "GOTMPDIR=" + private,
		"GOCACHE=" + private + "/go-build", "XDG_CACHE_HOME=" + private + "/cache",
	}
	if !slices.Equal(mech.env, wantEnv) {
		t.Fatalf("env = %v, want %v", mech.env, wantEnv)
	}
	if private == "" || private == "/tmp" || private == os.TempDir() {
		t.Fatalf("private TMPDIR = %q, want a per-run directory", private)
	}
	return private
}

// TestSandboxExecProfileOrdersCredentialDenialsLast proves a reviewed root
// under HOME cannot re-expose a credential path: the deny rules come after
// every allow, so the last matching rule wins.
func TestSandboxExecProfileOrdersCredentialDenialsLast(t *testing.T) {
	profile, ref := sandboxExecProfile(&sandboxProfileInputs{
		credentials: []string{"/nonexistent-home/.ssh"}, gitDirs: []string{"/nonexistent-home/repo/.git"},
		root: "/nonexistent-home/repo", private: "/nonexistent-tmp/private", tmpBase: "/nonexistent-tmp",
	})
	if ref != nil {
		t.Fatalf("sandboxExecProfile: %v", ref)
	}
	want := wantSeatbeltHeader +
		`(deny file-read* file-write* (subpath "/tmp"))` +
		`(deny file-read* file-write* (subpath "/private/tmp"))` +
		`(deny file-read* file-write* (subpath "/var/tmp"))` +
		`(deny file-read* file-write* (subpath "/private/var/tmp"))` +
		`(deny file-read* file-write* (subpath "/nonexistent-tmp"))` +
		`(allow file-read* file-write* (subpath "/nonexistent-tmp/private"))` +
		`(allow file-read* file-write* (subpath "/nonexistent-home/repo"))` +
		`(allow file-read-metadata (literal "/nonexistent-tmp") (literal "/") (literal "/nonexistent-home"))` +
		`(allow file-write* (literal "/dev/null") (literal "/dev/tty") (regex #"^/dev/fd/"))` +
		`(deny file-write* (regex "^/nonexistent-home/repo/(.*/)?\\.[Gg][Ii][Tt](/|$)"))` +
		`(deny file-write* (subpath "/nonexistent-home/repo/.git"))` +
		`(deny file-read* file-write* (subpath "/nonexistent-home/.ssh"))`
	if profile != want {
		t.Errorf("profile =\n%s\nwant\n%s", profile, want)
	}
}

// TestSandboxCredentialLocationsCoverEveryHomeAndRelocation pins the shared
// table both platforms hide: the full home-relative list under the child
// HOME, the host HOME, and the account home, then every relocation variable
// from the child and the host environments (a relative or empty value names
// nothing). Paths are literals, never rebuilt from the table.
func TestSandboxCredentialLocationsCoverEveryHomeAndRelocation(t *testing.T) {
	in := sandboxInputs{
		childEnv: []string{
			"HOME=/h/child", "XDG_CONFIG_HOME=/x/cfg", "KUBECONFIG=/x/k1:relative/k:/x/k2",
			"AWS_SHARED_CREDENTIALS_FILE=/x/aws/credentials", "DOCKER_CONFIG=", "XAUTHORITY=/x/xauth",
		},
		hostEnv: []string{
			"HOME=/h/host", "GH_CONFIG_DIR=/x/gh", "AWS_CONFIG_FILE=/x/aws/config",
			"DOCKER_CONFIG=/x/docker", "CLOUDSDK_CONFIG=/x/gcloud", "XDG_CONFIG_HOME=/x/cfg",
			"GLAB_CONFIG_DIR=/x/glab",
		},
		accountHome: "/h/account",
	}
	got, ref := in.credentialLocations("/r/repo", false)
	if ref != nil {
		t.Fatalf("credentialLocations: %v", ref)
	}
	table := []string{
		".aws", ".ssh", ".config/gh", ".docker", ".kube", ".config/gcloud", ".azure", ".gnupg",
		".netrc", ".git-credentials", ".npmrc", ".pypirc", ".terraform.d",
		".cargo/credentials.toml", ".cargo/credentials", ".vault-token", "Library/Keychains",
		".config/glab-cli", ".codex/auth.json", ".local/share/opencode/auth.json", ".pi/agent/auth.json",
		".claude/.credentials.json", ".config/github-copilot", ".gemini/oauth_creds.json", ".config/git/credentials",
		".local/share/opencode/mcp-auth.json", ".Xauthority", ".1password", ".bitwarden-ssh-agent.sock",
		"Library/Group Containers/2BUA8C4S2C.com.1password", "Library/Containers/com.maxgoedjen.Secretive.SecretAgent",
		".local/share/opencode",
	}
	var want []string
	for _, home := range []string{"/h/child", "/h/host", "/h/account"} {
		for _, rel := range table {
			want = append(want, home+"/"+rel)
		}
	}
	want = append(want,
		"/x/cfg/gh", "/x/aws/credentials", "/x/k1", "/x/k2", "/x/cfg/glab-cli", "/x/cfg/github-copilot",
		"/x/cfg/git/credentials", "/x/xauth",
		"/x/gh", "/x/aws/config", "/x/docker", "/x/gcloud", "/x/glab",
	)
	if !slices.Equal(got, want) {
		t.Errorf("locations =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestSandboxCredentialHomesDedupe keeps one home answered three ways to one
// set of locations, so the Linux argv carries no duplicate mount.
func TestSandboxCredentialHomesDedupe(t *testing.T) {
	in := sandboxInputs{childEnv: []string{"HOME=/h/same/"}, hostEnv: []string{"HOME=/h/same"}, accountHome: "/h/same"}
	homes, ref := in.homes()
	if ref != nil || !slices.Equal(homes, []string{"/h/same"}) {
		t.Errorf("homes = %v, %v; want [/h/same]", homes, ref)
	}
}

// closeSandbox releases one prepared mechanism and fails the test when its
// private state survives.
func closeSandbox(t *testing.T, sb *sandbox) {
	t.Helper()
	if err := sb.close(); err != nil {
		t.Errorf("close: %v", err)
	}
}

// TestSandboxHomeUsesTheLastHOME follows os/exec: the last of duplicate
// entries is what the child sees, so it is the home whose credentials hide.
func TestSandboxHomeUsesTheLastHOME(t *testing.T) {
	in := sandboxInputs{childEnv: []string{"HOME=/h/first", "HOME=/h/last"}}
	homes, ref := in.homes()
	if ref != nil || !slices.Equal(homes, []string{"/h/last"}) {
		t.Errorf("homes = %v, %v; want [/h/last]", homes, ref)
	}
}
