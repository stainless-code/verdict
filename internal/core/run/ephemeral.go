package run

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/agentcatalog"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/hostsandbox"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// Every review launch of a row with ephemeral state — whether verdict's OS
// sandbox wraps it, its own sandbox does, or neither — points each variable
// at a throwaway home in its private TMPDIR, seeded with no-follow copies of
// the few files the row names (its login, its model configuration), so
// nothing the review writes there outlives the launch, and nothing outside
// those copies is loaded from the real one.

// seedCap bounds one seed file, and seedMode is its copy's mode: a login,
// the owner's alone.
const (
	seedCap  = 1 << 20
	seedMode = 0o600
)

// ephemeralEnv makes the row's ephemeral homes under tmp, seeds them from the
// real directories env names (an absolute relocation value, else the
// home-relative default), and returns the environment pointing at them, with
// a warning for each login database it could not read. A real directory
// reached through a symlink, a relocation that is unsafe as a state root, or
// a seed or login database that is a link, not a regular file, or oversized
// refuses the launch; a missing directory, seed, or database is left out.
func ephemeralEnv(def *agentcatalog.Launch, env []string, tmp string) (homes, warnings []string, err error) {
	if len(def.StateEphemeral) == 0 {
		return nil, nil, nil
	}
	home, ok := hostsandbox.Home(env)
	if !ok || !filepath.IsAbs(home) {
		return nil, nil, jq.NewErrf("HOME is unset or relative, so %s's real home cannot be named", def.Name)
	}
	homes = make([]string, 0, len(def.StateEphemeral))
	for _, v := range def.StateEphemeral {
		entries, warning, err := makeHome(def.Name, v, filepath.Clean(home), env, tmp)
		if err != nil {
			return nil, nil, err
		}
		homes = append(homes, entries...)
		warnings = appendWarning(warnings, warning)
	}
	return homes, warnings, nil
}

// appendWarning appends a warning that is not empty.
func appendWarning(warnings []string, w string) []string {
	if w == "" {
		return warnings
	}
	return append(warnings, w)
}

// makeHome makes one variable's throwaway home under tmp, seeds it, and
// returns the entries pointing the launch at it.
func makeHome(name string, v agentcatalog.EphemeralVar, home string, env []string, tmp string,
) (entries []string, warning string, err error) {
	source, err := ephemeralSource(v, home, env)
	if err != nil {
		return nil, "", err
	}
	dir := filepath.Join(tmp, "home-"+strings.ToLower(v.Name))
	if err := os.MkdirAll(dir, viewDirMode); err != nil {
		return nil, "", jq.Sealed("make a throwaway home", err)
	}
	if err := seedHome(source.path, dir, v); err != nil {
		return nil, "", err
	}
	entries = []string{v.Name + inlineValue + dir}
	if v.LoginDB == nil {
		return entries, "", nil
	}
	entry, warning, err := seedLoginDB(name, v.LoginDB, source.path, dir, env)
	if err != nil {
		return nil, "", err
	}
	return append(entries, entry), warning, nil
}

// ephemeralSource names the real directory a variable's home is seeded from
// and checks it as state: a relocation root that is a link, the filesystem
// root, HOME, or an ancestor of HOME refuses, and so does a path with a
// symlink between it and its base.
func ephemeralSource(v agentcatalog.EphemeralVar, home string, env []string) (statePath, error) {
	s := statePath{path: filepath.Join(home, filepath.FromSlash(v.Root)), base: home}
	if value := hostsandbox.EnvLookup(env, v.Name); filepath.IsAbs(value) {
		s = statePath{path: filepath.Clean(value), base: filepath.Clean(value), relocated: true}
	}
	if err := checkBase(s, hostsandbox.ExpandRealpaths([]string{home})); err != nil {
		return s, err
	}
	return s, s.linkFree()
}

