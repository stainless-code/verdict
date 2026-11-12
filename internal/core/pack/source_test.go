package pack

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

// gitSourceCase is one accepted git source and the canonical URL/ref pack add
// must store and hand to git.
type gitSourceCase struct {
	name   string
	source string
	url    string
	ref    string
}

// refusedSourceCase is one refused remote-shaped source and the secret its
// bytes carry.
type refusedSourceCase struct {
	name   string
	source string
	secret string
}

// allowedGitSources is every git source shape the allowlist accepts.
var allowedGitSources = []gitSourceCase{
	{"https", "https://host/x.git", "https://host/x.git", ""},
	{"https port", "https://host:8443/x.git", "https://host:8443/x.git", ""},
	{"https ipv4", "https://127.0.0.1:8443/x.git", "https://127.0.0.1:8443/x.git", ""},
	{"https ipv6", "https://[2001:db8::1]:8443/x.git", "https://[2001:db8::1]:8443/x.git", ""},
	{"ssh", "ssh://git@host/x.git", "ssh://git@host/x.git", ""},
	{"ssh port", "ssh://deploy-bot@host:22/x", "ssh://deploy-bot@host:22/x", ""},
	{"ssh ipv6", "ssh://git@[2001:db8::1]:22/x", "ssh://git@[2001:db8::1]:22/x", ""},
	{"scp", "git@host:acme/x.git", "git@host:acme/x.git", ""},
	{"scp ipv6", "git@[2001:db8::1]:acme/x.git", "git@[2001:db8::1]:acme/x.git", ""},
	{"uppercase scheme", "HTTPS://host/x.git", "https://host/x.git", ""},
	{"https ref", "https://host/x.git@v1.2.3", "https://host/x.git", "v1.2.3"},
	{"scp ref", "git@host:acme/x.git@release/1", "git@host:acme/x.git", "release/1"},
	{"ssh ref", "ssh://deploy-bot@host:22/x@v1", "ssh://deploy-bot@host:22/x", "v1"},
}

// refusedGitSources is every credential or malformed-remote shape the earlier
// rounds named, plus the re-check's one-slash and percent-encoded escapes.
var refusedGitSources = []refusedSourceCase{
	{"one slash", "https:/user:S3CRET@host/acme/packs.git", "S3CRET"},
	{"one slash token", "https:/ghp_TOKEN@host/x.git", "ghp_TOKEN"},
	{"ssh percent colon", "ssh://user%3ATOK@host/acme/packs.git", "TOK"},
	{"scp percent colon", "user%3ATOK@host:acme/packs.git", "TOK"},
	{"https password", "https://oauth2:ghp_TOKEN@host/acme/packs.git", "ghp_TOKEN"},
	{"https token only", "https://ghp_SUPERSECRET@host/acme/packs.git", "ghp_SUPERSECRET"},
	{"second at", "https://us@er:S3CRET@host/x.git", "S3CRET"},
	{"at in password", "https://user:p@ss@host/x.git", "p@ss"},
	{"bare https user", "https://ghp_BAREUSER@host/x.git", "ghp_BAREUSER"},
	{"ssh password", "ssh://user:S3CRET@host/x", "S3CRET"},
	{"percent colon https", "https://user%3AS3CRET@host/x.git", "S3CRET"},
	{"percent at https", "https://us%40er:S3CRET@host/x.git", "S3CRET"},
	{"percent at ssh user", "ssh://us%40er@host/x", "us%40er"},
	{"http userinfo", "http://ghp_SUPERSECRET@host/x.git", "ghp_SUPERSECRET"},
	{"scp password", "oauth2:S3CRET@host:acme/packs.git", "S3CRET"},
	{"uppercase password", "HTTPS://user:TOK@host/x.git", "TOK"},
	{"uppercase token", "HTTPS://TOK@host/x.git", "TOK"},
	{"ipv6 userinfo", "https://user:TOK@[2001:db8::1]:8443/x.git", "TOK"},
	{"git plus https", "git+https://user:TOK@host/x.git", "TOK"},
	{"git scheme", "git://host/x.git", ""},
	{"ssh without user", "ssh://host/x.git", ""},
	{"leading dash path", "https://host/-x", ""},
	{"space in path", "https://host/x y", ""},
	{"invalid ref", "https://host/x.git@-bad", ""},
	{"no path", "https://host", ""},
	{"dir url label", "dir:https://TOK@host/x.git", "TOK"},
}

