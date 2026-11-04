// Package statedir is the prototype state-root resolver: one function that
// answers where run state lives, and a tiny TOML reader for the [state] dir
// override. Prototype only — throwaway branch, never merged.
package statedir

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/stainless-code/verdict/internal/jq"
)

// Sources name the winning layer in the override chain.
const (
	SourceDefault = "default"
	SourceFlag    = "flag"
	SourceEnv     = "env"
	SourceControl = "control"
)

// Scopes name how the root was derived.
const (
	ScopeRepo = "repo"
	ScopeDir  = "dir"
)

const (
	// EnvVar is the environment override.
	EnvVar = "VERDICT_STATE"
	// ConfigName is the committed controls file (prototype: [state] dir).
	ConfigName = "verdict.toml"
	// DirName is the default state directory under the repo root.
	DirName = ".verdict"
	// ControlsName is the local controls directory under the state root.
	ControlsName = "controls"
	// EnvControls is the prototype controls-mode switch.
	EnvControls = "VERDICT_PROTO_CONTROLS"
	// ModeCommitted selects committed controls.
	ModeCommitted = "committed"
	// ModeLocal is the default.
	ModeLocal = "local"
	// parentDir and the bundle prefixes keep path predicates readable.
	parentDir         = ".."
	localBundlePrefix = "local-"
	corpusPrefix      = "corpus-"
	// stateDirPerm is the mode the state root is created with.
	stateDirPerm = 0o755
	// dotGitignorePerm is owner-only; git only reads it.
	dotGitignorePerm = 0o600
)

// State is one resolved state root.
//
//nolint:govet // prototype: field order follows the envelope, not padding
type State struct {
	Root     string
	Scope    string
	Source   string
	Writable bool
	// Base is the repository toplevel (or cwd outside a repo).
	Base string
}

var (
	mu      sync.Mutex
	cached  *State
	flagDir string
)

// SetFlag records --state-dir. main calls it once after the global strip.
func SetFlag(dir string) {
	mu.Lock()
	defer mu.Unlock()
	flagDir = dir
	cached = nil
}

// Reset clears the cache (tests and child processes).
func Reset() {
	mu.Lock()
	defer mu.Unlock()
	cached = nil
}

// Resolve returns the state root once per process.
func Resolve() State {
	mu.Lock()
	defer mu.Unlock()
	if cached != nil {
		return *cached
	}
	st := resolve()
	cached = &st
	return st
}

func resolve() State {
	start := cwd()
	if proj := os.Getenv("CLAUDE_PROJECT_DIR"); proj != "" {
		if abs, err := filepath.Abs(proj); err == nil {
			start = abs
		}
	}
	base, scope := repoBase(start)
	st := State{Base: base, Scope: scope, Source: SourceDefault, Root: filepath.Join(base, DirName)}

	switch {
	case flagDir != "":
		st.Root, st.Source = absFrom(start, flagDir), SourceFlag
	case os.Getenv(EnvVar) != "":
		st.Root, st.Source = absFrom(base, os.Getenv(EnvVar)), SourceEnv
	default:
		if dir := configStateDir(base); dir != "" {
			st.Root, st.Source = absFrom(base, dir), SourceControl
		}
	}
	if !within(st.Root, base) {
		st.Scope = ScopeDir
	}
	st.Root = filepath.Clean(st.Root)
	st.Writable = probeWritable(st.Root)
	return st
}

// repoBase finds the git toplevel from start, falling back to start.
//
//nolint:noctx // the resolver has no context seam; git rev-parse is instant
func repoBase(start string) (string, string) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = start
	if out, err := cmd.Output(); err == nil {
		if top := strings.TrimSpace(string(out)); top != "" {
			return top, ScopeRepo
		}
	}
	return start, ScopeDir
}

func cwd() string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	return dir
}

func absFrom(base, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(base, p)
}

func within(path, base string) bool {
	rel, err := filepath.Rel(base, path)
	return err == nil && rel != parentDir && !strings.HasPrefix(rel, parentDir+string(filepath.Separator))
}

