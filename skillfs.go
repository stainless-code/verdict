// Package skillfs embeds the shipped skill payload from skills/verdict/ and
// the published schemas from schemas/. Go embed cannot reach parent
// directories, so this root package embeds both in place instead of generating
// copies. The binary serves both under the paths an installed skill uses:
// "prompts/review.md" and "schemas/review.schema.json".
package skillfs

import (
	"embed"
	"fmt"
	"io"
	"io/fs"
	"slices"
	"strings"
)

// SkillMD is the skill entry point, relative to the payload root.
const SkillMD = "SKILL.md"

// Dir is the checkout directory that holds the skill payload: the one
// directory the skills CLI discovers and copies, so it carries nothing but
// that payload.
const Dir = "skills/verdict"

// SchemasDir is the checkout directory that holds the published schemas. It
// is embedded separately but served under the same name in the payload.
const SchemasDir = "schemas"

// payload is the embedded skill directory: SKILL.md, NOTICE.md (the
// upstream license the adapted prompts travel with), prompts/, packs/ (the
// built-in lens pack), references/, and the POSIX shim that resolves the
// binary from a skill copy. Patterns name files and subtrees, never the bare
// directory, so a stray file beside them is not embedded (and
// TestEmbedMatchesRepo fails on it).
//
//go:embed skills/verdict/SKILL.md skills/verdict/NOTICE.md skills/verdict/prompts
//go:embed skills/verdict/packs skills/verdict/scripts/verdict skills/verdict/references
var payload embed.FS

// published is the repository's published schemas, served at "schemas/...".
//
//go:embed schemas
var published embed.FS

// FS serves the skill payload and the published schemas under one root.
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

type embeddedFS struct {
	skill   fs.FS
	schemas fs.FS
}

func newFS() fs.FS {
	sub, err := fs.Sub(payload, Dir)
	if err != nil {
		panic("skillfs: " + err.Error())
	}
	return embeddedFS{skill: sub, schemas: published}
}

func (f embeddedFS) Open(name string) (fs.File, error) {
	if name == rootPath {
		return openRoot(f)
	}
	if name == SchemasDir || strings.HasPrefix(name, SchemasDir+"/") {
		file, err := f.schemas.Open(name)
		if err != nil {
			return nil, wrapEmbedded(verbOpen, name, err)
		}
		return file, nil
	}
	file, err := f.skill.Open(name)
	if err != nil {
		return nil, wrapEmbedded(verbOpen, name, err)
	}
	return file, nil
}

func openRoot(f embeddedFS) (fs.File, error) {
	skillRoot, err := f.skill.Open(rootPath)
	if err != nil {
		return nil, wrapEmbedded(verbOpen, Dir, err)
	}
	defer skillRoot.Close()
	info, err := skillRoot.Stat()
	if err != nil {
		return nil, wrapEmbedded(verbStat, Dir, err)
	}
	skillDir, ok := skillRoot.(fs.ReadDirFile)
	if !ok {
		return nil, wrapEmbedded(verbOpen, Dir+" is not a directory", fs.ErrInvalid)
	}
	skillEntries, err := skillDir.ReadDir(-1)
	if err != nil {
		return nil, wrapEmbedded(verbRead, Dir, err)
	}
	schemaRoot, err := f.schemas.Open(rootPath)
	if err != nil {
		return nil, wrapEmbedded(verbOpen, SchemasDir, err)
	}
	defer schemaRoot.Close()
	schemaDir, ok := schemaRoot.(fs.ReadDirFile)
	if !ok {
		return nil, wrapEmbedded(verbOpen, SchemasDir+" is not a directory", fs.ErrInvalid)
	}
	schemaEntries, err := schemaDir.ReadDir(-1)
	if err != nil {
		return nil, wrapEmbedded(verbRead, SchemasDir, err)
	}
	entries := make([]fs.DirEntry, 0, len(skillEntries)+len(schemaEntries))
	entries = append(entries, skillEntries...)
	entries = append(entries, schemaEntries...)
	slices.SortFunc(entries, func(a, b fs.DirEntry) int {
		return strings.Compare(a.Name(), b.Name())
	})
	return &rootDir{info: info, entries: entries}, nil
}

type rootDir struct {
	info    fs.FileInfo
	entries []fs.DirEntry
	read    bool
}

func (d *rootDir) Stat() (fs.FileInfo, error) { return d.info, nil }

func (d *rootDir) Read([]byte) (int, error) { return 0, io.EOF }

func (d *rootDir) Close() error { return nil }

func (d *rootDir) ReadDir(n int) ([]fs.DirEntry, error) {
	if d.read {
		return nil, io.EOF
	}
	if n > 0 && n < len(d.entries) {
		out := d.entries[:n]
		d.entries = d.entries[n:]
		return out, nil
	}
	d.read = true
	return d.entries, nil
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
		return nil, fmt.Errorf("walk embedded skill: %v", err)
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
