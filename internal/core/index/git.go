package indexcore

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
)

// gitPins are the command-scope values every index git call carries. They
// outrank the repository's own config, so a color, signature, rename, or
// pathspec setting cannot change what is parsed.
var gitPins = []string{
	"--literal-pathspecs",
	"-c", "core.quotepath=false",
	"-c", "color.ui=false",
	"-c", "diff.renames=false",
	"-c", "log.showSignature=false",
}

// gitPinnedEnv is the environment every index git call runs with. The config
// files are nulled, not merely overridden; the user's safe.directory list is
// carried across at command scope by trustArgs.
var gitPinnedEnv = map[string]string{
	"GIT_CONFIG_NOSYSTEM":    "1",
	"GIT_CONFIG_GLOBAL":      os.DevNull,
	"GIT_PAGER":              "cat",
	"GIT_OPTIONAL_LOCKS":     "0",
	"GIT_TERMINAL_PROMPT":    "0",
	"GIT_NO_REPLACE_OBJECTS": "1",
	"LC_ALL":                 "C",
}

// gitInheritedRedirects names git's location escape hatches: a snapshot must
// never pass them through to a child.
var gitInheritedRedirects = []string{
	"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY",
	"GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_COMMON_DIR", "GIT_NAMESPACE",
	"GIT_PREFIX", "GIT_EXTERNAL_DIFF", "GIT_DIFF_OPTS",
}

// trustScopes are git's protected config scopes: safe.directory is honored
// from these and from the command line, never from the repository itself.
var trustScopes = []string{"--system", "--global"}

// Parsing and rendering constants shared by the git readers.
const (
	batchHeaderFields = 3
	lsTreeFields      = 4
	lsTreeSizeIndex   = 3
	decimalBase       = 10
	trustOutputCap    = 64 << 10
	shortSHA          = 12
	newline           = "\n"
)

// gitOutputCap bounds every index git capture. The tree cap plus one shard
// file covers the batch stream; ls-tree and rev-parse are far smaller.
const gitOutputCap = 97 << 20

// shaRe matches a full git object id (SHA-1 or SHA-256).
var shaRe = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)

// commander returns the injected command runner.
func (st *state) commander() core.Commander {
	if st.req.Runtime == nil {
		return nil
	}
	return st.req.Commander
}

// snapshot returns the injected environment snapshot.
func (st *state) snapshot() map[string]string {
	if st.req.Runtime == nil {
		return nil
	}
	return st.req.Env
}

// git runs one git command and reports its streams and wait code.
func (st *state) git(ctx context.Context, dir string, args ...string) (string, int, string, error) {
	return st.gitCapped(ctx, gitOutputCap, dir, args...)
}

// gitCapped runs one git command with an explicit capture cap.
func (st *state) gitCapped(
	ctx context.Context, limit int, dir string, args ...string,
) (string, int, string, error) {
	cmdr := st.commander()
	if cmdr == nil {
		return "", 0, "", &core.RefusalError{Code: codeIndexFailed, Message: "index needs a command runner"}
	}
	argv := slices.Concat([]string{gitBin, "-C", dir}, st.trustArgs(), gitPins, args)
	res, err := cmdr.Run(ctx, &core.Command{Argv: argv, Env: st.gitEnv(false), OutputCap: limit})
	if err != nil {
		if _, ok := errors.AsType[*core.MissingToolError](err); ok {
			return "", 0, "", &core.RefusalError{Code: "missing-tool", Message: "git is not on PATH"}
		}
		if _, ok := errors.AsType[*core.OutputCappedError](err); ok {
			return "", 0, "", &core.RefusalError{
				Code: codeIndexCapped, Message: fmt.Sprintf("git output exceeded the %d-byte capture cap", limit),
			}
		}
		return "", 0, "", &core.RefusalError{Code: codeIndexFailed, Message: "git " + args[0] + " failed: " + err.Error()}
	}
	return res.Stdout, res.ExitCode, res.Stderr, nil
}

// asRefusal returns err's typed refusal, sealing an unexpected error so every
// git failure crosses the service boundary as one type.
func asRefusal(err error) *core.RefusalError {
	if ref, ok := errors.AsType[*core.RefusalError](err); ok {
		return ref
	}
	return &core.RefusalError{Code: codeIndexFailed, Message: err.Error()}
}