// seedHome copies each seed below the real directory into the throwaway
// home, through a root handle and without following any link.
func seedHome(source, dir string, v agentcatalog.EphemeralVar) error {
	root, err := os.OpenRoot(source)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return jq.Sealed("open "+source, err)
	}
	defer func() { _ = root.Close() }()
	for _, rel := range v.Seed {
		if err := seedOne(root, filepath.Join(dir, filepath.FromSlash(rel)), rel, v.SeedKeys[rel]); err != nil {
			return jq.NewErrf("the seed %s: %v", filepath.Join(source, filepath.FromSlash(rel)), err)
		}
	}
	return nil
}

// seedOne copies one seed to dst, keeping only keys of a JSON seed when the
// row names them; a missing seed, or a JSON one that is not an object, is
// left out.
func seedOne(root *os.Root, dst, rel string, keys []string) error {
	raw, err := readSeed(root, filepath.FromSlash(rel))
	if err != nil || raw == nil {
		return err
	}
	if keys != nil {
		if raw = filterSeed(rel, raw, keys); raw == nil {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(dst), viewDirMode); err != nil {
		return jq.Sealed("make a throwaway home", err)
	}
	// dst is a catalog-cited seed joined under the launch's own TMPDIR.
	if err := os.WriteFile(dst, raw, seedMode); err != nil {
		return jq.Sealed("seed a throwaway home", err)
	}
	return nil
}

// readSeed reads one seed below root, nil for a missing one: every directory
// on its way must be a directory, not a link, and the file a regular file
// opened without following a link, at most seedCap bytes.
func readSeed(root *os.Root, rel string) ([]byte, error) {
	if present, err := dirsOnTheWay(root, filepath.Dir(rel)); err != nil || !present {
		return nil, err
	}
	f, err := readcommon.OpenNoFollowIn(root, rel)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err //nolint:wrapcheck // the caller names the seed
	}
	defer func() { _ = f.Close() }()
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		return nil, jq.NewErrf("not a regular file")
	}
	raw, err := io.ReadAll(io.LimitReader(f, seedCap+1))
	switch {
	case err != nil:
		return nil, jq.Sealed("read", err)
	case len(raw) > seedCap:
		return nil, jq.NewErrf("larger than a login or configuration file")
	}
	return raw, nil
}

// dirsOnTheWay checks each directory below root on the way to a seed: false
// when one is missing, an error when one is a link or not a directory.
func dirsOnTheWay(root *os.Root, dir string) (bool, error) {
	at := ""
	for part := range strings.SplitSeq(dir, string(filepath.Separator)) {
		if part == here {
			continue
		}
		at = filepath.Join(at, part)
		info, err := root.Lstat(at)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return false, nil
		case err != nil || !info.IsDir():
			return false, jq.NewErrf("%s on its way is a link or not a directory", at)
		}
	}
	return true, nil
}

// filterSeed keeps only the allowed keys of a JSON or TOML seed, by its
// extension.
func filterSeed(rel string, raw []byte, keys []string) []byte {
	if strings.HasSuffix(rel, ".toml") {
		return keepTOMLKeys(raw, keys)
	}
	return keepKeys(raw, keys)
}

// tomlOther marks a TOML table no allowed key lives in.
const tomlOther = "-"

// keepTOMLKeys keeps only the allowed lines of a TOML configuration: a bare
// `key = value` at the top level whose key is in keys, and in each
// [<table>.<id>] table a bare key listed as "<table>.*.<key>". Every other
// table (MCP servers, profiles, features, projects, hooks), array table,
// dotted or quoted key, comment, and value that does not end on its line is
// dropped, so nothing the allowlist does not name reaches the copy.
func keepTOMLKeys(raw []byte, keys []string) []byte {
	var out strings.Builder
	table := ""
	for line := range strings.Lines(string(raw)) {
		var kept string
		kept, table = tomlLine(strings.TrimSpace(line), table, keys)
		out.WriteString(kept)
	}
	return []byte(strings.TrimPrefix(out.String(), newline))
}

// newline ends each kept TOML line.
const newline = "\n"

