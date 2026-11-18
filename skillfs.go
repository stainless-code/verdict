// Package skillfs embeds the Bootstrap skill from skills/verdict/, the
// Procedure from procedure/, and the published schemas from schemas/. Go embed
// cannot reach parent directories, so this root package embeds each tree in
// place and serves them through one read-only multi-root fs. Inventory paths
// keep their pre-split names: "prompts/review.md", "packs/…", "references/…",
// and "schemas/review.schema.json".
package skillfs

import (
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"slices"
	"strings"
)

// SkillMD is the stub entry point, relative to the payload root.
const SkillMD = "SKILL.md"

// ProcedureMD is the Procedure index, served as the default `verdict skill`
// body. It lives in the embedded payload, not in the installed stub.
const ProcedureMD = "PROCEDURE.md"

// StubVersion is the stub contract version served by this binary. The
// Bootstrap skill's frontmatter carries the same integer and `skill status`
// compares the installed value against it.
const StubVersion = "1"

// Dir is the checkout directory the skills CLI discovers and copies, so it
// carries only the three Bootstrap skill files.
const Dir = "skills/verdict"

// ProcedureDir is the checkout directory that holds the Procedure payload.
// Its files are served at the same inventory paths the skill used before the
// split, with PROCEDURE.md added.
const ProcedureDir = "procedure"

// SchemasDir is the checkout directory that holds the published schemas. It
// is embedded separately but served under the same name in the payload.
const SchemasDir = "schemas"

// stub is the installed Bootstrap skill: the shim, its notice, and the entry
// point. Patterns name files explicitly, so a stray file beside them is not
// embedded (and TestEmbedMatchesRepo fails on it).
//
//go:embed skills/verdict/SKILL.md skills/verdict/NOTICE.md skills/verdict/scripts/verdict
var stub embed.FS

// procedure is the task instruction payload: the Procedure index, prompts,
// built-in lens pack, and references. All of it is served by the binary and
// none of it is copied into an installed skill.
//
//go:embed procedure
var procedure embed.FS

// published is the repository's published schemas, served at "schemas/...".
//
//go:embed schemas
var published embed.FS

// FS serves the stub, the Procedure, and the published schemas under one root.
var FS = newFS()

const rootPath = "."

const (
	verbOpen = "open"
	verbRead = "read"
	verbStat = "stat"
)

type embeddedError struct {
	cause error
	verb  string
	name  string
}

func (e *embeddedError) Error() string { return e.verb + " " + e.name + ": " + e.cause.Error() }

func (e *embeddedError) Unwrap() error { return e.cause }

func wrapEmbedded(verb, name string, cause error) error {
	return &embeddedError{verb: verb, name: name, cause: cause}
}

// multiFS resolves one inventory path against each root in order. A missing
// file in one root is a normal miss; any other error is preserved.
type multiFS struct {
	roots []fs.FS
}

func newFS() fs.FS {
	stubRoot, err := fs.Sub(stub, Dir)
	if err != nil {
		panic("skillfs: " + err.Error())
	}
	procedureRoot, err := fs.Sub(procedure, ProcedureDir)
	if err != nil {
		panic("skillfs: " + err.Error())
	}
	return multiFS{roots: []fs.FS{stubRoot, procedureRoot, published}}
}

func (m multiFS) Open(name string) (fs.File, error) {
	if name == rootPath {
		return openRoot(m)
	}
	for _, root := range m.roots {
		file, err := root.Open(name)
		if err == nil {
			return file, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, wrapEmbedded(verbOpen, name, err)
		}
	}
	return nil, wrapEmbedded(verbOpen, name, fs.ErrNotExist)
}

// openRoot merges the roots' top-level entries into one sorted directory. The
// first root supplies the directory metadata; every root supplies entries.
func openRoot(m multiFS) (fs.File, error) {
	var info fs.FileInfo
	var entries []fs.DirEntry
	for i, root := range m.roots {
		got, rootInfo, err := readRootEntries(root, rootName(i))
		if err != nil {
			return nil, err
		}
		if i == 0 {
			info = rootInfo
		}
		entries = append(entries, got...)
	}
	slices.SortFunc(entries, func(a, b fs.DirEntry) int {
		return strings.Compare(a.Name(), b.Name())
	})
	return &rootDir{info: info, entries: entries}, nil
}

// readRootEntries opens one overlay root and drains its directory, closing
// the handle before the merged root returns it to the caller.
func readRootEntries(root fs.FS, name string) ([]fs.DirEntry, fs.FileInfo, error) {
	dir, err := root.Open(rootPath)
	if err != nil {
		return nil, nil, wrapEmbedded(verbOpen, name, err)
	}
	info, err := dir.Stat()
	if err != nil {
		_ = dir.Close()
		return nil, nil, wrapEmbedded(verbStat, name, err)
	}
	readDir, ok := dir.(fs.ReadDirFile)
	if !ok {
		_ = dir.Close()
		return nil, nil, wrapEmbedded(verbOpen, name+" is not a directory", fs.ErrInvalid)
	}
	entries, err := readDir.ReadDir(-1)
	_ = dir.Close()
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, nil, wrapEmbedded(verbRead, name, err)
	}
	return entries, info, nil
}

func rootName(i int) string {
	switch i {
	case 0:
		return Dir
	case 1:
		return ProcedureDir
	default:
		return SchemasDir
	}
}

type rootDir struct {
	info    fs.FileInfo
	entries []fs.DirEntry
}

func (d *rootDir) Stat() (fs.FileInfo, error) { return d.info, nil }

func (d *rootDir) Read([]byte) (int, error) { return 0, io.EOF }

func (d *rootDir) Close() error { return nil }

func (d *rootDir) ReadDir(n int) ([]fs.DirEntry, error) {
	if len(d.entries) == 0 {
		if n <= 0 {
			return nil, nil
		}
		return nil, io.EOF
	}
	if n <= 0 || n >= len(d.entries) {
		out := d.entries
		d.entries = nil
		return out, nil
	}
	out := d.entries[:n]
	d.entries = d.entries[n:]
	return out, nil
}

const (
	execMode = 0o755
	fileMode = 0o644
)

var executable = map[string]bool{
	"schemas/validate.sh": true,
	"scripts/verdict":     true,
}

// Paths lists every embedded payload path in slash form, sorted.
func Paths() ([]string, error) {
	var out []string
	err := fs.WalkDir(FS, rootPath, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		//nolint:wrapcheck,errorlint // %v seals the boundary by design (GO-ERRORS-03)
		return nil, fmt.Errorf("walk embedded payload: %v", err)
	}
	slices.Sort(out)
	return out, nil
}

// Read returns one embedded payload file by its slash path.
func Read(name string) ([]byte, error) {
	raw, err := fs.ReadFile(FS, name)
	if err != nil {
		//nolint:wrapcheck,errorlint // %v seals the boundary by design (GO-ERRORS-03)
		return nil, fmt.Errorf("read embedded %s: %v", name, err)
	}
	return raw, nil
}

// Mode is the mode a payload file installs with: 0755 for the executable
// shim and schema validator, 0644 for everything else.
func Mode(name string) fs.FileMode {
	if executable[name] {
		return execMode
	}
	return fileMode
}