// gitBatch runs one `git cat-file --batch` over blobs and returns the bytes
// keyed by object id. The stream is parsed one record at a time.
func (st *state) gitBatch(ctx context.Context, dir string, blobs []string) (map[string][]byte, *core.RefusalError) {
	if len(blobs) == 0 {
		return map[string][]byte{}, nil
	}
	cmdr := st.commander()
	if cmdr == nil {
		return nil, &core.RefusalError{Code: codeIndexFailed, Message: "index needs a command runner"}
	}
	argv := slices.Concat([]string{gitBin, "-C", dir}, st.trustArgs(), gitPins, []string{"cat-file", "--batch"})
	stdin := strings.NewReader(strings.Join(blobs, newline) + newline)
	res, err := cmdr.Run(ctx, &core.Command{Argv: argv, Env: st.gitEnv(false), OutputCap: gitOutputCap, Stdin: stdin})
	if err != nil {
		return nil, batchRefusal(err)
	}
	if res.ExitCode != 0 {
		return nil, &core.RefusalError{Code: codeIndexFailed, Message: "git cat-file failed: " + firstLine(res.Stderr)}
	}
	objects, ref := parseBatch(res.Stdout)
	if ref != nil {
		return nil, ref
	}
	return objects, nil
}

// batchRefusal seals one cat-file run error: a missing git binary, an
// over-cap capture, or a plain failure.
func batchRefusal(err error) *core.RefusalError {
	if _, ok := errors.AsType[*core.MissingToolError](err); ok {
		return &core.RefusalError{Code: "missing-tool", Message: "git is not on PATH"}
	}
	if _, ok := errors.AsType[*core.OutputCappedError](err); ok {
		return &core.RefusalError{
			Code:    codeIndexCapped,
			Message: fmt.Sprintf("git cat-file output exceeded the %d-byte capture cap", gitOutputCap),
		}
	}
	return &core.RefusalError{Code: codeIndexFailed, Message: "git cat-file failed: " + err.Error()}
}

// parseBatch reads a cat-file --batch stream: a header line followed by the
// named blob's exact bytes and one newline.
//
//nolint:revive // the git stream and environment matrix reads as one block
func parseBatch(stream string) (map[string][]byte, *core.RefusalError) {
	objects := make(map[string][]byte)
	rest := stream
	for rest != "" {
		header, after, ok := strings.Cut(rest, newline)
		if !ok {
			return nil, &core.RefusalError{Code: codeIndexCorrupt, Message: "git cat-file returned a truncated header"}
		}
		fields := strings.Fields(header)
		if len(fields) < batchHeaderFields || fields[1] != "blob" {
			return nil, &core.RefusalError{Code: codeIndexCorrupt, Message: "git cat-file returned an unexpected record"}
		}
		size, ok := atoi(fields[2])
		if !ok || size < 0 || size > len(after) {
			return nil, &core.RefusalError{Code: codeIndexCorrupt, Message: "git cat-file returned a truncated blob"}
		}
		objects[fields[0]] = []byte(after[:size])
		if size == len(after) {
			rest = ""
			continue
		}
		if after[size] != newline[0] {
			return nil, &core.RefusalError{Code: codeIndexCorrupt, Message: "git cat-file returned a malformed blob"}
		}
		rest = after[size+1:]
	}
	return objects, nil
}

// atoi reads a non-negative decimal integer.
func atoi(s string) (int, bool) {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*decimalBase + int(r-'0')
		if n < 0 {
			return 0, false
		}
	}
	return n, true
}

// gitEnv builds the child environment from the injected snapshot: every
// variable except git's redirects and config overrides, then the pins.
//
//nolint:revive // the git stream and environment matrix reads as one block
func (st *state) gitEnv(userConfig bool) []string {
	env := make(map[string]string)
	for name, value := range st.snapshot() {
		if slices.Contains(gitInheritedRedirects, name) {
			continue
		}
		if !userConfig && strings.HasPrefix(name, "GIT_CONFIG") {
			continue
		}
		env[name] = value
	}
	for name, value := range gitPinnedEnv {
		if userConfig && strings.HasPrefix(name, "GIT_CONFIG") {
			continue
		}
		env[name] = value
	}
	out := make([]string, 0, len(env))
	for _, name := range slices.Sorted(maps.Keys(env)) {
		out = append(out, name+"="+env[name])
	}
	return out
}

