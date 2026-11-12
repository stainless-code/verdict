package pack

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/schemacheck"
)

const (
	lockManifest = `{"schema_version":"1","name":"acme/lenses","version":"1.0.0","lenses":[{"id":"perf"}]}`
	lockHash     = "24e651328246cb30fd8e65893deb92d3136036b466daa9615f92a7110c592b0c"
	lockHashV2   = "760382c944542247a5c7c2937e1e1e209c5c23a8daa2598f8133ccd7e8a1b5e1"
	lockSigDir   = "b9e1cce2648ab8ea1bafe1eeaed0e9103bdf91b547f82806748bc6f1471cbe8c"
)

// lockFixture writes one repo pack under root, plus the given lock body when
// non-empty.
func lockFixture(t *testing.T, root, lock string) {
	t.Helper()
	writeFixture(t, root, map[string]string{
		".verdict/packs/lenses/pack.json":      lockManifest,
		".verdict/packs/lenses/lenses/perf.md": "perf body\n",
	})
	if lock != "" {
		writeFixture(t, root, map[string]string{LockFile: lock})
	}
}

// lockSnapshot resolves the fixture's trusted-base snapshot through the head
// opt-out, exactly as fetch does on a local fixture.
func lockSnapshot(t *testing.T, root string) *controls.Snapshot {
	t.Helper()
	snap, ref := controls.Resolve(t.Context(), nil, &controls.Request{
		BundleDir: t.TempDir(), AllowHead: true, Root: root,
	})
	if ref != nil {
		t.Fatalf("controls.Resolve: %s: %s", ref.Code, ref.Message)
	}
	return snap
}

// lockBody renders one well-formed lock document for a single entry.
func lockBody(hash, sig string) string {
	return `{"schema_version":"1","packs":[{"name":"acme/lenses","source":"dir:src-packs/lenses",` +
		`"version":"1.0.0","hash":"` + hash + `","sig":"` + sig + `"}]}`
}

// lockSourceBody renders one well-formed row with the given source.
func lockSourceBody(source string) string {
	quoted, err := json.Marshal(source)
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf(`{"schema_version":"1","packs":[{"name":"acme/lenses","source":%s,`+
		`"version":"1.0.0","hash":%q,"sig":%q}]}`, quoted, lockHash, lockSigDir)
}

// TestLockSourceSchemaMatchesLoader proves the published source pattern and
// the loader agree over the whole allowlist table, plus the dir: grammar.
func TestLockSourceSchemaMatchesLoader(t *testing.T) {
	t.Parallel()
	checker, err := schemacheck.Load(
		filepath.Join("..", "..", "..", "skills", "verdict", "schemas", "lock.schema.json"), "")
	if err != nil {
		t.Fatalf("load lock schema: %v", err)
	}
	type sourceCase struct {
		name   string
		source string
		valid  bool
	}
	sha := strings.Repeat("c", 40)
	cases := []sourceCase{
		{"dir parent", "dir:../packs", true},
		{"dir path", "dir:src-packs/lenses", true},
		{"dir leading dash", "dir:-src", false},
		{"dir newline", "dir:src\npacks", false},
		{"dir backslash", `dir:src\packs`, false},
		{"dir control", "dir:src\x7fpacks", false},
		{"dir url label", "dir:https://user:tok@host/x", true},
		{"uppercase git row", "git:HTTPS://host/x.git@" + sha, false},
		{"scp path scheme", "git:git@host://acme/packs.git@" + sha, false},
		{"scp path scheme alias", "git:h@ttps://host/x@" + sha, false},
		{"scp trailing colon", "git:git@host:acme/x.git:@" + sha, true},
		{"scp trailing colon slash", "git:git@host:acme/x.git:/@" + sha, true},
		{"scp double colon", "git:git@host:acme::tok/x.git@" + sha, true},
		{"scp absolute path", "git:git@host:/srv/x.git@" + sha, true},
		{"host label 63", "git:https://" + strings.Repeat("a", 63) + ".example/x@" + sha, true},
		{"host label 64", "git:https://" + strings.Repeat("a", 64) + ".example/x@" + sha, false},
		{"host label 250", "git:https://" + strings.Repeat("a", 250) + ".example/x@" + sha, false},
		{"ipv6 dotted", "git:https://[2001:db8::1.2.3.4]/x@" + sha, true},
	}
	for _, tc := range allowedGitSources {
		cases = append(cases, sourceCase{
			name: tc.name, source: sourceGitPrefix + tc.url + refSep + sha, valid: true,
		})
	}
	for _, tc := range refusedGitSources {
		cases = append(cases, sourceCase{
			name: tc.name, source: sourceGitPrefix + tc.source + refSep + sha, valid: false,
		})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkLockSource(t, checker, tc.source, tc.valid)
		})
	}
}

