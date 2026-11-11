package archtest

import (
	"go/ast"
	"go/build"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// windowsOnlyFuncs records the Windows helpers that have no non-Windows peer
// on purpose, keyed "<dir>:<func>" with the reason. An entry that stops naming
// an unpaired function fails, so the allowlist cannot rot.
var windowsOnlyFuncs = map[string]string{
	"internal/atomic:retryableReplace":   "retries the Windows-only rename locks",
	"internal/core/exec:closeHandle":     "closes a Win32 handle whose outcome is already decided",
	"internal/core/exec:pid32":           "narrows a pid to the DWORD a Win32 call takes",
	"internal/core/exec:resumeProcess":   "resumes a suspended Windows process",
	"internal/core/exec:resumeThread":    "resumes a suspended Windows thread",
	"internal/core/exec:taskkillTree":    "kills a Windows tree through taskkill",
	"internal/core/exec:threadsOf":       "reads the threads of a suspended Windows process",
	"internal/core/exec:treeFor":         "wraps the Windows Job Object handle",
	"internal/core/exec:win32Call":       "runs a Win32 proc whose result word rules the call",
	"internal/core/upgrade:pid32":        "narrows a pid to the DWORD OpenProcess takes",
	"internal/core/upgrade:waitOnHandle": "polls a process handle until it signals or the context ends",
}

var nonWindowsTargets = []string{"linux", "darwin", "freebsd", "openbsd", "netbsd", "dragonfly"}

// unpairedFunc is one Windows-only declaration with no non-Windows peer.
type unpairedFunc struct {
	file string
	name string
}

// TestPlatformTwins fails when a package-level function declared in a
// *_windows.go file has no same-named declaration in a file the package builds
// on another OS. The Windows build is the one CI cannot exercise end to end,
// so a Windows-only function must be a recorded decision, not an accident.
func TestPlatformTwins(t *testing.T) {
	t.Parallel()
	root := moduleRoot(t)
	unpaired := map[string]unpairedFunc{}
	for dir, files := range windowsFilesByDir(t, root) {
		maps.Copy(unpaired, unpairedFuncs(t, root, dir, files))
	}
	for _, key := range slices.Sorted(maps.Keys(unpaired)) {
		if windowsOnlyFuncs[key] != "" {
			continue
		}
		fn := unpaired[key]
		t.Errorf("%s declares func %s with no non-Windows peer; add the peer or record it in windowsOnlyFuncs with a reason",
			fn.file, fn.name)
	}
	for _, key := range slices.Sorted(maps.Keys(windowsOnlyFuncs)) {
		if _, ok := unpaired[key]; !ok {
			t.Errorf("windowsOnlyFuncs entry %q is stale: the function has a non-Windows peer or no longer exists", key)
		}
	}
}

// unpairedFuncs returns the Windows declarations in one package that no
// non-Windows build of it declares.
func unpairedFuncs(t *testing.T, root, dir string, files []string) map[string]unpairedFunc {
	t.Helper()
	peers := nonWindowsFuncs(t, dir)
	rel := relativeDir(t, root, dir)
	unpaired := make(map[string]unpairedFunc)
	for _, file := range files {
		for _, name := range packageFuncs(t, filepath.Join(dir, file)) {
			if peers[name] {
				continue
			}
			unpaired[rel+":"+name] = unpairedFunc{file: filepath.ToSlash(filepath.Join(rel, file)), name: name}
		}
	}
	return unpaired
}

func windowsFilesByDir(t *testing.T, root string) map[string][]string {
	t.Helper()
	dirs := make(map[string][]string)
	for _, top := range []string{"cmd", "internal", "pkg"} {
		if err := filepath.WalkDir(filepath.Join(root, top), collectWindowsFiles(dirs)); err != nil {
			t.Fatalf("walk %s: %v", top, err)
		}
	}
	if len(dirs) == 0 {
		t.Fatal("no *_windows.go files found; the guard would pass vacuously")
	}
	return dirs
}

func collectWindowsFiles(dirs map[string][]string) fs.WalkDirFunc {
	return func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return skipSourceDir(entry.Name())
		}
		if strings.HasSuffix(entry.Name(), "_windows.go") {
			dir := filepath.Dir(path)
			dirs[dir] = append(dirs[dir], entry.Name())
		}
		return nil
	}
}

func skipSourceDir(name string) error {
	switch name {
	case "node_modules", "testdata", "vendor":
		return filepath.SkipDir
	default:
		return nil
	}
}

func nonWindowsFuncs(t *testing.T, dir string) map[string]bool {
	t.Helper()
	peers := make(map[string]bool)
	for _, name := range goFileNames(t, dir) {
		if !matchesNonWindows(dir, name) {
			continue
		}
		for _, fn := range packageFuncs(t, filepath.Join(dir, name)) {
			peers[fn] = true
		}
	}
	return peers
}

// matchesNonWindows asks the build system whether dir/name compiles for any
// non-Windows target, reading the same constraints the compiler does.
func matchesNonWindows(dir, name string) bool {
	for _, goos := range nonWindowsTargets {
		ctxt := build.Default
		ctxt.GOOS = goos
		ctxt.GOARCH = "amd64"
		ctxt.CgoEnabled = false
		ok, err := ctxt.MatchFile(dir, name)
		if err == nil && ok {
			return true
		}
	}
	return false
}

func goFileNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		if isGoSource(entry) {
			names = append(names, entry.Name())
		}
	}
	return names
}

func isGoSource(entry fs.DirEntry) bool {
	name := entry.Name()
	return !entry.IsDir() && strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")
}

func packageFuncs(t *testing.T, path string) []string {
	t.Helper()
	var names []string
	for _, decl := range parseGo(t, path).Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil {
			names = append(names, fn.Name.Name)
		}
	}
	return names
}

func relativeDir(t *testing.T, root, dir string) string {
	t.Helper()
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.ToSlash(rel)
}
