// Package gitcmd builds every git command verdict runs, so none of them runs
// a program the reviewed content chose.
//
// A checkout under review is untrusted: its tree can set `core.hooksPath` to
// an in-tree directory (husky, lefthook), and its `.gitattributes` can route
// files through any clean, smudge, or process filter, textconv, or external
// diff driver the operator configured — git-lfs among them, which a PR's
// `.lfsconfig` can point at a URL of its choosing. A variable a parent git
// exported (inside a hook, or under `git -c`) can retarget a call at another
// repository or index, or turn hooks back on through GIT_CONFIG_PARAMETERS.
//
// [Argv] and [Env] close all of that for every call: the repository is named
// with -C, no hook and no fsmonitor program runs, a diff runs no external
// diff or textconv driver, and the inherited location variables and
// GIT_CONFIG_PARAMETERS are dropped. Clean filters still run where git needs
// them to read a working tree as git itself sees it (status, a working-tree
// diff, add); a call that writes a working tree passes [FilterOptions] so no
// smudge or process filter runs either.
//
// The package is a leaf: it imports no other repository package, so the
// contract root and every service can build their git calls here.
package gitcmd

import (
	"os"
	"slices"
	"strconv"
	"strings"
)

// bin is the git executable every verdict-internal call spawns.
const bin = "git"

// The argv pieces Argv writes and Split reads back.
const (
	optRepo    = "-C"
	optConfig  = "-c"
	setTo      = "="
	noExtDiff  = "--no-ext-diff"
	noTextconv = "--no-textconv"
	// rootPrefix is `git -C <root>`, the argv's fixed head.
	rootPrefix = 3
)

// safety is the configuration every call runs with, as key/value pairs: no
// hook resolves (the hooks path names a file, so a lookup under it finds
// nothing, even where the checkout sets core.hooksPath to its own .husky)
// and no fsmonitor program runs.
var safety = []string{
	"core.hooksPath", os.DevNull,
	"core.fsmonitor", "false",
}

// diffSafety are the flags a subcommand that renders file content takes, so
// no external diff or textconv driver the checkout's attributes select runs.
var diffSafety = map[string][]string{
	"diff":  {noExtDiff, noTextconv},
	"log":   {noExtDiff, noTextconv},
	"show":  {noExtDiff, noTextconv},
	"blame": {noTextconv},
}

// inherited are the variables a parent git exports that would retarget or
// reconfigure a call: another repository, work tree, index, or object store
// (an inherited GIT_INDEX_FILE once let a read-tree overwrite the user's own
// index), and GIT_CONFIG_PARAMETERS, which outranks GIT_CONFIG_COUNT and
// which git exports to the hooks it runs under `git -c`. Every call names its
// repository with -C, so none of them is needed.
var inherited = []string{
	"GIT_INDEX_FILE", "GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_OBJECT_DIRECTORY",
	"GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_NAMESPACE", "GIT_PREFIX", "GIT_CONFIG_PARAMETERS",
}

// Argv returns `git -C <root> <options> -c core.hooksPath=… -c
// core.fsmonitor=false <subcommand> [diff flags] <rest>` for args, which may
// open with global options of the caller's own (`-c key=value`,
// `--literal-pathspecs`) before the subcommand. The safety settings follow
// the caller's options, so the last -c wins for them; a subcommand that
// renders content gets --no-ext-diff and --no-textconv (blame, which takes
// no external diff, gets --no-textconv). An empty root names the current
// directory.
func Argv(root string, args ...string) []string {
	if root == "" {
		root = "."
	}
	at := subcommandIndex(args)
	argv := slices.Concat([]string{bin, optRepo, root}, args[:at], safetyOptions())
	if at < len(args) {
		sub := args[at]
		argv = slices.Concat(argv, []string{sub}, diffSafety[sub], args[at+1:])
	}
	return argv
}

// Env returns base — the process environment when base is nil — without the
// inherited location variables and GIT_CONFIG_PARAMETERS, with the safety
// settings appended through GIT_CONFIG_COUNT after any entries base already
// carries. [Argv] sets them again as -c options, which outrank the
// environment; both are set so neither alone is load-bearing.
func Env(base []string) []string {
	if base == nil {
		base = os.Environ()
	}
	env := slices.DeleteFunc(slices.Clone(base), func(e string) bool {
		name, _, _ := strings.Cut(e, setTo)
		return slices.Contains(inherited, name)
	})
	return WithConfig(env, safety...)
}