// checkLockSource proves the loader and the published schema agree on one
// source.
func checkLockSource(t *testing.T, checker *schemacheck.Checker, source string, valid bool) {
	t.Helper()
	body := lockSourceBody(source)
	checkLockParse(t, source, body, valid)
	checkLockPattern(t, checker, source, body, valid)
}

// checkLockParse proves the loader's verdict on one source.
func checkLockParse(t *testing.T, source, body string, valid bool) {
	t.Helper()
	_, ref := parseLock([]byte(body))
	switch {
	case valid && ref != nil:
		t.Fatalf("parseLock(%q) = %s: %s, want it accepted", source, ref.Code, ref.Message)
	case !valid && (ref == nil || ref.Code != CodeBadLockSource):
		t.Fatalf("parseLock(%q) = %v, want bad-lock-source", source, ref)
	}
}

// checkLockPattern proves the published pattern's verdict on one source.
func checkLockPattern(t *testing.T, checker *schemacheck.Checker, source, body string, valid bool) {
	t.Helper()
	failures := checker.Check(decodeJSON(t, body))
	switch {
	case valid && len(failures) != 0:
		t.Fatalf("schema rejected %q: %v", source, failures)
	case !valid && len(failures) == 0:
		t.Fatalf("schema accepted refused source %q", source)
	}
}

// TestLockSigPinsEveryEntryField proves the integrity digest covers name,
// source, version, and hash: each hand edit changes the digest.
func TestLockSigPinsEveryEntryField(t *testing.T) {
	t.Parallel()
	entry := LockEntry{
		Name: "acme/lenses", Source: "dir:src-packs/lenses", Version: "1.0.0",
		Hash: lockHash, Sig: lockSigDir,
	}
	if got := LockSig(&entry); got != lockSigDir {
		t.Fatalf("LockSig = %s, want %s", got, lockSigDir)
	}
	//nolint:govet // the mutate matrix reads as name then action
	for _, mutate := range []struct {
		name string
		edit func(*LockEntry)
	}{
		{"name", func(e *LockEntry) { e.Name = "acme/other" }},
		{"source", func(e *LockEntry) { e.Source = "dir:elsewhere" }},
		{"version", func(e *LockEntry) { e.Version = "1.0.1" }},
		{"hash", func(e *LockEntry) { e.Hash = lockHashV2 }},
	} {
		edited := entry
		mutate.edit(&edited)
		if got := LockSig(&edited); got == lockSigDir {
			t.Errorf("LockSig ignores %s: still %s", mutate.name, got)
		}
	}
}

