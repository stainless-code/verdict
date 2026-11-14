package readcommon

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// home is one prepared bundle store: a regular file, a listing directory, and
// the root both live under.
type home struct {
	root string
	file string
	dir  string
}

// newHome plants the three fixtures the StoreEntries cases share.
func newHome(t *testing.T) home {
	t.Helper()
	root := t.TempDir()
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("not a bundle home\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "dir")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "one.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return home{root: root, file: file, dir: dir}
}

// TestStoreEntriesAbsentHomeIsEmpty pins the stat's ErrNotExist branch: a home
// no verb has written yet reads as empty, not as a refusal.
func TestStoreEntriesAbsentHomeIsEmpty(t *testing.T) {
	absent := filepath.Join(newHome(t).root, "absent")
	if entries, err := StoreEntries(absent); err != nil || entries != nil {
		t.Fatalf("absent home = (%v, %v); want (nil, nil)", entries, err)
	}
}

// TestStoreEntriesRefusesARegularFile pins the non-directory branch: the
// refusal names the home in the one sentence every verb uses.
func TestStoreEntriesRefusesARegularFile(t *testing.T) {
	file := newHome(t).file
	entries, err := StoreEntries(file)
	if err == nil || !strings.Contains(err.Error(), "bundle store "+file+" is unreadable") {
		t.Fatalf("regular file = (%v, %v); want one unreadable sentence naming it", entries, err)
	}
}

// TestStoreEntriesRefusesAFileWhoseReadDirAnswers pins the Windows shape: a
// regular file opened as a directory reports [fs.ErrNotExist], so a ReadDir
// that answers instead of failing must still not read as a store.
func TestStoreEntriesRefusesAFileWhoseReadDirAnswers(t *testing.T) {
	file := newHome(t).file
	ghost := func(string) ([]os.DirEntry, error) { return []os.DirEntry{fakeEntry("ghost")}, nil }
	if entries, err := storeEntries(file, os.Stat, ghost); err == nil {
		t.Fatalf("regular file with a willing ReadDir = (%v, nil); want the stat decision", entries)
	}
}

// TestStoreEntriesListsADirectory pins the listing itself.
func TestStoreEntriesListsADirectory(t *testing.T) {
	dir := newHome(t).dir
	entries, err := StoreEntries(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "one.json" {
		t.Fatalf("listed home = (%v, %v); want the one file", entries, err)
	}
}

// TestStoreEntriesRefusesAnUnlistableHome pins the ReadDir error branch.
func TestStoreEntriesRefusesAnUnlistableHome(t *testing.T) {
	locked := unlistableHome(t, newHome(t).root)
	entries, err := StoreEntries(locked)
	if err == nil || !strings.Contains(err.Error(), "bundle store "+locked+" is unreadable") {
		t.Fatalf("unlistable home = (%v, %v); want one unreadable sentence naming it", entries, err)
	}
}

// TestStoreEntriesRefusesAHomeItCannotStat pins the stat error branch.
func TestStoreEntriesRefusesAHomeItCannotStat(t *testing.T) {
	locked := unlistableHome(t, newHome(t).root)
	if entries, err := StoreEntries(filepath.Join(locked, "child")); err == nil {
		t.Fatalf("home under an unreadable parent = (%v, nil); want the stat error", entries)
	}
}

// unlistableHome makes a 0o000 directory under root and returns it.
func unlistableHome(t *testing.T, root string) string {
	t.Helper()
	skipUnwritable(t)
	locked := filepath.Join(root, "locked")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(locked, 0o755); err != nil {
			t.Logf("restore mode: %v", err)
		}
	})
	return locked
}

// fakeEntry is a DirEntry a willing ReadDir hands back.
type fakeEntry string

func (f fakeEntry) Name() string               { return string(f) }
func (f fakeEntry) IsDir() bool                { return false }
func (f fakeEntry) Type() fs.FileMode          { return 0 }
func (f fakeEntry) Info() (fs.FileInfo, error) { return nil, fs.ErrNotExist }

// skipUnwritable skips the permission cases where a mode bit stops nothing:
// Windows has no POSIX mode, and root reads a 0o000 directory.
func skipUnwritable(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("chmod does not deny a listing on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root lists a 0o000 directory")
	}
}