// probeWritable reports whether the root can be created and written, without
// creating the root itself: the nearest existing ancestor is probed with a
// temp file. It never reads $HOME.
//
//nolint:revive,gosec // prototype: the operator names the state root
func probeWritable(root string) bool {
	dir := root
	for {
		fi, err := os.Stat(dir)
		if err == nil {
			if !fi.IsDir() {
				return false
			}
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
	f, err := os.CreateTemp(dir, ".verdict-probe-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return true
}

// Ensure creates the root and its self-ignoring .gitignore. It is called by
// the first write of a run, never by a read verb.
func (s State) Ensure() error {
	if !s.Writable {
		return &UnwritableError{State: s}
	}
	if err := os.MkdirAll(s.Root, stateDirPerm); err != nil {
		return &UnwritableError{State: s, Err: err}
	}
	ignore := filepath.Join(s.Root, ".gitignore")
	if _, err := os.Stat(ignore); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(ignore, []byte("*\n"), dotGitignorePerm); err != nil {
			return &UnwritableError{State: s, Err: err}
		}
	}
	return nil
}

// UnwritableError is the typed state-unwritable refusal payload.
//
//nolint:govet // prototype: field order follows the payload, not padding
type UnwritableError struct {
	State State
	Err   error
}

// Error renders the refusal, naming path, winning source, and scope.
func (e *UnwritableError) Error() string {
	msg := fmt.Sprintf("state root %s is not writable (source %s, scope %s)", e.State.Root, e.State.Source, e.State.Scope)
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	return msg
}

// BundleDir joins a bundle name onto the resolved root (absolute).
func (s State) BundleDir(pr string) string {
	if strings.HasPrefix(pr, localBundlePrefix) || strings.HasPrefix(pr, corpusPrefix) {
		return filepath.Join(s.Root, pr)
	}
	if isDir(filepath.Join(s.Root, localBundlePrefix+pr)) && !exists(filepath.Join(s.Root, pr, "PR.json")) {
		return filepath.Join(s.Root, localBundlePrefix+pr)
	}
	return filepath.Join(s.Root, pr)
}

// ControlsDir is the local-mode controls directory.
func (s State) ControlsDir() string { return filepath.Join(s.Root, ControlsName) }

// ControlsLabel names one control's source for human messages and envelope
// sources: the committed path in committed mode, the local snapshot path in
// local mode.
func ControlsLabel(name string) string {
	if ControlsMode() == ModeCommitted {
		switch name {
		case "panel", "budget", "max_bytes", "chunk", "kb-policy":
			return "verdict.toml [review]"
		case "learnings.md", "kb.jsonl":
			return filepath.Join("verdict", name)
		}
		if strings.HasPrefix(name, "overrides/") {
			return filepath.Join("verdict", name)
		}
		return filepath.Join("verdict", name)
	}
	return filepath.Join(Resolve().ControlsDir(), name)
}

// RelPrefix is the root as a cwd-relative slash-terminated prefix (the form
// git porcelain lines carry), or the absolute root when it sits outside cwd.
func (s State) RelPrefix() string {
	rel, err := filepath.Rel(cwd(), s.Root)
	if err != nil || rel == parentDir || strings.HasPrefix(rel, parentDir+string(filepath.Separator)) {
		return s.Root + string(filepath.Separator)
	}
	return filepath.ToSlash(rel) + "/"
}

// ControlsMode reads the prototype switch: committed|local (default local).
func ControlsMode() string {
	if os.Getenv(EnvControls) == ModeCommitted {
		return ModeCommitted
	}
	return ModeLocal
}

// Wire renders the envelope `state` object.
func Wire() any {
	st := Resolve()
	return jq.NewObj(
		"root", st.Root,
		"scope", st.Scope,
		"source", st.Source,
		"writable", st.Writable,
	)
}

// IsLocalBundleDir reports whether an absolute bundle dir is a local bundle.
func IsLocalBundleDir(dir string) bool {
	base := filepath.Base(dir)
	return strings.HasPrefix(base, localBundlePrefix) || strings.HasPrefix(base, corpusPrefix)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func isDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// configStateDir reads [state] dir from <base>/verdict.toml.
//
//nolint:gosec // base is the resolved repository root
func configStateDir(base string) string {
	raw, err := os.ReadFile(filepath.Join(base, ConfigName))
	if err != nil {
		return ""
	}
	return ParseTOML(raw)["state.dir"]
}

// ParseTOML is a prototype flat parser: section headers plus scalar
// key = value lines, rendered unquoted. It understands no arrays, tables, or
// multiline strings.
//
//nolint:revive // prototype: a flat parser is intentionally one pass
func ParseTOML(raw []byte) map[string]string {
	out := map[string]string{}
	section := ""
	for line := range strings.SplitSeq(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if i := strings.Index(line, "#"); i >= 0 && !strings.Contains(line[:i], "\"") {
			line = strings.TrimSpace(line[:i])
		}
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if section != "" {
			key = section + "." + key
		}
		out[key] = unquote(strings.TrimSpace(val))
	}
	return out
}

func unquote(v string) string {
	if len(v) >= 2 {
		if (v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'') {
			return v[1 : len(v)-1]
		}
	}
	return v
}