// TestParseGitSourceAllows proves every accepted form parses to its canonical
// URL and ref.
func TestParseGitSourceAllows(t *testing.T) {
	t.Parallel()
	for _, tc := range allowedGitSources {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := parseGitSource(tc.source)
			if !ok || got.url != tc.url || got.ref != tc.ref {
				t.Fatalf("parseGitSource(%q) = %+v, %v; want url %q ref %q",
					tc.source, got, ok, tc.url, tc.ref)
			}
		})
	}
}

// TestParseGitSourceRefuses proves every refused form does not parse.
func TestParseGitSourceRefuses(t *testing.T) {
	t.Parallel()
	for _, tc := range refusedGitSources {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got, ok := parseGitSource(tc.source); ok {
				t.Fatalf("parseGitSource(%q) = %+v, want refused", tc.source, got)
			}
		})
	}
}

// TestAddRefusesGitSources proves every refused shape refuses before any git
// call or write, and neither the refusal nor the display renderer echoes the
// source or its secret.
func TestAddRefusesGitSources(t *testing.T) {
	for _, tc := range refusedGitSources {
		t.Run(tc.name, func(t *testing.T) {
			assertAddRefused(t, tc.source, tc.secret)
		})
	}
}

// assertAddRefused runs one refused add and checks the whole refusal
// contract: code, masked message, zero git calls, and no write.
func assertAddRefused(t *testing.T, source, secret string) {
	t.Helper()
	t.Chdir(t.TempDir())
	cmdr := &gitCommander{t: t}
	_, ref := RunAdd(context.Background(), &AddRequest{
		Runtime: &core.Runtime{Commander: cmdr},
		Source:  source,
	})
	if ref == nil || ref.Code != CodeBadPackSource {
		t.Fatalf("RunAdd(%q) = %v, want bad-pack-source", source, ref)
	}
	assertMaskedMessage(t, ref.Message, secret)
	if len(cmdr.calls) != 0 {
		t.Fatalf("git calls = %d, want none before the refusal", len(cmdr.calls))
	}
	if _, err := os.Stat(".verdict"); !os.IsNotExist(err) {
		t.Fatalf("refused add wrote .verdict (stat err %v)", err)
	}
	if got := displaySource(source); got != invalidSource {
		t.Fatalf("displaySource(%q) = %q, want %q", source, got, invalidSource)
	}
}

// assertMaskedMessage proves a refusal renders the placeholder and never the
// secret.
func assertMaskedMessage(t *testing.T, message, secret string) {
	t.Helper()
	if !strings.Contains(message, invalidSource) {
		t.Fatalf("message = %q, want the %s placeholder", message, invalidSource)
	}
	if secret != "" && strings.Contains(message, secret) {
		t.Fatalf("message = %q, carries %q", message, secret)
	}
}

// TestAddRefusesCredentialShapedPath proves a source that only looks like a
// directory still refuses as a source, with its bytes masked.
func TestAddRefusesCredentialShapedPath(t *testing.T) {
	t.Chdir(t.TempDir())
	cmdr := &gitCommander{t: t}
	const source = "user:pass@host"
	_, ref := RunAdd(context.Background(), &AddRequest{
		Runtime: &core.Runtime{Commander: cmdr},
		Source:  source,
	})
	if ref == nil || ref.Code != CodeBadPackSource {
		t.Fatalf("RunAdd(%q) = %v, want bad-pack-source", source, ref)
	}
	if !strings.Contains(ref.Message, invalidSource) {
		t.Fatalf("message = %q, want the %s placeholder", ref.Message, invalidSource)
	}
	if strings.Contains(ref.Message, "pass") {
		t.Fatalf("message = %q, carries the userinfo", ref.Message)
	}
	if len(cmdr.calls) != 0 {
		t.Fatalf("git calls = %d, want none", len(cmdr.calls))
	}
}

// TestAddClonesAllowedGitSources proves every accepted form reaches git with
// the exact canonical URL and its optional --branch ref, and is recorded as
// that URL pinned to the resolved sha.
func TestAddClonesAllowedGitSources(t *testing.T) {
	for _, tc := range allowedGitSources {
		t.Run(tc.name, func(t *testing.T) {
			assertAllowedClone(t, tc)
		})
	}
}

