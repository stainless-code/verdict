package backtest

import (
	"context"
	"errors"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// errGitMissing is the package-level missing-tool message; returning it
// directly keeps the boundary sealed.
var errGitMissing = errors.New("git is not on PATH")

// errGitOutputCap names a stream that hit the capture cap.
var errGitOutputCap = errors.New("output exceeded the capture cap")

// git runs one git command and treats a capped stream as an error; callers
// that can reason about truncation use [state.gitCapped] directly.
func (st *state) git(ctx context.Context, dir string, args ...string) (string, int, string, error) {
	out, exit, stderr, truncated, err := st.gitCapped(ctx, commandOutputCap, dir, args...)
	if err != nil {
		return "", 0, "", err
	}
	if truncated {
		return "", 0, "", jq.Sealed("git "+args[0], errGitOutputCap)
	}
	return out, exit, stderr, nil
}

// gitPins are the command-scope values every mining git call carries. The
// config pins outrank the repository's own config, so a `color.diff=always`
// or `log.showSignature=true` in .git/config cannot change what is parsed.
// Pathspecs stay literal so a deleted path such as `app/[id].tsx` or a name
// starting with `:` reaches blame and grep as the file name git stored, not
// as a glob or pathspec magic.
var gitPins = []string{
	"--literal-pathspecs",
	"-c", "core.quotepath=false",
	"-c", "color.ui=false",
	"-c", "log.showSignature=false",
}

// diffPins are the diff-family flags: they fix the prefixes, the algorithm,
// and the external-tool and textconv hooks that would reshape `-U0` output.
var diffPins = []string{
	"--no-color", "--no-ext-diff", "--no-textconv",
	"--src-prefix=a/", "--dst-prefix=b/", "--diff-algorithm=myers",
}

// gitPinnedEnv is the environment every mining git call runs with. The
// config files are nulled, not merely overridden, because the user's global
// config is a machine fact the artifact must not depend on; the user's
// safe.directory list is the one value carried across, at command scope. The
// locale is C so stderr matching is stable across hosts.
var gitPinnedEnv = map[string]string{
	"GIT_CONFIG_NOSYSTEM":    "1",
	"GIT_CONFIG_GLOBAL":      os.DevNull,
	"GIT_PAGER":              "cat",
	"GIT_OPTIONAL_LOCKS":     "0",
	"GIT_TERMINAL_PROMPT":    "0",
	"GIT_NO_REPLACE_OBJECTS": "1",
	"LC_ALL":                 "C",
}

// gitInheritedRedirects names git's location and behavior escape hatches that
// a snapshot must never pass through: each one retargets the repository,
// index, object store, or diff driver the miner reads.
var gitInheritedRedirects = []string{
	"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY",
	"GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_COMMON_DIR", "GIT_NAMESPACE",
	"GIT_PREFIX", "GIT_EXTERNAL_DIFF", "GIT_DIFF_OPTS",
}

// diffArgs prefixes a diff-family subcommand with the pinned diff flags.
func diffArgs(sub string, args ...string) []string {
	return slices.Concat([]string{sub}, diffPins, args)
}

// trustScopes are git's protected config scopes: safe.directory is honored
// from these and from the command line, never from the repository's own
// config, so a checkout cannot vouch for itself.
var trustScopes = []string{"--system", "--global"}

// readTrust reads the user's safe.directory list with the user's own git
// environment. The scrub that keeps mining hermetic also drops the ownership
// decisions the user made for checkouts they do not own, so those decisions
// are forwarded at command scope instead of replaced with a blanket trust.
// A missing tool or an unreadable scope reads as no trust: the run then fails
// at the checkout it cannot read, carrying git's own line.
func (st *state) readTrust(ctx context.Context) []string {
	var dirs []string
	for _, scope := range trustScopes {
		out, exit, err := st.gitUser(ctx, "config", scope, "--null", "--get-all", "safe.directory")
		if err != nil || exit != 0 {
			continue
		}
		dirs = append(dirs, splitTrust(out)...)
	}
	return dirs
}

// gitUser runs one config read with the user's normal environment, in argv
// form and with the pager and locale pins that cannot change which scopes it
// reads.
func (st *state) gitUser(ctx context.Context, args ...string) (string, int, error) {
	cmd := &core.Command{
		Argv:      slices.Concat([]string{"git"}, args),
		Env:       gitUserEnv(st.snapshot()),
		OutputCap: commandOutputCap,
	}
	res, err := st.commander().Run(ctx, cmd)
	if err != nil {
		return "", 0, jq.Sealed("git config", err)
	}
	return res.Stdout, res.ExitCode, nil
}

// splitTrust separates one NUL-terminated --get-all capture into its values.
// NUL termination keeps a value that contains a newline whole; an empty value
// is git's reset of the list and stays where the user put it.
func splitTrust(out string) []string {
	if out == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(out, "\x00"), "\x00")
}