// WithConfig appends key/value pairs to env as command-scope configuration
// through GIT_CONFIG_COUNT, after the entries env already carries.
func WithConfig(env []string, kv ...string) []string {
	count := configCount(env)
	for i := 0; i+1 < len(kv); i += 2 {
		n := strconv.Itoa(count + i/2)
		env = append(env, "GIT_CONFIG_KEY_"+n+setTo+kv[i], "GIT_CONFIG_VALUE_"+n+setTo+kv[i+1])
	}
	return append(env, "GIT_CONFIG_COUNT="+strconv.Itoa(count+len(kv)/2))
}

// configCount is the GIT_CONFIG_COUNT env already sets (the last one wins).
func configCount(env []string) int {
	count := 0
	for _, e := range env {
		v, ok := strings.CutPrefix(e, "GIT_CONFIG_COUNT=")
		if n, err := strconv.Atoi(v); ok && err == nil && n > 0 {
			count = n
		}
	}
	return count
}

// ListFilters are the args that list the configured filter drivers' keys,
// NUL-terminated, for [FilterOptions]. git exits 1 when there are none.
var ListFilters = []string{"config", "-z", "--name-only", "--get-regexp", `^filter\.`}

// FilterOptions turns a [ListFilters] listing into the global options that
// switch every listed driver off for one call: an empty clean, smudge, and
// process command is no filter, and required=false lets a file whose driver
// is off pass through as its blob bytes. A call that writes a working tree
// from reviewed content passes them, so a checkout runs no filter program —
// git-lfs included — that the content's attributes select. A driver is only
// ever named by configuration, which the reviewed tree cannot write, so the
// listing names every driver an attribute could reach.
func FilterOptions(listing string) []string {
	var drivers []string
	for key := range strings.SplitSeq(listing, "\x00") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(key), "filter.")
		dot := strings.LastIndexByte(rest, '.')
		if !ok || dot <= 0 || slices.Contains(drivers, rest[:dot]) {
			continue
		}
		drivers = append(drivers, rest[:dot])
	}
	var opts []string
	for _, d := range drivers {
		key := "filter." + d + "."
		opts = append(opts,
			optConfig, key+"clean"+setTo, optConfig, key+"smudge"+setTo, optConfig, key+"process"+setTo,
			optConfig, key+"required"+setTo+"false")
	}
	return opts
}

// Split undoes [Argv]: it returns the root and the caller's own args, without
// the settings and flags Argv added. ok is false for an argv Argv did not
// build, so a test seam that answers on the caller's args can also require
// that every git call it sees carries the safety settings.
func Split(argv []string) (root string, args []string, ok bool) {
	if len(argv) < rootPrefix || argv[0] != bin || argv[1] != optRepo {
		return "", nil, false
	}
	root, rest := argv[rootPrefix-1], argv[rootPrefix:]
	opts := safetyOptions()
	at := subcommandIndex(rest)
	if at < len(opts) || !slices.Equal(rest[at-len(opts):at], opts) {
		return "", nil, false
	}
	args = slices.Concat(rest[:at-len(opts)], rest[at:])
	if at == len(rest) {
		return root, args, true
	}
	sub := at - len(opts)
	flags := diffSafety[args[sub]]
	tail := args[sub+1:]
	if len(tail) < len(flags) || !slices.Equal(tail[:len(flags)], flags) {
		return "", nil, false
	}
	return root, slices.Concat(args[:sub+1], tail[len(flags):]), true
}

// Subcommand names the subcommand an [Argv] argv runs, for error text.
func Subcommand(argv []string) string {
	if _, args, ok := Split(argv); ok {
		if at := subcommandIndex(args); at < len(args) {
			return args[at]
		}
	}
	return bin
}

// safetyOptions renders safety as -c options.
func safetyOptions() []string {
	opts := make([]string, 0, len(safety))
	for i := 0; i+1 < len(safety); i += 2 {
		opts = append(opts, optConfig, safety[i]+setTo+safety[i+1])
	}
	return opts
}

// subcommandIndex is where the subcommand sits in args: after the global
// options, of which -c and -C take a value. git runs --version as the
// version command, so it counts as one. len(args) when there is none.
func subcommandIndex(args []string) int {
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == optConfig || args[i] == optRepo:
			i++
		case args[i] == "--version" || !strings.HasPrefix(args[i], "-"):
			return i
		}
	}
	return len(args)
}
