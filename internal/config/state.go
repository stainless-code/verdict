package config

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/stainless-code/verdict/internal/atomic"
)

// State layout: the `.verdict` directory itself is never ignored. Only
// `/state/` is ignored through the nested file, so review state stays
// local while every control stays trackable. Legacy `.review/` is
// untouched: no migration, no rewrite, no shim.
const (
	// VerdictDir is the repository configuration directory at the Git root.
	VerdictDir = ".verdict"
	// ConfigPath is the one tracked typed configuration, at the Git root.
	ConfigPath = ".verdict/config.json"
	// KBPath is the tracked shared knowledge corpus beside the config.
	KBPath = ".verdict/kb.jsonl"
	// LearningsPath is the tracked shared heading memory.
	LearningsPath = ".verdict/learnings.md"
	// OverridesDir is the tracked shared override fragments directory.
	OverridesDir = ".verdict/overrides"
	// GitignorePath carries the single tracked-shape `/state/` line.
	GitignorePath = ".verdict/.gitignore"
	// StateDirName is the ignored local review state directory.
	StateDirName = "state"
	// gitignoreStateLine is the entire tracked ignore contract.
	gitignoreStateLine = "/state/\n"
	// stateDirPerm creates the configuration directory the ignore file
	// lives in.
	stateDirPerm = 0o755
)

// stateError is the package's sealed error type. State writes fail
// locally (permissions, storage); callers surface the message without
// inspecting the cause.
type stateError struct {
	cause error
	msg   string
}

// Error implements error.
func (e *stateError) Error() string { return e.msg }

// Unwrap exposes the cause to [errors.Is]/[errors.As].
func (e *stateError) Unwrap() error { return e.cause }

// Layout resolves the state paths under one repository root.
type Layout struct {
	// Root is the Git root the layout was built for.
	Root string
}

// NewLayout builds the layout for root, defaulting empty root to the
// working directory.
func NewLayout(root string) Layout {
	if root == "" {
		root = "."
	}
	return Layout{Root: root}
}

// VerdictDir returns the configuration directory path.
func (l Layout) VerdictDir() string { return filepath.Join(l.Root, VerdictDir) }

// ConfigFile returns the tracked configuration path.
func (l Layout) ConfigFile() string { return filepath.Join(l.Root, ConfigPath) }

// StateDir returns the ignored local state directory path.
func (l Layout) StateDir() string {
	return filepath.Join(l.Root, VerdictDir, StateDirName)
}

// GitignoreFile returns the nested ignore file path.
func (l Layout) GitignoreFile() string { return filepath.Join(l.Root, GitignorePath) }

// EnsureStateIgnore creates the `.verdict` directory and its `.gitignore`
// with the `/state/` line before any state is written, so generated state
// is never tracked. It never runs on reads: validation, explanation, and
// preview paths must not create files, shared or local. An existing
// ignore file is preserved, gaining the line only when missing.
func (l Layout) EnsureStateIgnore() *stateError {
	if err := os.MkdirAll(l.VerdictDir(), stateDirPerm); err != nil {
		return &stateError{msg: "could not create " + l.VerdictDir(), cause: err}
	}
	path := l.GitignoreFile()
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if werr := atomic.WriteFile(path, []byte(gitignoreStateLine)); werr != nil {
			return &stateError{msg: "could not write " + path, cause: werr}
		}
		return nil
	}
	if err != nil {
		return &stateError{msg: "could not read " + path, cause: err}
	}
	if hasStateLine(string(raw)) {
		return nil
	}
	return appendIgnoreLine(path, raw)
}

// appendIgnoreLine adds the `/state/` line to an existing ignore file,
// preserving its trailing newline contract.
func appendIgnoreLine(path string, raw []byte) *stateError {
	line := gitignoreStateLine
	if len(raw) > 0 && raw[len(raw)-1] != '\n' {
		line = "\n" + line
	}
	if werr := atomic.WriteFile(path, append(raw, line...)); werr != nil {
		return &stateError{msg: "could not write " + path, cause: werr}
	}
	return nil
}

// hasStateLine reports whether the ignore file already ignores /state/.
func hasStateLine(raw string) bool {
	for _, line := range splitIgnoreLines(raw) {
		if line == "/state/" || line == "/state" || line == "state/" || line == "state" {
			return true
		}
	}
	return false
}

// splitIgnoreLines splits on newlines, trimming trailing carriage
// returns and whitespace.
func splitIgnoreLines(raw string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(raw); i++ {
		if i == len(raw) || raw[i] == '\n' {
			line := raw[start:i]
			line = trimRight(line)
			out = append(out, line)
			start = i + 1
		}
	}
	return out
}

// trimRight strips trailing spaces, tabs, and carriage returns.
func trimRight(s string) string {
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