// trustArgs is the command-scope prefix every mining call carries. git honors
// safe.directory there, so the user's list survives the config scrub with its
// order and its empty-value resets intact.
func (st *state) trustArgs() []string {
	args := make([]string, 0, 2*len(st.trust))
	for _, dir := range st.trust {
		args = append(args, "-c", "safe.directory="+dir)
	}
	return args
}

// gitEnv builds the child environment from the injected snapshot: every
// variable except git's redirects and config overrides, then the pins.
func gitEnv(snapshot map[string]string) []string {
	return buildGitEnv(snapshot, false)
}

// gitUserEnv builds the environment for the safe.directory read: the user's
// config scopes and overlays stay live, because the scrub that makes mining
// hermetic is exactly what hides the user's ownership decisions.
func gitUserEnv(snapshot map[string]string) []string {
	return buildGitEnv(snapshot, true)
}

// buildGitEnv merges the snapshot with the pins, dropping git's redirects
// always and the config overrides unless the call reads the user's config.
func buildGitEnv(snapshot map[string]string, userConfig bool) []string {
	env := mergeGitEnv(snapshotEnv(snapshot, userConfig), userConfig)
	out := make([]string, 0, len(env))
	for _, name := range slices.Sorted(maps.Keys(env)) {
		out = append(out, name+"="+env[name])
	}
	return out
}

// snapshotEnv copies the snapshot variables a child keeps.
func snapshotEnv(snapshot map[string]string, userConfig bool) map[string]string {
	env := make(map[string]string, len(snapshot)+len(gitPinnedEnv))
	for name, value := range snapshot {
		if gitSnapDropped(name, userConfig) {
			continue
		}
		env[name] = value
	}
	return env
}

// mergeGitEnv overlays the pins, withholding the config nulls from a read of
// the user's own config.
func mergeGitEnv(env map[string]string, userConfig bool) map[string]string {
	for name, value := range gitPinnedEnv {
		if userConfig && strings.HasPrefix(name, "GIT_CONFIG") {
			continue
		}
		env[name] = value
	}
	return env
}

// gitSnapDropped reports whether one snapshot variable is withheld from a
// child: git's redirects always, its config overrides only when the child is
// scrubbed.
func gitSnapDropped(name string, userConfig bool) bool {
	if slices.Contains(gitInheritedRedirects, name) {
		return true
	}
	return !userConfig && strings.HasPrefix(name, "GIT_CONFIG")
}

// gitCapped runs one git command through the injected commander, reporting a
// stream that hit its cap instead of failing on it.
func (st *state) gitCapped(
	ctx context.Context, limit int, dir string, args ...string,
) (string, int, string, bool, error) {
	argv := slices.Concat([]string{"git", "-C", dir}, st.trustArgs(), gitPins, args)
	cmd := &core.Command{Argv: argv, Env: gitEnv(st.snapshot()), OutputCap: limit}
	res, err := st.commander().Run(ctx, cmd)
	if err != nil {
		if _, ok := errors.AsType[*core.MissingToolError](err); ok {
			return "", 0, "", false, errGitMissing //nolint:wrapcheck // sealed local sentinel
		}
		if _, ok := errors.AsType[*core.OutputCappedError](err); ok {
			return res.Stdout, res.ExitCode, res.Stderr, true, nil
		}
		return "", 0, "", false, jq.Sealed("git "+args[0], err)
	}
	return res.Stdout, res.ExitCode, res.Stderr, res.StdoutTruncated, nil
}

// snapshot is the injected environment the child environment is built from.
// A direct caller that injects no runtime gets an empty snapshot, so the pins
// alone decide the child's environment.
func (st *state) snapshot() map[string]string {
	if st.req.Runtime == nil {
		return nil
	}
	return st.req.Runtime.Env
}

// revParse resolves one revision to a full commit id.
func (st *state) revParse(ctx context.Context, spec string) (string, bool) {
	out, exit, _, err := st.git(ctx, st.root, "rev-parse", "--verify", "--end-of-options", spec+"^{commit}")
	if err != nil || exit != 0 {
		return "", false
	}
	sha := strings.TrimSpace(firstLine(out))
	if !hexRe.MatchString(sha) {
		return "", false
	}
	return sha, true
}

