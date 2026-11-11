package pack

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// TestPackMixedCaseKeysRefuse proves every manifest key must carry its exact
// lowercase spelling: encoding/json would otherwise match a capitalised key
// and let an explicit max_files zero pass as absent.
//
//nolint:revive // the mixed-case key matrix reads as one block
func TestPackMixedCaseKeysRefuse(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		manifest string
		want     string
	}{
		{
			name:     "schema_version",
			manifest: `{"Schema_Version":"1","name":"acme/lenses","version":"1.2.3","lenses":[{"id":"perf"}]}`,
			want:     `"Schema_Version"`,
		},
		{
			name:     "lenses",
			manifest: `{"schema_version":"1","name":"acme/lenses","version":"1.2.3","LENSES":[{"id":"perf"}]}`,
			want:     `"LENSES"`,
		},
		{
			name:     "lens id",
			manifest: `{"schema_version":"1","name":"acme/lenses","version":"1.2.3","lenses":[{"ID":"perf"}]}`,
			want:     `"ID"`,
		},
		{
			name:     "lens max_files",
			manifest: `{"schema_version":"1","name":"acme/lenses","version":"1.2.3","lenses":[{"id":"perf","MAX_FILES":0}]}`,
			want:     `"MAX_FILES"`,
		},
		{
			name:     "rule id",
			manifest: `{"schema_version":"1","name":"acme/lenses","version":"1.2.3","severity":{"s":"critical"},"lenses":[{"id":"perf"}],"rules":[{"ID":"r1","severity":"s","globs":["**/*.go"]}]}`,
			want:     `"ID"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fsys := fstest.MapFS{
				"pack.json":      {Data: []byte(tc.manifest)},
				"lenses/perf.md": {Data: []byte("perf body\n")},
			}
			_, ref := Load(fsys, ".")
			if ref == nil || ref.Code != CodeBadPack {
				t.Fatalf("Load = %v, want bad-pack", ref)
			}
			if !strings.Contains(ref.Message, tc.want) {
				t.Fatalf("message = %q, want it to name %q", ref.Message, tc.want)
			}
		})
	}
}

// openGuardFS counts opens of the named pipe path, proving the walk refuses
// it before any read: a real FIFO would block in Open.
type openGuardFS struct {
	fstest.MapFS
	opened *int
}

func (g openGuardFS) Open(name string) (fs.File, error) {
	if name == "lenses/perf.md" {
		*g.opened++
	}
	return g.MapFS.Open(name)
}

// TestPackNonRegularBodyRefuses proves a FIFO or device entry refuses before
// any read: its size is zero, so only the mode check can stop it.
func TestPackNonRegularBodyRefuses(t *testing.T) {
	t.Parallel()
	opened := 0
	fsys := openGuardFS{MapFS: fstest.MapFS{
		"pack.json": {Data: []byte(
			`{"schema_version":"1","name":"acme/lenses","version":"1.2.3","lenses":[{"id":"perf"}]}`)},
		"lenses/perf.md": {Data: []byte(""), Mode: fs.ModeNamedPipe},
	}, opened: &opened}
	_, ref := Load(fsys, ".")
	if ref == nil || ref.Code != CodePackUnsafePath {
		t.Fatalf("named-pipe lens = %v, want pack-unsafe-path", ref)
	}
	if opened != 0 {
		t.Fatalf("the named pipe was opened %d time(s), want the walk to refuse before Open", opened)
	}
}

// endlessFS serves one endless body for a file whose walked size is zero, so
// only the read bound can stop it.
type endlessFS struct{ fstest.MapFS }

func (e endlessFS) Open(name string) (fs.File, error) {
	f, err := e.MapFS.Open(name)
	if err != nil || name != "lenses/perf.md" {
		return f, err
	}
	return endlessFile{File: f}, nil
}

type endlessFile struct{ fs.File }

func (f endlessFile) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	return len(p), nil
}

var _ io.Reader = endlessFile{}

// TestPackBoundedReadRefusesGrowth proves the body read is bounded to
// MaxFileBytes+1: a file that grew after the walk refuses instead of reading
// without limit.
func TestPackBoundedReadRefusesGrowth(t *testing.T) {
	t.Parallel()
	fsys := endlessFS{fstest.MapFS{
		"pack.json": {Data: []byte(
			`{"schema_version":"1","name":"acme/lenses","version":"1.2.3","lenses":[{"id":"perf"}]}`)},
		"lenses/perf.md": {Data: []byte("")},
	}}
	_, ref := Load(fsys, ".")
	if ref == nil || ref.Code != CodePackTooLarge {
		t.Fatalf("endless lens = %v, want pack-too-large", ref)
	}
}

// TestPackSymlinkedRootRefuses proves the pack root itself is Lstat'ed: a
// symlink to a real pack refuses instead of following it.
func TestPackSymlinkedRootRefuses(t *testing.T) {
	t.Parallel()
	target := t.TempDir()
	writeFile(t, filepath.Join(target, "pack.json"),
		`{"schema_version":"1","name":"acme/lenses","version":"1.2.3","lenses":[{"id":"perf"}]}`)
	writeFile(t, filepath.Join(target, "lenses", "perf.md"), "perf body\n")
	link := filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("this platform cannot create symlinks here: %v", err)
	}
	if _, ref := LoadDir(link); ref == nil || ref.Code != CodePackUnsafePath {
		t.Fatalf("symlinked root = %v, want pack-unsafe-path", ref)
	}
}
