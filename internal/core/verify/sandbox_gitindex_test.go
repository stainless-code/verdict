package verifycore

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// gitIndexSeeds are real indexes git wrote (testdata/gitindex): versions 2,
// 3 (an intent-to-add entry sets the extended flags), and 4, SHA-1 and
// SHA-256, each with the gitlink `lib/sub` among entries sharing its prefix
// and a cache-tree extension.
func gitIndexSeeds(tb testing.TB) map[string][]byte {
	tb.Helper()
	paths, err := filepath.Glob(filepath.Join("testdata", "gitindex", "*.idx"))
	if err != nil || len(paths) == 0 {
		tb.Fatalf("no git index seeds (%v)", err)
	}
	seeds := map[string][]byte{}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			tb.Fatal(err)
		}
		seeds[filepath.Base(path)] = data
	}
	return seeds
}

// TestGitlinksFromIndexReadsEverySeed pins the reader against each real
// index layout.
func TestGitlinksFromIndexReadsEverySeed(t *testing.T) {
	for name, data := range gitIndexSeeds(t) {
		t.Run(name, func(t *testing.T) {
			if links, reason := gitlinksFromIndex(data); reason != "" || !slices.Equal(links, []string{"lib/sub"}) {
				t.Errorf("gitlinks = %q (reason %q), want [lib/sub]", links, reason)
			}
		})
	}
}

// crashExtendedPastEnd is a 94-byte v3 index whose only entry sets the
// extended flag with no room left for it: skipping the extra flags word ran
// the read position past the end, and the name read sliced [76:74].
func crashExtendedPastEnd() []byte {
	entry := make([]byte, gitIndexStatLen+gitIndexSHA1Len+gitIndexFlagsLen)
	binary.BigEndian.PutUint32(entry[gitIndexModeOffset:], 0o100644)
	binary.BigEndian.PutUint16(entry[len(entry)-gitIndexFlagsLen:], gitIndexExtended|1)
	return gitIndexBytes(3, entry, nil)
}

// crashVarintOverflow is a v4 index whose first name opens with nine
// continuation bytes: the strip count, decoded in a signed int, overflowed
// to a negative value and sliced the previous name out of range.
func crashVarintOverflow() []byte {
	entry := make([]byte, gitIndexStatLen+gitIndexSHA1Len+gitIndexFlagsLen)
	binary.BigEndian.PutUint32(entry[gitIndexModeOffset:], 0o100644)
	name := append(bytes.Repeat([]byte{gitIndexVarintMore}, 9), 0, 0)
	return gitIndexBytes(gitIndexMaxVersion, entry, name)
}

// gitIndexBytes assembles a one-entry index with a zeroed SHA-1 trailer.
func gitIndexBytes(version uint32, entry, name []byte) []byte {
	data := append([]byte{}, gitIndexSignature...)
	data = binary.BigEndian.AppendUint32(data, version)
	data = binary.BigEndian.AppendUint32(data, 1)
	data = append(append(data, entry...), name...)
	return append(data, make([]byte, gitIndexSHA1Len)...)
}

// TestGitlinksFromIndexRefusesTheCrashers keeps both crashers the fuzz
// target found refused rather than panicking.
func TestGitlinksFromIndexRefusesTheCrashers(t *testing.T) {
	for name, data := range map[string][]byte{
		"extended flags past the end": crashExtendedPastEnd(),
		"varint overflow":             crashVarintOverflow(),
	} {
		t.Run(name, func(t *testing.T) {
			if links, reason := gitlinksFromIndex(data); reason == "" {
				t.Errorf("gitlinks = %q with no refusal, want the index refused", links)
			}
		})
	}
}

// FuzzGitIndex feeds the index reader arbitrary bytes: it must never panic,
// and whatever it accepts must be a list of non-empty paths without NULs.
func FuzzGitIndex(f *testing.F) {
	for _, data := range gitIndexSeeds(f) {
		f.Add(data)
	}
	f.Add(crashExtendedPastEnd())
	f.Add(crashVarintOverflow())
	f.Fuzz(func(t *testing.T, data []byte) {
		if links, reason := gitlinksFromIndex(data); reason == "" {
			requireWellFormedGitlinks(t, links)
		}
	})
}

// requireWellFormedGitlinks fails on an accepted gitlink that is empty or
// carries a NUL.
func requireWellFormedGitlinks(t *testing.T, links []string) {
	t.Helper()
	for _, link := range links {
		if link == "" || strings.ContainsRune(link, 0) {
			t.Fatalf("accepted gitlink %q", link)
		}
	}
}

// TestSandboxGitlinksRefusesAnOversizedIndex caps the index read: past
// 64 MiB the reader refuses instead of loading it.
func TestSandboxGitlinksRefusesAnOversizedIndex(t *testing.T) {
	index := filepath.Join(t.TempDir(), "index")
	f, err := os.Create(index)
	mustSandboxFS(t, err)
	mustSandboxFS(t, f.Truncate(gitIndexMaxBytes+1))
	mustSandboxFS(t, f.Close())
	links, present, reason := sandboxGitlinks(index)
	if !present || reason != "larger than 64 MiB" {
		t.Errorf("gitlinks = %q (present %v, reason %q), want the size refusal", links, present, reason)
	}
}

// TestGitIndexVarintBoundsItsEncoding pins git's offset varint: each
// continuation adds one before shifting (0x80 0x00 is 128, not 0), and an
// encoding longer than five bytes is malformed rather than decoded.
func TestGitIndexVarintBoundsItsEncoding(t *testing.T) {
	for _, tc := range []struct {
		data []byte
		want uint64
		ok   bool
	}{
		{data: []byte{0x05}, want: 5, ok: true},
		{data: []byte{0x80, 0x00}, want: 128, ok: true},
		{data: []byte{0x80, 0x80, 0x80, 0x80, 0x00}, want: 0x10204080, ok: true},
		{data: []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x00}},
	} {
		r := &gitIndexReader{data: tc.data, end: len(tc.data)}
		if got, ok := r.varint(); got != tc.want || ok != tc.ok {
			t.Errorf("varint(% x) = %d, %v; want %d, %v", tc.data, got, ok, tc.want, tc.ok)
		}
	}
}

// TestGitlinksFromIndexRefusesAnEmptyName keeps an entry with an empty name,
// which git never writes, from being accepted as part of a valid index.
func TestGitlinksFromIndexRefusesAnEmptyName(t *testing.T) {
	entry := make([]byte, gitIndexStatLen+gitIndexSHA1Len+gitIndexFlagsLen)
	binary.BigEndian.PutUint32(entry[gitIndexModeOffset:], 0o100644)
	data := gitIndexBytes(gitIndexMinVersion, entry, []byte{0, 0})
	if links, reason := gitlinksFromIndex(data); reason == "" {
		t.Errorf("gitlinks = %q with no refusal, want the empty name refused", links)
	}
}