// readTrust reads the user's safe.directory list with the user's own config
// scopes. The scrub that keeps index git hermetic also drops the ownership
// decisions the user made for checkouts they do not own.
func (st *state) readTrust(ctx context.Context, dir string) []string {
	cmdr := st.commander()
	if cmdr == nil {
		return nil
	}
	var dirs []string
	for _, scope := range trustScopes {
		argv := []string{"git", "-C", dir, "config", scope, "--null", "--get-all", "safe.directory"}
		res, err := cmdr.Run(ctx, &core.Command{Argv: argv, Env: st.gitEnv(true), OutputCap: trustOutputCap})
		if err != nil || res.ExitCode != 0 || res.Stdout == "" {
			continue
		}
		dirs = append(dirs, strings.Split(strings.TrimSuffix(res.Stdout, "\x00"), "\x00")...)
	}
	return dirs
}

// trustArgs is the command-scope prefix that forwards the trust list.
func (st *state) trustArgs() []string {
	args := make([]string, 0, 2*len(st.trust))
	for _, dir := range st.trust {
		args = append(args, "-c", "safe.directory="+dir)
	}
	return args
}

// resolveTree resolves one revision to a tree object id.
func (st *state) resolveTree(ctx context.Context, dir, ref string) (string, *core.RefusalError) {
	return st.revParse(ctx, dir, ref+"^{tree}", "tree")
}

// resolveCommit resolves one revision to a commit object id.
func (st *state) resolveCommit(ctx context.Context, dir, ref string) (string, *core.RefusalError) {
	return st.revParse(ctx, dir, ref+"^{commit}", "commit")
}

// revParse runs one rev-parse and validates the object id.
func (st *state) revParse(ctx context.Context, dir, spec, kind string) (string, *core.RefusalError) {
	out, exit, stderr, err := st.git(ctx, dir, "rev-parse", "--verify", "--end-of-options", spec)
	if err != nil {
		return "", asRefusal(err)
	}
	if exit != 0 {
		return "", &core.RefusalError{Code: "bad-at", Message: "cannot resolve " + spec + ": " + firstLine(stderr)}
	}
	sha := strings.TrimSpace(firstLine(out))
	if !shaRe.MatchString(sha) {
		return "", &core.RefusalError{Code: "bad-at", Message: "cannot resolve " + spec + " to a " + kind}
	}
	return sha, nil
}

// firstLine returns s's first line.
func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), newline)
	return line
}

// short renders a commit id's leading 12 bytes for human lines.
func short(sha string) string {
	if len(sha) <= shortSHA {
		return sha
	}
	return sha[:shortSHA]
}

// treeEntry is one regular file in a tree.
type treeEntry struct {
	Path string
	Blob string
	Mode string
	Size int64
}

// lsTree lists one tree's regular files.
func (st *state) lsTree(ctx context.Context, dir, tree string) ([]treeEntry, *core.RefusalError) {
	out, exit, stderr, err := st.git(ctx, dir, "ls-tree", "-r", "-l", "-z", tree)
	if err != nil {
		return nil, asRefusal(err)
	}
	if exit != 0 {
		return nil, &core.RefusalError{Code: codeIndexFailed, Message: "git ls-tree failed: " + firstLine(stderr)}
	}
	return parseLsTree(out)
}

// parseLsTree parses `ls-tree -r -l -z` records.
//
//nolint:revive // the git stream and environment matrix reads as one block
func parseLsTree(out string) ([]treeEntry, *core.RefusalError) {
	var entries []treeEntry
	for record := range strings.SplitSeq(out, "\x00") {
		if record == "" {
			continue
		}
		meta, path, ok := strings.Cut(record, "\t")
		if !ok {
			return nil, &core.RefusalError{Code: codeIndexFailed, Message: "git ls-tree returned a malformed record"}
		}
		fields := strings.Fields(meta)
		if len(fields) != lsTreeFields || fields[1] != "blob" {
			continue
		}
		size, ok := atoi(fields[lsTreeSizeIndex])
		if !ok {
			continue
		}
		entries = append(entries, treeEntry{Mode: fields[0], Blob: fields[2], Size: int64(size), Path: path})
	}
	return entries, nil
}

// readModule reads the module path from the tree's go.mod, if it has one.
//
//nolint:revive // the git stream and environment matrix reads as one block
func (st *state) readModule(ctx context.Context, dir string, entries []treeEntry) string {
	for _, e := range entries {
		if e.Path != "go.mod" {
			continue
		}
		out, exit, _, err := st.git(ctx, dir, "cat-file", "blob", e.Blob)
		if err != nil || exit != 0 {
			return ""
		}
		for line := range strings.SplitSeq(out, newline) {
			rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module ")
			if ok {
				return strings.TrimSpace(rest)
			}
		}
	}
	return ""
}