// tomlLine is what one trimmed line keeps, and the table it leaves open: a
// header the allowlist reaches is kept (after a blank line), an allowed key
// line is kept, and everything else is dropped.
func tomlLine(t, table string, keys []string) (kept, next string) {
	switch {
	case t == "" || strings.HasPrefix(t, "#"):
		return "", table
	case strings.HasPrefix(t, "["):
		if next = tomlTable(t, keys); next != tomlOther {
			return newline + t + newline, next
		}
		return "", next
	}
	if line, ok := tomlKeyLine(t, table, keys); ok {
		return line + newline, table
	}
	return "", table
}

// tomlTable is the allowlist pattern a table header opens ("<table>.*"), or
// tomlOther when no allowed key lives in it: an array table, a header with
// anything after it, or a name that is not <table>.<bare id>.
func tomlTable(header string, keys []string) string {
	end := strings.IndexByte(header, ']')
	if strings.HasPrefix(header, "[[") || end < 0 || strings.TrimSpace(header[end+1:]) != "" {
		return tomlOther
	}
	parent, id, ok := strings.Cut(strings.TrimSpace(header[1:end]), ".")
	if !ok || !bareTOMLKey(parent) || !bareTOMLKey(id) ||
		!slices.ContainsFunc(keys, func(k string) bool { return strings.HasPrefix(k, parent+".*.") }) {
		return tomlOther
	}
	return parent + ".*"
}

// tomlKeyLine returns one `key = value` line when its bare key is allowed in
// the current table and its value ends on the line.
func tomlKeyLine(line, table string, keys []string) (string, bool) {
	if table == tomlOther {
		return "", false
	}
	key, value, ok := strings.Cut(line, "=")
	key, value = strings.TrimSpace(key), strings.TrimSpace(value)
	name := key
	if table != "" {
		name = table + "." + key
	}
	if !ok || !bareTOMLKey(key) || !oneLineTOMLValue(value) || !slices.Contains(keys, name) {
		return "", false
	}
	return key + " = " + value, true
}

// bareTOMLKey reports a TOML bare key: letters, digits, '_' and '-'.
func bareTOMLKey(key string) bool {
	return key != "" && strings.Trim(key, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-") == ""
}

// oneLineTOMLValue reports a value that ends on its own line: not a
// multi-line string, and an array or inline table that closes there.
func oneLineTOMLValue(value string) bool {
	switch {
	case value == "", strings.HasPrefix(value, `"""`), strings.HasPrefix(value, "'''"):
		return false
	case strings.HasPrefix(value, "["):
		return strings.HasSuffix(value, "]")
	case strings.HasPrefix(value, "{"):
		return strings.HasSuffix(value, "}")
	}
	return true
}

// keepKeys keeps only the named top-level keys of a JSON object; nil when it
// is not one, so the copy is left out and the CLI starts from its defaults.
func keepKeys(raw []byte, keys []string) []byte {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil
	}
	kept := make(map[string]json.RawMessage, len(keys))
	for _, k := range keys {
		if v, ok := doc[k]; ok {
			kept[k] = v
		}
	}
	out, err := jq.Marshal(kept)
	if err != nil {
		return nil
	}
	return out
}

// throwawayHomes points the row's ephemeral variables at their throwaway
// homes in the view's private TMPDIR, for every launch that starts a host
// process; a home that cannot be made refuses the launch.
func (st *state) throwawayHomes(def *agentcatalog.Launch, v *view, env []string) ([]string, error) {
	if st.box == nil {
		return env, nil
	}
	homes, warnings, err := ephemeralEnv(def, absConfigHomes(st.childEnv()), v.tmp)
	if err != nil {
		return nil, &core.StartError{Err: jq.NewErrf("could not make its throwaway home (%v) — nothing ran", err)}
	}
	for _, w := range warnings {
		if !slices.Contains(st.warnings, w) {
			st.warn(w)
		}
	}
	return hostsandbox.OverrideEnv(env, homes), nil
}
