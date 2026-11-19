package journal

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	envXDGStateHome = "XDG_STATE_HOME"
	envHome         = "HOME"
	envLocalAppData = "LOCALAPPDATA"
	envUserProfile  = "USERPROFILE"
	osWindows       = "windows"
	repoKeyBytes    = 8
	journalExt      = ".jsonl"
)

// StateDir returns the base state directory for the journal from env:
// $XDG_STATE_HOME when set, else %LOCALAPPDATA% on Windows, else
// ~/.local/state (falling back to %USERPROFILE%\.local\state on Windows).
// It returns "" when env supplies none of those variables.
func StateDir(env map[string]string) string {
	if v := strings.TrimSpace(env[envXDGStateHome]); v != "" {
		return v
	}
	if runtime.GOOS == osWindows {
		return windowsStateDir(env)
	}
	return unixHomeStateDir(env)
}

func windowsStateDir(env map[string]string) string {
	if v := strings.TrimSpace(env[envLocalAppData]); v != "" {
		return v
	}
	if home := unixHomeStateDir(env); home != "" {
		return home
	}
	if up := strings.TrimSpace(env[envUserProfile]); up != "" {
		return filepath.Join(up, ".local", "state")
	}
	return ""
}

func unixHomeStateDir(env map[string]string) string {
	if home := strings.TrimSpace(env[envHome]); home != "" {
		return filepath.Join(home, ".local", "state")
	}
	return ""
}

// CanonicalRepoRoot returns the cleaned, absolute, symlink-resolved path for
// repoRoot so CLI, MCP, SDK, and tests hash the same key for one repository.
func CanonicalRepoRoot(repoRoot string) (string, error) {
	if repoRoot == "" {
		repoRoot = "."
	}
	abs, err := filepath.Abs(repoRoot)
	if err != nil {
		return "", wrapErr("resolve repo root "+repoRoot, err)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	return filepath.Clean(abs), nil
}

// RepoKey returns the 16-hex-character key derived from the SHA-256 of the
// canonical absolute repository root.
func RepoKey(absRepoRoot string) string {
	sum := sha256.Sum256([]byte(absRepoRoot))
	return hex.EncodeToString(sum[:repoKeyBytes])
}

// Path resolves the external journal file path
// (<state>/verdict/journal/<repo-key>/<bundle>.jsonl) and canonical repo root.
// It returns ok=false when state dir, bundle name, or repo root is unusable.
func Path(repoRoot, bundleName string, env map[string]string) (path, absRepo string, ok bool) {
	state := StateDir(env)
	if state == "" || !validBundleName(bundleName) {
		return "", "", false
	}
	abs, err := CanonicalRepoRoot(repoRoot)
	if err != nil {
		return "", "", false
	}
	p := filepath.Join(state, "verdict", "journal", RepoKey(abs), bundleName+journalExt)
	return p, abs, true
}

func validBundleName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	return !strings.ContainsAny(name, `/\`)
}
