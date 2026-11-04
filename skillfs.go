// Package skillfs embeds the shipped skill payload from the repository root,
// where the files live: go:embed cannot reach parent directories, so the
// payload lives in a root package instead of a generated copy. The binary and
// the served skill are byte-identical by construction, so `verdict skill`
// can never serve a version that differs from the binary that built it.
package skillfs

import (
	"embed"
	"fmt"
	"io/fs"
	"slices"
)

// SkillMD is the skill entry point, relative to the payload root.
const SkillMD = "SKILL.md"

// FS is the embedded payload: SKILL.md, prompts/, schemas/, and the POSIX
// shim that resolves the binary from a skill copy.
//
//go:embed SKILL.md prompts schemas scripts/verdict
var FS embed.FS

// Payload install modes.
const (
	execMode = 0o755
	fileMode = 0o644
)

// executable lists the payload files installed with the executable bit.
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
	raw, err := FS.ReadFile(name)
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