// assertAllowedClone runs one accepted add and checks its clone argv and lock
// row.
func assertAllowedClone(t *testing.T, tc gitSourceCase) {
	t.Helper()
	t.Chdir(t.TempDir())
	cmdr := &gitCommander{t: t, files: map[string]string{
		"pack.json":      lockManifest,
		"lenses/perf.md": "perf body\n",
	}}
	resp, ref := RunAdd(context.Background(), &AddRequest{
		Runtime: &core.Runtime{Commander: cmdr},
		Source:  tc.source,
	})
	if ref != nil {
		t.Fatalf("RunAdd(%q): %s: %s", tc.source, ref.Code, ref.Message)
	}
	if len(cmdr.calls) != 3 {
		t.Fatalf("git calls = %d, want clone, rev-parse, checkout", len(cmdr.calls))
	}
	assertCloneURL(t, cmdr.calls[0].argv, tc.url, tc.ref)
	label := sourceGitPrefix + tc.url + refSep + addGitSha
	want := "added acme/lenses@1.0.0 source=" + label + " sha256:" + lockHash
	if got := resp.Human; !slices.Equal(got, []string{want}) {
		t.Fatalf("human = %q, want %q", got, want)
	}
	raw, err := os.ReadFile(LockFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"source": "`+label+`"`) {
		t.Fatalf("lock = %s, want source %q", raw, label)
	}
}

// assertCloneURL proves the clone argv carries url, and --branch only when a
// ref was given.
func assertCloneURL(t *testing.T, argv []string, url, ref string) {
	t.Helper()
	if got := argv[len(argv)-2]; got != url {
		t.Fatalf("clone URL = %q, want %q (argv %q)", got, url, argv)
	}
	at := slices.Index(argv, "--branch")
	if got, want := at >= 0, ref != ""; got != want {
		t.Fatalf("clone argv = %q, want --branch present=%v", argv, want)
	}
	if ref != "" && argv[at+1] != ref {
		t.Fatalf("clone ref = %q, want %q", argv[at+1], ref)
	}
}

// TestDisplaySource proves every rendered source is either the parsed source
// or the fixed placeholder, never the bytes a caller supplied.
func TestDisplaySource(t *testing.T) {
	t.Parallel()
	sha := strings.Repeat("c", 40)
	for _, tc := range []struct {
		name   string
		source string
		want   string
	}{
		{"builtin", "builtin", "builtin"},
		{"repo row", ".verdict/packs/lenses", ".verdict/packs/lenses"},
		{"dir label", "dir:src-packs/lenses", "dir:src-packs/lenses"},
		{"git label", "git:https://host/x.git@" + sha, "git:https://host/x.git@" + sha},
		{"uppercase git label", "git:HTTPS://host/x.git@" + sha, invalidSource},
		{"one slash git label", "git:https:/user:TOK@host/x.git@" + sha, invalidSource},
		{"masked dir url", "dir:https://TOK@host/x", invalidSource},
		{"masked dir userinfo", "dir:user:pass@host", invalidSource},
		{"plain url", "https://host/x.git", "https://host/x.git"},
		{"plain uppercase url", "HTTPS://host/x.git", "https://host/x.git"},
		{"plain dir", "src-packs/lenses", "dir:src-packs/lenses"},
		{"plain credential url", "https://user:TOK@host/x", invalidSource},
		{"plain credential shape", "user:pass@host", invalidSource},
		{"windows drive", "dir:C:/packs", "dir:C:/packs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := displaySource(tc.source); got != tc.want {
				t.Fatalf("displaySource(%q) = %q, want %q", tc.source, got, tc.want)
			}
		})
	}
}

// TestGitStderrWithheld proves git's own text is kept only when it carries no
// userinfo separator: an @ anywhere in it drops the whole message.
func TestGitStderrWithheld(t *testing.T) {
	t.Parallel()
	const hint = "the remote error is withheld because it carries an @; check the source and your access"
	for _, tc := range []struct {
		name string
		text string
		want string
	}{
		{"scp form", "fatal: repository 'er:TOKX@other.invalid:acme/packs.git' not found", hint},
		{"https form", "fatal: unable to access 'https://er:S3CRET@example.invalid/x.git/'", hint},
		{"clean", "fatal: not a git repository", "fatal: not a git repository"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := scrubGitStderr(tc.text); got != tc.want {
				t.Fatalf("scrubGitStderr(%q) = %q, want %q", tc.text, got, tc.want)
			}
		})
	}
}

// TestAddWithholdsCloneStderrCarryingAnAt proves the re-check's scp-form stderr
// escape cannot print its token through a refusal.
func TestAddWithholdsCloneStderrCarryingAnAt(t *testing.T) {
	t.Chdir(t.TempDir())
	cmdr := &gitCommander{
		t:           t,
		cloneStderr: "fatal: repository 'er:TOKX@other.invalid:acme/packs.git' not found\n",
		cloneExit:   128,
	}
	_, ref := RunAdd(context.Background(), &AddRequest{
		Runtime: &core.Runtime{Commander: cmdr},
		Source:  "https://example.invalid/acme/packs.git",
	})
	if ref == nil || ref.Code != CodeBadPack {
		t.Fatalf("RunAdd = %v, want bad-pack", ref)
	}
	if strings.Contains(ref.Message, "TOKX") {
		t.Fatalf("message = %q, carries the clone credential", ref.Message)
	}
	if !strings.Contains(ref.Message, "withheld") {
		t.Fatalf("message = %q, want the withheld hint", ref.Message)
	}
}

// TestListMasksUnparseableDirSource proves a lock row the dir grammar accepts
// but that looks like a URL never reaches a human or --json list row raw.
func TestListMasksUnparseableDirSource(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	const secret = "TOK_LOCK_ROW"
	source := "dir:https://" + secret + "@host/x"
	writeFixture(t, root, map[string]string{LockFile: lockSourceBody(source)})
	req := &Request{List: true}
	req.Runtime = &core.Runtime{}
	req.RepoRoot = root
	resp, ref := RunList(t.Context(), req)
	if ref != nil {
		t.Fatalf("RunList: %s: %s", ref.Code, ref.Message)
	}
	if len(resp.Data.Packs) != 1 {
		t.Fatalf("rows = %d, want the missing lock row", len(resp.Data.Packs))
	}
	if got := resp.Data.Packs[0].Source; got != invalidSource {
		t.Fatalf("json source = %q, want %q", got, invalidSource)
	}
	if got := strings.Join(resp.Human, "\n"); !strings.Contains(got, "source="+invalidSource) ||
		strings.Contains(got, secret) {
		t.Fatalf("human = %q, want a masked source", got)
	}
}

// TestListRefusesOneSlashLockSource proves the A' lock row refuses pack list
// and the refusal masks the source.
func TestListRefusesOneSlashLockSource(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	const secret = "S3CRET_LOCK"
	source := "git:https:/user:" + secret + "@host/x.git@" + addGitSha
	writeFixture(t, root, map[string]string{LockFile: lockSourceBody(source)})
	req := &Request{List: true}
	req.Runtime = &core.Runtime{}
	req.RepoRoot = root
	_, ref := RunList(t.Context(), req)
	if ref == nil || ref.Code != CodeBadLockSource {
		t.Fatalf("RunList = %v, want bad-lock-source", ref)
	}
	if strings.Contains(ref.Message, secret) {
		t.Fatalf("message = %q, carries %q", ref.Message, secret)
	}
	if !strings.Contains(ref.Message, invalidSource) {
		t.Fatalf("message = %q, want the %s placeholder", ref.Message, invalidSource)
	}
}

// TestLockRejectsRefusedGitSources proves every refused add source is also a
// refused git: lock row once pinned to a sha.
func TestLockRejectsRefusedGitSources(t *testing.T) {
	t.Parallel()
	for _, tc := range refusedGitSources {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertLockRefused(t, sourceGitPrefix+tc.source+refSep+addGitSha, tc.secret)
		})
	}
}

// assertLockRefused proves one git: lock row refuses and masks its source.
func assertLockRefused(t *testing.T, source, secret string) {
	t.Helper()
	_, ref := parseLock([]byte(lockSourceBody(source)))
	if ref == nil || ref.Code != CodeBadLockSource {
		t.Fatalf("parseLock(%q) = %v, want bad-lock-source", source, ref)
	}
	assertMaskedMessage(t, ref.Message, secret)
	if got := displaySource(source); got != invalidSource {
		t.Fatalf("displaySource(%q) = %q, want %q", source, got, invalidSource)
	}
}

// TestLockAcceptsAllowedGitSources proves each allowlisted URL is a valid git:
// row pinned to a sha, and displays unchanged.
func TestLockAcceptsAllowedGitSources(t *testing.T) {
	t.Parallel()
	for _, tc := range allowedGitSources {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertLockAccepted(t, sourceGitPrefix+tc.url+refSep+addGitSha)
		})
	}
}

// assertLockAccepted proves one canonical git: row parses and displays
// unchanged.
func assertLockAccepted(t *testing.T, source string) {
	t.Helper()
	if _, ref := parseLock([]byte(lockSourceBody(source))); ref != nil {
		t.Fatalf("parseLock(%q) = %s: %s, want it accepted", source, ref.Code, ref.Message)
	}
	if got := displaySource(source); got != source {
		t.Fatalf("displaySource(%q) = %q, want it unchanged", source, got)
	}
}

// TestValidLockSourceRejectsNonCanonicalScheme proves the lock stores the
// lower-case scheme pack add writes; a hand-written upper-case row refuses.
func TestValidLockSourceRejectsNonCanonicalScheme(t *testing.T) {
	t.Parallel()
	raw := sourceGitPrefix + "HTTPS://host/x.git" + refSep + addGitSha
	if validLockSource(raw) {
		t.Fatalf("validLockSource(%q) = true, want the stored scheme lower-case", raw)
	}
}
