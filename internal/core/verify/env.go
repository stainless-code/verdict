package verifycore

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/envsecret"
)

const codeBadFlags = "bad-flags"

var defaultAllowlistNames = []string{
	"PATH", "HOME", "LANG", "TERM", "TMPDIR", "TZ", "CI", "USER", "LOGNAME",
	"SHELL", "COLORTERM", "NO_COLOR", "FORCE_COLOR", "CLICOLOR", "CLICOLOR_FORCE",
	"XDG_CACHE_HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME",
	"GOPATH", "GOCACHE", "GOMODCACHE", "GOROOT", "GOTOOLCHAIN",
	"GOPROXY", "GOPRIVATE", "GONOPROXY", "GONOSUMDB", "CGO_ENABLED", "CC", "CXX",
	"CARGO_HOME", "RUSTUP_HOME",
	"NPM_CONFIG_CACHE", "PNPM_HOME", "BUN_INSTALL", "DENO_DIR",
	"VIRTUAL_ENV", "UV_CACHE_DIR", "PIP_CACHE_DIR",
}

var windowsAllowlistNames = []string{
	"TEMP", "TMP", "PATHEXT", "COMSPEC", "SYSTEMROOT", "WINDIR",
	"USERPROFILE", "APPDATA", "LOCALAPPDATA", "PROGRAMDATA",
	"PROGRAMFILES", "PROGRAMFILES(X86)", "COMMONPROGRAMFILES",
	"OS", "NUMBER_OF_PROCESSORS", "PROCESSOR_ARCHITECTURE",
	"SYSTEMDRIVE", "HOMEDRIVE", "HOMEPATH", "MSYSTEM",
}

func defaultAllowlistSet(isWindows bool) map[string]bool {
	set := make(map[string]bool, len(defaultAllowlistNames)+len(windowsAllowlistNames))
	for _, name := range defaultAllowlistNames {
		set[name] = true
	}
	if isWindows {
		for _, name := range windowsAllowlistNames {
			set[name] = true
		}
	}
	return set
}

func (st *state) prepareEnv(dir, base string) *core.RefusalError {
	if err := validateEnvNames("--env-allowlist", st.req.EnvAllowlist); err != nil {
		return err
	}
	repoNames, share, ref := readControls(dir, base)
	if ref != nil {
		return ref
	}
	st.share = share
	explicit := make([]string, 0, len(st.req.EnvAllowlist)+len(repoNames))
	explicit = append(explicit, st.req.EnvAllowlist...)
	explicit = append(explicit, repoNames...)

	env, dropped := filterChildEnv(st.req.Environ, explicit, st.req.JSONMode, runtime.GOOS == "windows")
	st.env = env
	if len(dropped) > 0 {
		st.warn(formatDroppedWarning(dropped))
	}
	return nil
}

func validateEnvNames(source string, names []string) *core.RefusalError {
	for _, name := range names {
		if !validEnvName(name) {
			return &core.RefusalError{
				Code:    codeBadFlags,
				Message: core.Msgf("invalid environment variable name in %s (want [A-Za-z_][A-Za-z0-9_]*)", source),
			}
		}
	}
	return nil
}

func validEnvName(name string) bool {
	if name == "" || !isEnvStart(name[0]) {
		return false
	}
	for i := 1; i < len(name); i++ {
		if !isEnvCont(name[i]) {
			return false
		}
	}
	return true
}

func isEnvStart(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == '_'
}

func isEnvCont(c byte) bool {
	return isEnvStart(c) || (c >= '0' && c <= '9')
}

// readControls reads the verify inputs the bundle's base-snapshotted
// controls carry: the child-environment opt-ins in .verdict/verify-env and
// the shared directories in .verdict/config.json's verify.share. Both come
// from the PR base, never the head, so a branch cannot widen either.
func readControls(dir, base string) (names, share []string, ref *core.RefusalError) {
	snap, ref := baseControls(dir, base)
	if ref != nil || snap == nil {
		return nil, nil, ref
	}
	if raw, ok := snap.ReadFile(controls.VerifyEnvPath); ok {
		if names, ref = parseVerifyEnvFile(raw); ref != nil {
			return nil, nil, ref
		}
	}
	if share, ref = readShareConfig(snap); ref != nil {
		return nil, nil, ref
	}
	return names, share, nil
}

