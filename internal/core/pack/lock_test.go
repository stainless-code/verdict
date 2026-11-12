package pack

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/schemacheck"
)

const (
	lockManifest = `{"schema_version":"1","name":"acme/lenses","version":"1.0.0","lenses":[{"id":"perf"}]}`
	lockHash     = "96680942582c5c2639601918eabe079884e86e5b00a040b6a209e5681be88d41"
	lockHashV2   = "be3ac14b258cbe5fc3a59669a4aa203a262e288373e3a7e4f0b33624adfdaca5"
	lockSigDir   = "16084c3131b76e2c546ea22d1afe5d50ae83c612da2ca039b638551244a37677"
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
// the loader agree on a clean dir: path and reject a leading dash, newline,
// backslash, or control character.
//
//nolint:gocognit,revive // the source grammar matrix reads as one block
func TestLockSourceSchemaMatchesLoader(t *testing.T) {
	t.Parallel()
	checker, err := schemacheck.Load(
		filepath.Join("..", "..", "..", "skills", "verdict", "schemas", "lock.schema.json"), "")
	if err != nil {
		t.Fatalf("load lock schema: %v", err)
	}
	for _, tc := range []struct {
		source string
		valid  bool
	}{
		{"dir:../packs", true},
		{"dir:src-packs/lenses", true},
		{"git:https://user@host/repo.git@" + strings.Repeat("c", 40), true},
		{"dir:-src", false},
		{"dir:src\npacks", false},
		{`dir:src\packs`, false},
		{"dir:src\x7fpacks", false},
	} {
		t.Run(tc.source, func(t *testing.T) {
			t.Parallel()
			body := lockSourceBody(tc.source)
			_, ref := parseLock([]byte(body))
			if tc.valid && ref != nil {
				t.Fatalf("parseLock = %s: %s, want it accepted", ref.Code, ref.Message)
			}
			if !tc.valid && (ref == nil || ref.Code != CodeBadPack) {
				t.Fatalf("parseLock = %v, want bad-pack", ref)
			}
			failures := checker.Check(decodeJSON(t, body))
			if tc.valid && len(failures) != 0 {
				t.Fatalf("schema rejected %q: %v", tc.source, failures)
			}
			if !tc.valid && len(failures) == 0 {
				t.Fatalf("schema accepted refused source %q", tc.source)
			}
		})
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
			name: "git userinfo source",
			body: `{"schema_version":"1","packs":[{"name":"acme/lenses","source":"git:https://user@host/repo.git@` +
				strings.Repeat("c", 40) + `","version":"1.0.0","hash":"` + lockHash + `","sig":"` + lockSigDir + `"}]}`,
			valid: true,
		},
		{
			name: "git password source",
			body: `{"schema_version":"1","packs":[{"name":"acme/lenses","source":"git:https://user:tok@host/repo.git@` +
				strings.Repeat("c", 40) + `","version":"1.0.0","hash":"` + lockHash + `","sig":"` + lockSigDir + `"}]}`,
			want: "user:***@host",
			hide: "user:tok@host",
		},
		{
			name: "bad source",
			body: `{"schema_version":"1","packs":[{"name":"acme/lenses","source":"http://x",` +
				`"version":"1.0.0","hash":"` + lockHash + `","sig":"` + lockSigDir + `"}]}`,
			want: `source "http://x"`,
		},
		{name: "dir leading dash", body: lockSourceBody("dir:-src"), want: `source "dir:-src"`},
		{name: "dir newline", body: lockSourceBody("dir:src\npacks"), want: "source"},
		{name: "dir backslash", body: lockSourceBody(`dir:src\packs`), want: "source"},
		{name: "dir control", body: lockSourceBody("dir:src\x7fpacks"), want: "source"},
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
			if ref == nil || ref.Code != CodeBadPack {
				t.Fatalf("parseLock = %v, want bad-pack", ref)
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
