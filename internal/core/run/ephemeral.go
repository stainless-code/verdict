package run

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
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
// home-relative default), and returns the environment pointing at them. A
// real directory reached through a symlink, a relocation that is unsafe as a
// state root, or a seed that is a link, not a regular file, or oversized
// refuses the launch; a missing directory or seed is left out.
func ephemeralEnv(def *agentcatalog.Launch, env []string, tmp string) ([]string, error) {
	if len(def.StateEphemeral) == 0 {
		return nil, nil
	}
	home, ok := hostsandbox.Home(env)
	if !ok || !filepath.IsAbs(home) {
		return nil, jq.NewErrf("HOME is unset or relative, so %s's real home cannot be named", def.Name)
	}
	out := make([]string, 0, len(def.StateEphemeral))
	for _, v := range def.StateEphemeral {
		dir, err := makeHome(v, filepath.Clean(home), env, tmp)
		if err != nil {
			return nil, err
		}
		out = append(out, v.Name+inlineValue+dir)
	}
	return out, nil
}

// makeHome makes one variable's throwaway home under tmp and seeds it.
func makeHome(v agentcatalog.EphemeralVar, home string, env []string, tmp string) (string, error) {
	source, err := ephemeralSource(v, home, env)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(tmp, "home-"+strings.ToLower(v.Name))
	if err := os.MkdirAll(dir, viewDirMode); err != nil {
		return "", jq.Sealed("make a throwaway home", err)
	}
	return dir, seedHome(source.path, dir, v)
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
		if raw = keepKeys(raw, keys); raw == nil {
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
	homes, err := ephemeralEnv(def, absConfigHomes(st.childEnv()), v.tmp)
	if err != nil {
		return nil, &core.StartError{Err: jq.NewErrf("could not make its throwaway home (%v) — nothing ran", err)}
	}
	return hostsandbox.OverrideEnv(env, homes), nil
}