// baseControls opens the bundle's verified controls snapshot, or nil when
// the bundle carries none.
func baseControls(dir, base string) (*controls.Snapshot, *core.RefusalError) {
	if _, err := os.Stat(filepath.Join(dir, controls.FileName)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, &core.RefusalError{
			Code:    controls.CodeUnavailable,
			Message: core.Msgf("cannot inspect bundle controls %s: %v", controls.FileName, err),
		}
	}
	return controls.Gate(dir, base)
}

// readShareConfig reads verify.share from the base configuration; a base
// configuration that does not parse refuses, as it does for every verb.
func readShareConfig(snap *controls.Snapshot) ([]string, *core.RefusalError) {
	raw, ok := snap.ReadFile(config.ConfigPath)
	if !ok {
		return nil, nil
	}
	cfg, ref := config.ParseBase(&config.BaseConfig{Raw: raw, Present: true})
	if ref != nil || cfg == nil || cfg.Verify == nil {
		return nil, ref
	}
	return cfg.Verify.Share, nil
}

func parseVerifyEnvFile(raw []byte) ([]string, *core.RefusalError) {
	var names []string
	lines := strings.Split(string(raw), "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if !validEnvName(trimmed) {
			return nil, &core.RefusalError{
				Code: codeBadFlags,
				Message: core.Msgf(
					"invalid environment variable name on %s line %d (want [A-Za-z_][A-Za-z0-9_]*)",
					controls.VerifyEnvPath, i+1,
				),
			}
		}
		names = append(names, trimmed)
	}
	return names, nil
}

func envKey(name string, caseInsensitive bool) string {
	if caseInsensitive {
		return strings.ToUpper(name)
	}
	return name
}

func shouldKeepEnvVar(name, key string, explicit, defaultAllow map[string]bool) bool {
	if explicit[key] {
		return true
	}
	if envsecret.IsSecret(name) {
		return false
	}
	return defaultAllow[key] || strings.HasPrefix(key, "LC_")
}

func envNameSet(names []string, isWindows bool) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, name := range names {
		set[envKey(name, isWindows)] = true
	}
	return set
}

func appendDefaultNoColor(kept []string, jsonMode, isWindows bool) []string {
	if !jsonMode {
		return kept
	}
	noColorKey := envKey("NO_COLOR", isWindows)
	for _, kv := range kept {
		name, _, _ := strings.Cut(kv, "=")
		if envKey(name, isWindows) == noColorKey {
			return kept
		}
	}
	return append(kept, "NO_COLOR=1")
}

func filterChildEnv(environ, explicit []string, jsonMode, isWindows bool) (kept, dropped []string) {
	// Non-nil empty slice prevents os/exec from inheriting os.Environ().
	kept = make([]string, 0, len(environ)+1)
	explicitSet := envNameSet(explicit, isWindows)
	defaultAllow := defaultAllowlistSet(isWindows)
	droppedSet := make(map[string]bool)

	for _, kv := range environ {
		name, _, ok := strings.Cut(kv, "=")
		if !ok || name == "" {
			continue
		}
		if shouldKeepEnvVar(name, envKey(name, isWindows), explicitSet, defaultAllow) {
			kept = append(kept, kv)
		} else {
			droppedSet[name] = true
		}
	}
	if len(droppedSet) == 0 {
		return appendDefaultNoColor(kept, jsonMode, isWindows), nil
	}
	return appendDefaultNoColor(kept, jsonMode, isWindows), slices.Sorted(maps.Keys(droppedSet))
}

func formatDroppedWarning(dropped []string) string {
	return fmt.Sprintf(
		"verify dropped %d non-allowlisted child env vars (%s); pass --env-allowlist or .verdict/verify-env to include",
		len(dropped), strings.Join(dropped, ", "),
	)
}