// isAncestor reports whether a is in b's history. A git failure is a typed
// refusal, never read as "no".
func (st *state) isAncestor(ctx context.Context, a, b string) (bool, *core.RefusalError) {
	_, exit, stderr, err := st.git(ctx, st.root, "merge-base", "--is-ancestor", a, b)
	if err != nil {
		return false, st.gitFailure(ctx, err.Error())
	}
	switch exit {
	case 0:
		return true, nil
	case 1:
		return false, nil
	default:
		return false, st.gitFailure(ctx, "git merge-base failed: "+firstLine(stderr))
	}
}

// parents returns a commit's parent ids.
func (st *state) parents(ctx context.Context, sha string) ([]string, *core.RefusalError) {
	out, exit, stderr, err := st.git(ctx, st.root, "rev-list", "--parents", "-n", "1", sha)
	if err != nil {
		return nil, st.gitFailure(ctx, err.Error())
	}
	if exit != 0 {
		return nil, st.gitFailure(ctx, "git rev-list failed for "+short(sha)+": "+firstLine(stderr))
	}
	fields := strings.Fields(firstLine(out))
	if len(fields) == 0 {
		return nil, st.refuse(codeBacktestFail, "git rev-list printed no commit for "+short(sha))
	}
	return fields[1:], nil
}

// mergeBase returns the merge base of two commits, or "" when they share no
// ancestor. A git failure is a typed refusal: "exit 1, no common ancestor" is
// an answer the caller reads as no-pr, and a broken object is not.
func (st *state) mergeBase(ctx context.Context, a, b string) (string, *core.RefusalError) {
	out, exit, stderr, err := st.git(ctx, st.root, "merge-base", a, b)
	if err != nil {
		return "", st.gitFailure(ctx, err.Error())
	}
	switch exit {
	case 0:
		return strings.TrimSpace(firstLine(out)), nil
	case 1:
		return "", nil
	default:
		return "", st.gitFailure(ctx, "git merge-base failed: "+firstLine(stderr))
	}
}

// commitDate returns a commit's committer date in UTC to the second.
func (st *state) commitDate(ctx context.Context, sha string) (string, *core.RefusalError) {
	out, exit, stderr, err := st.git(ctx, st.root, diffArgs("show", "-s", "--format=%cI", sha)...)
	if err != nil {
		return "", st.gitFailure(ctx, err.Error())
	}
	if exit != 0 {
		return "", st.gitFailure(ctx, "git show failed for "+short(sha)+": "+firstLine(stderr))
	}
	at, err := time.Parse(time.RFC3339, strings.TrimSpace(firstLine(out)))
	if err != nil {
		return "", st.refuse(codeBacktestFail, "unreadable commit date for "+short(sha))
	}
	return at.UTC().Format(gitDateLayout), nil
}

// commitBefore returns the newest commit at or before cutoff, or "" when none.
func (st *state) commitBefore(ctx context.Context, until, cutoff string) (string, *core.RefusalError) {
	out, exit, stderr, err := st.git(ctx, st.root, "rev-list", "-1", "--before="+cutoff, until)
	if err != nil {
		return "", st.gitFailure(ctx, err.Error())
	}
	if exit != 0 {
		return "", st.gitFailure(ctx, "git rev-list failed: "+firstLine(stderr))
	}
	return strings.TrimSpace(firstLine(out)), nil
}

// rootCommit returns the oldest root commit of until's history, or "".
func (st *state) rootCommit(ctx context.Context, until string) (string, *core.RefusalError) {
	out, exit, stderr, err := st.git(ctx, st.root, "rev-list", "--max-parents=0", "--reverse", until)
	if err != nil {
		return "", st.gitFailure(ctx, err.Error())
	}
	if exit != 0 {
		return "", st.gitFailure(ctx, "git rev-list failed: "+firstLine(stderr))
	}
	return strings.TrimSpace(firstLine(out)), nil
}

// subjectBody returns a commit's subject and body.
func (st *state) subjectBody(ctx context.Context, sha string) (string, string, *core.RefusalError) {
	out, exit, stderr, err := st.git(ctx, st.root, diffArgs("show", "-s", "--format=%s%x00%b", sha)...)
	if err != nil {
		return "", "", st.gitFailure(ctx, err.Error())
	}
	if exit != 0 {
		return "", "", st.gitFailure(ctx, "git show failed for "+short(sha)+": "+firstLine(stderr))
	}
	subject, body, _ := strings.Cut(out, "\x00")
	return strings.TrimSpace(subject), strings.TrimSpace(body), nil
}
