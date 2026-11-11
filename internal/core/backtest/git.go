package backtest

import (
	"context"
	"errors"
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

// gitCapped runs one git command through the injected commander, reporting a
// stream that hit its cap instead of failing on it.
func (st *state) gitCapped(
	ctx context.Context, limit int, dir string, args ...string,
) (string, int, string, bool, error) {
	argv := append([]string{"git", "-C", dir, "-c", "core.quotepath=false"}, args...)
	res, err := st.commander().Run(ctx, &core.Command{Argv: argv, OutputCap: limit})
	if err != nil {
		if _, ok := errors.AsType[*core.MissingToolError](err); ok {
			return "", 0, "", false, errGitMissing //nolint:wrapcheck // sealed local sentinel
		}
		return "", 0, "", false, jq.Sealed("git "+args[0], err)
	}
	return res.Stdout, res.ExitCode, res.Stderr, res.StdoutTruncated, nil
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
		return false, st.refuse(codeBacktestFail, err.Error())
	}
	switch exit {
	case 0:
		return true, nil
	case 1:
		return false, nil
	default:
		return false, st.refuse(codeBacktestFail, "git merge-base failed: "+firstLine(stderr))
	}
}

// parents returns a commit's parent ids.
func (st *state) parents(ctx context.Context, sha string) ([]string, *core.RefusalError) {
	out, exit, stderr, err := st.git(ctx, st.root, "rev-list", "--parents", "-n", "1", sha)
	if err != nil {
		return nil, st.refuse(codeBacktestFail, err.Error())
	}
	if exit != 0 {
		return nil, st.refuse(codeBacktestFail, "git rev-list failed for "+short(sha)+": "+firstLine(stderr))
	}
	fields := strings.Fields(firstLine(out))
	if len(fields) == 0 {
		return nil, st.refuse(codeBacktestFail, "git rev-list printed no commit for "+short(sha))
	}
	return fields[1:], nil
}

// mergeBase returns the merge base of two commits, or "" when none exists.
func (st *state) mergeBase(ctx context.Context, a, b string) string {
	out, exit, _, err := st.git(ctx, st.root, "merge-base", a, b)
	if err != nil || exit != 0 {
		return ""
	}
	return strings.TrimSpace(firstLine(out))
}

// commitDate returns a commit's committer date in UTC to the second.
func (st *state) commitDate(ctx context.Context, sha string) (string, *core.RefusalError) {
	out, exit, stderr, err := st.git(ctx, st.root, "show", "-s", "--format=%cI", sha)
	if err != nil {
		return "", st.refuse(codeBacktestFail, err.Error())
	}
	if exit != 0 {
		return "", st.refuse(codeBacktestFail, "git show failed for "+short(sha)+": "+firstLine(stderr))
	}
	at, err := time.Parse(time.RFC3339, strings.TrimSpace(firstLine(out)))
	if err != nil {
		return "", st.refuse(codeBacktestFail, "unreadable commit date for "+short(sha))
	}
	return at.UTC().Format(gitDateLayout), nil
}

// commitBefore returns the newest commit at or before cutoff, or "" when none.
func (st *state) commitBefore(ctx context.Context, until, cutoff string) string {
	out, exit, _, err := st.git(ctx, st.root, "rev-list", "-1", "--before="+cutoff, until)
	if err != nil || exit != 0 {
		return ""
	}
	return strings.TrimSpace(firstLine(out))
}

// rootCommit returns the oldest root commit of until's history, or "".
func (st *state) rootCommit(ctx context.Context, until string) string {
	out, exit, _, err := st.git(ctx, st.root, "rev-list", "--max-parents=0", "--reverse", until)
	if err != nil || exit != 0 {
		return ""
	}
	return strings.TrimSpace(firstLine(out))
}

// subjectBody returns a commit's subject and body.
func (st *state) subjectBody(ctx context.Context, sha string) (string, string, *core.RefusalError) {
	out, exit, stderr, err := st.git(ctx, st.root, "show", "-s", "--format=%s%x00%b", sha)
	if err != nil {
		return "", "", st.refuse(codeBacktestFail, err.Error())
	}
	if exit != 0 {
		return "", "", st.refuse(codeBacktestFail, "git show failed for "+short(sha)+": "+firstLine(stderr))
	}
	subject, body, _ := strings.Cut(out, "\x00")
	return strings.TrimSpace(subject), strings.TrimSpace(body), nil
}
