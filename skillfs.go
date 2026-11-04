// Package skillfs embeds the shipped skill payload from skills/verdict/, the
// one directory that holds it: go:embed cannot reach parent directories, so
// this root package embeds it in place instead of a generated copy. The
// skills CLI installs that same directory from the registry, so the binary,
// the served skill, and `npx skills add` are byte-identical by construction
// and `verdict skill` can never serve a version that differs from the binary
// that built it.
package skillfs

import (
	"embed"
	"fmt"
	"io/fs"
	"slices"
)

// SkillMD is the skill entry point, relative to the payload root.
const SkillMD = "SKILL.md"

// Dir is the checkout directory that holds the payload: the one directory the
// skills CLI discovers and copies, so it carries nothing but the payload —
// including NOTICE.md, so an installed copy carries the adapted prompts'
// upstream license rather than depending on the repository root.
const Dir = "skills/verdict"

// payload is the embedded skill directory: SKILL.md, NOTICE.md (the
// upstream license the adapted prompts travel with), prompts/, schemas/, and
// the POSIX shim that resolves the binary from a skill copy. Patterns name
// files and subtrees, never the bare directory, so a stray file beside them
// is not embedded (and TestEmbedMatchesRepo fails on it).
//
//go:embed skills/verdict/SKILL.md skills/verdict/NOTICE.md skills/verdict/prompts skills/verdict/schemas skills/verdict/scripts/verdict
var payload embed.FS

// FS is the payload rooted at the skill directory: paths read "SKILL.md",
// "prompts/review.md", never "skills/verdict/...".
var FS = mustSub()

func mustSub() fs.FS {
	sub, err := fs.Sub(payload, Dir)
	if err != nil {
		panic("skillfs: " + err.Error())
	}
	return sub
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
	err := fs.WalkDir(FS, ".", func(path string, entry fs.DirEntry, err error) error {
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