// TestLockStrictJSONRefuses proves the lock refuses an unknown key, a
// differently-cased key, an unsorted entry list, and a malformed source.
//
//nolint:gocognit,revive // the strict-JSON matrix reads as one block
func TestLockStrictJSONRefuses(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		body  string
		want  string
		hide  string
		code  string
		valid bool
	}{
		{
			name: "unknown top key",
			body: `{"schema_version":"1","packs":[],"extra":true}`,
			want: `"extra"`,
		},
		{
			name: "mixed case top key",
			body: `{"Schema_Version":"1","packs":[]}`,
			want: `"Schema_Version"`,
		},
		{
			name: "mixed case entry key",
			body: `{"schema_version":"1","packs":[{"Name":"acme/lenses","source":"dir:x",` +
				`"version":"1.0.0","hash":"` + lockHash + `","sig":"` + lockSigDir + `"}]}`,
			want: `"Name"`,
		},
		{
			name: "unsorted",
			body: `{"schema_version":"1","packs":[` +
				`{"name":"b/two","source":"dir:x","version":"1.0.0","hash":"` + lockHash + `","sig":"` + lockSigDir + `"},` +
				`{"name":"a/one","source":"dir:y","version":"1.0.0","hash":"` + lockHash + `","sig":"` + lockSigDir + `"}]}`,
			want: "not sorted",
		},
		{
			name: "git ssh bare user source",
			body: `{"schema_version":"1","packs":[{"name":"acme/lenses","source":"git:ssh://git@host/repo.git@` +
				strings.Repeat("c", 40) + `","version":"1.0.0","hash":"` + lockHash + `","sig":"` + lockSigDir + `"}]}`,
			valid: true,
		},
		{
			name: "git scp source",
			body: `{"schema_version":"1","packs":[{"name":"acme/lenses","source":"git:git@host:path@` +
				strings.Repeat("c", 40) + `","version":"1.0.0","hash":"` + lockHash + `","sig":"` + lockSigDir + `"}]}`,
			valid: true,
		},
		{
			name: "git bare https user source",
			body: `{"schema_version":"1","packs":[{"name":"acme/lenses","source":"git:https://user@host/repo.git@` +
				strings.Repeat("c", 40) + `","version":"1.0.0","hash":"` + lockHash + `","sig":"` + lockSigDir + `"}]}`,
			want: invalidSource,
			code: CodeBadLockSource,
			hide: "user@host",
		},
		{
			name: "git second at source",
			body: `{"schema_version":"1","packs":[{"name":"acme/lenses","source":"git:https://us@er:S3CRET@host/x.git@` +
				strings.Repeat("c", 40) + `","version":"1.0.0","hash":"` + lockHash + `","sig":"` + lockSigDir + `"}]}`,
			want: invalidSource,
			code: CodeBadLockSource,
			hide: "S3CRET",
		},
		{
			name: "git at in password source",
			body: `{"schema_version":"1","packs":[{"name":"acme/lenses","source":"git:https://user:p@ss@host/x.git@` +
				strings.Repeat("c", 40) + `","version":"1.0.0","hash":"` + lockHash + `","sig":"` + lockSigDir + `"}]}`,
			want: invalidSource,
			code: CodeBadLockSource,
			hide: "p@ss",
		},
		{
			name: "git token only source",
			body: `{"schema_version":"1","packs":[{"name":"acme/lenses","source":"git:https://ghp_SUPERSECRET@host/x.git@` +
				strings.Repeat("c", 40) + `","version":"1.0.0","hash":"` + lockHash + `","sig":"` + lockSigDir + `"}]}`,
			want: invalidSource,
			code: CodeBadLockSource,
			hide: "ghp_SUPERSECRET",
		},
		{
			name: "git ssh password source",
			body: `{"schema_version":"1","packs":[{"name":"acme/lenses","source":"git:ssh://user:S3CRET@host/x@` +
				strings.Repeat("c", 40) + `","version":"1.0.0","hash":"` + lockHash + `","sig":"` + lockSigDir + `"}]}`,
			want: invalidSource,
			code: CodeBadLockSource,
			hide: "S3CRET",
		},
		{
			name: "git percent colon source",
			body: `{"schema_version":"1","packs":[{"name":"acme/lenses","source":"git:https://user%3AS3CRET@host/x.git@` +
				strings.Repeat("c", 40) + `","version":"1.0.0","hash":"` + lockHash + `","sig":"` + lockSigDir + `"}]}`,
			want: invalidSource,
			code: CodeBadLockSource,
			hide: "S3CRET",
		},
		{
			name: "git percent at source",
			body: `{"schema_version":"1","packs":[{"name":"acme/lenses","source":"git:https://us%40er:S3CRET@host/x.git@` +
				strings.Repeat("c", 40) + `","version":"1.0.0","hash":"` + lockHash + `","sig":"` + lockSigDir + `"}]}`,
			want: invalidSource,
			code: CodeBadLockSource,
			hide: "S3CRET",
		},
		{
			name: "git http userinfo source",
			body: `{"schema_version":"1","packs":[{"name":"acme/lenses","source":"git:http://ghp_SUPERSECRET@host/x.git@` +
				strings.Repeat("c", 40) + `","version":"1.0.0","hash":"` + lockHash + `","sig":"` + lockSigDir + `"}]}`,
			want: invalidSource,
			code: CodeBadLockSource,
			hide: "ghp_SUPERSECRET",
		},
		{
			name: "git password source",
			body: `{"schema_version":"1","packs":[{"name":"acme/lenses","source":"git:https://user:tok@host/repo.git@` +
				strings.Repeat("c", 40) + `","version":"1.0.0","hash":"` + lockHash + `","sig":"` + lockSigDir + `"}]}`,
			want: invalidSource,
			code: CodeBadLockSource,
			hide: "user:tok@host",
		},
		{
			name: "bad source",
			body: `{"schema_version":"1","packs":[{"name":"acme/lenses","source":"http://x",` +
				`"version":"1.0.0","hash":"` + lockHash + `","sig":"` + lockSigDir + `"}]}`,
			want: `source "<invalid source>"`,
			code: CodeBadLockSource,
		},
		{name: "dir leading dash", body: lockSourceBody("dir:-src"), want: `source "<invalid source>"`, code: CodeBadLockSource},
		{name: "dir newline", body: lockSourceBody("dir:src\npacks"), want: "source", code: CodeBadLockSource},
		{name: "dir backslash", body: lockSourceBody(`dir:src\packs`), want: "source", code: CodeBadLockSource},
		{name: "dir control", body: lockSourceBody("dir:src\x7fpacks"), want: "source", code: CodeBadLockSource},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, ref := parseLock([]byte(tc.body))
			if tc.valid {
				if ref != nil {
					t.Fatalf("parseLock = %s: %s, want it accepted", ref.Code, ref.Message)
				}
				return
			}
			wantCode := tc.code
			if wantCode == "" {
				wantCode = CodeBadPack
			}
			if ref == nil || ref.Code != wantCode {
				t.Fatalf("parseLock = %v, want %s", ref, wantCode)
			}
			if !strings.Contains(ref.Message, tc.want) {
				t.Fatalf("message = %q, want it to contain %q", ref.Message, tc.want)
			}
			if tc.hide != "" && strings.Contains(ref.Message, tc.hide) {
				t.Fatalf("message = %q, carries %q", ref.Message, tc.hide)
			}
		})
	}
}

// TestLockRequiresPacksArray proves a missing or null packs field refuses while
// an explicitly empty array stays a valid lock.
//
//nolint:revive // the missing, null, and empty matrix reads as one block
func TestLockRequiresPacksArray(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		body string
		ok   bool
	}{
		{`{"schema_version":"1"}`, false},
		{`{"schema_version":"1","packs":null}`, false},
		{`{"schema_version":"1","packs":[]}`, true},
	} {
		_, ref := parseLock([]byte(tc.body))
		if tc.ok {
			if ref != nil {
				t.Fatalf("parseLock(%s) = %s: %s, want it accepted", tc.body, ref.Code, ref.Message)
			}
			continue
		}
		if ref == nil || ref.Code != CodeBadPack {
			t.Fatalf("parseLock(%s) = %v, want bad-pack", tc.body, ref)
		}
		if want := "carries no packs array"; !strings.Contains(ref.Message, want) {
			t.Fatalf("message = %q, want it to contain %q", ref.Message, want)
		}
	}
}

// TestWriteLockRefusesOversizedDocument proves an add cannot write a lock
// loadLock would then refuse for size.
func TestWriteLockRefusesOversizedDocument(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	lock := &Lock{SchemaVersion: LockSchemaVersion, Packs: []LockEntry{{
		Name: "acme/lenses", Source: "dir:" + strings.Repeat("x", MaxFileBytes),
		Version: "1.0.0", Hash: lockHash, Sig: lockSigDir,
	}}}
	ref := writeLock(root, lock)
	if ref == nil || ref.Code != CodeBadPack {
		t.Fatalf("writeLock = %v, want bad-pack", ref)
	}
	if want := "over the 65536-byte cap"; !strings.Contains(ref.Message, want) {
		t.Fatalf("message = %q, want it to contain %q", ref.Message, want)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(LockFile))); !os.IsNotExist(err) {
		t.Fatalf("oversized write left %s (stat err %v)", LockFile, err)
	}
}

// TestLockFileIsAControl proves the lock path stays in the fixed controls
// inventory, so the snapshot can never stop carrying the file CheckLock reads.
func TestLockFileIsAControl(t *testing.T) {
	t.Parallel()
	if !slices.Contains(controls.Inventory, LockFile) {
		t.Fatalf("controls inventory %v does not carry %s", controls.Inventory, LockFile)
	}
}

// TestCheckLockAcceptsMatchingTree proves a lock whose entry reproduces the
// vendored tree passes the shared check.
func TestCheckLockAcceptsMatchingTree(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	lockFixture(t, root, lockBody(lockHash, lockSigDir))
	if ref := CheckLock(lockSnapshot(t, root)); ref != nil {
		t.Fatalf("CheckLock = %s: %s, want nil", ref.Code, ref.Message)
	}
}

// TestCheckLockRefusesMissingEntry proves a repo pack with no lock row
// refuses pack-lock-missing and names the pack.
func TestCheckLockRefusesMissingEntry(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	lockFixture(t, root, "")
	ref := CheckLock(lockSnapshot(t, root))
	if ref == nil || ref.Code != CodeLockMissing {
		t.Fatalf("CheckLock = %v, want pack-lock-missing", ref)
	}
	if !strings.Contains(ref.Message, "acme/lenses") || !strings.Contains(ref.Message, LockFile) {
		t.Fatalf("message = %q, want the pack and lock named", ref.Message)
	}
}

// TestCheckLockRefusesUnlistedVendoredPack proves a lock file that exists and
// names one pack still refuses a second vendored pack it does not list.
func TestCheckLockRefusesUnlistedVendoredPack(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	lockFixture(t, root, lockBody(lockHash, lockSigDir))
	writeFixture(t, root, map[string]string{
		".verdict/packs/extra/pack.json": `{"schema_version":"1","name":"acme/extra",` +
			`"version":"2.0.0","lenses":[{"id":"perf"}]}`,
		".verdict/packs/extra/lenses/perf.md": "extra body\n",
	})
	ref := CheckLock(lockSnapshot(t, root))
	if ref == nil || ref.Code != CodeLockMissing {
		t.Fatalf("CheckLock = %v, want pack-lock-missing", ref)
	}
	if !strings.Contains(ref.Message, "acme/extra") {
		t.Fatalf("message = %q, want it to name acme/extra", ref.Message)
	}
}

// TestCheckLockRefusesTreeDrift proves a lock hash that differs from the
// vendored tree refuses pack-lock-drift and names both hashes.
func TestCheckLockRefusesTreeDrift(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	wrong := strings.Repeat("0", 64)
	lockFixture(t, root, lockBody(wrong, LockSig(&LockEntry{
		Name: "acme/lenses", Source: "dir:src-packs/lenses", Version: "1.0.0", Hash: wrong,
	})))
	ref := CheckLock(lockSnapshot(t, root))
	if ref == nil || ref.Code != CodeLockDrift {
		t.Fatalf("CheckLock = %v, want pack-lock-drift", ref)
	}
	for _, want := range []string{wrong, lockHash, "acme/lenses"} {
		if !strings.Contains(ref.Message, want) {
			t.Errorf("message = %q, want it to contain %s", ref.Message, want)
		}
	}
}

// TestCheckLockRefusesEditedSig proves a hand-edited sig refuses drift even
// when the tree hash still matches.
func TestCheckLockRefusesEditedSig(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	lockFixture(t, root, lockBody(lockHash, strings.Repeat("a", 64)))
	ref := CheckLock(lockSnapshot(t, root))
	if ref == nil || ref.Code != CodeLockDrift {
		t.Fatalf("CheckLock = %v, want pack-lock-drift", ref)
	}
	want := "sig " + strings.Repeat("a", 64)
	if !strings.Contains(ref.Message, want) || !strings.Contains(ref.Message, lockSigDir) {
		t.Fatalf("message = %q, want both sigs", ref.Message)
	}
}

// TestCheckLockRefusesGonePack proves a lock entry whose vendored directory
// is gone refuses drift, naming the recorded hash.
func TestCheckLockRefusesGonePack(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFixture(t, root, map[string]string{LockFile: lockBody(lockHash, lockSigDir)})
	ref := CheckLock(lockSnapshot(t, root))
	if ref == nil || ref.Code != CodeLockDrift {
		t.Fatalf("CheckLock = %v, want pack-lock-drift", ref)
	}
	if !strings.Contains(ref.Message, lockHash) || !strings.Contains(ref.Message, "no vendored pack") {
		t.Fatalf("message = %q, want the recorded hash and the missing tree", ref.Message)
	}
}

// TestCheckLockNoPacksNoLock proves the empty repository needs no lock.
func TestCheckLockNoPacksNoLock(t *testing.T) {
	t.Parallel()
	if ref := CheckLock(lockSnapshot(t, t.TempDir())); ref != nil {
		t.Fatalf("CheckLock = %s: %s, want nil", ref.Code, ref.Message)
	}
}
