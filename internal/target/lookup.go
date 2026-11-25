package target

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/stainless-code/verdict/internal/core"
)

const (
	lookupTimeout = 20 * time.Second
	lookupCap     = 64 << 10
	// lookupLimit is how many same-named branches one page may hold. Forks
	// share branch names, so a stranger's `main` can sit beside the real PR; a
	// page that fills is not trusted to be the whole list.
	lookupLimit = "100"
	lookupMax   = 100
)

// lookupFields names, per host CLI, the JSON keys of one listed pull request:
// its number, its head branch, and its web URL.
type lookupFields struct{ number, head, url string }

var (
	githubFields = lookupFields{"number", "headRefName", "url"}
	gitlabFields = lookupFields{"iid", "source_branch", "web_url"}
)

const (
	providerGitLab = "gitlab"
	providerGitHub = "github"
)

const githubJSON = "number,headRefName,url,isCrossRepository,headRepositoryOwner,headRepository"

// branchLookupArgv is the host CLI call that lists the open pull requests
// whose head branch is named branch (in any repository: the answer is
// filtered to this one afterwards). The repository is qualified with the
// origin's host, so a machine whose default host differs still asks the
// origin's. The branch rides in the same argument as its flag, so a name can
// never be read as a flag of its own. It is nil for a host with no lookup;
// core.BranchLookupProviders is the table a test pins it to.
func branchLookupArgv(provider, host, repo, branch string) []string {
	switch provider {
	case providerGitHub:
		return []string{
			"gh", "pr", "list", "--repo", host + "/" + repo, "--head=" + branch, "--state", "open",
			"--json", githubJSON, "--limit", lookupLimit,
		}
	case providerGitLab:
		return []string{
			"glab", "mr", "list", "-R", "https://" + host + "/" + repo, "--source-branch=" + branch,
			"-F", "json", "--per-page", lookupLimit,
		}
	default:
		return nil
	}
}

func lookupFieldsFor(provider string) lookupFields {
	if provider == providerGitLab {
		return gitlabFields
	}
	return githubFields
}

// hostPr asks the origin's host which open pull request has branch as its
// head. Exactly one validated match settles the target; none, several, a host
// error, and an answer that fails validation each refuse naming the args.
func hostPr(ctx context.Context, d Deps, q *Request, f *found, branch, hint string) (string, *core.RefusalError) {
	argv := branchLookupArgv(f.provider, f.host, f.repo, branch)
	if argv == nil {
		return "", noHostMatch(f, branch, hint, "has no branch lookup")
	}
	ctx, cancel := context.WithTimeout(ctx, lookupTimeout)
	defer cancel()
	// The argv already names the origin's host; pinning it too has the runner
	// verify that no spelling of the call can reach another one.
	res, err := d.Commander.Run(core.WithHost(ctx, f.host), &core.Command{Argv: argv, OutputCap: lookupCap})
	if err != nil || res.ExitCode != 0 || res.StdoutTruncated || ctx.Err() != nil {
		return "", noHostMatch(f, branch, hint, fmt.Sprintf("could not be asked (%s failed)", argv[0]))
	}
	nums, ok := validatedNumbers(res.Stdout, f, branch, q.Env)
	switch {
	case !ok:
		return "", noHostMatch(f, branch, hint, "answered with something verdict does not trust")
	case len(nums) == 0:
		return "", noHostMatch(f, branch, hint, "has no open pull request for it")
	case len(nums) > 1:
		return "", noHostMatch(f, branch, hint, "lists more than one open pull request for it ("+strings.Join(nums, ", ")+")")
	}
	return nums[0], nil
}

func noHostMatch(f *found, branch, hint, why string) *core.RefusalError {
	return core.TargetUnresolved("no bundle under .verdict/state records branch %q for %s, and %s %s; pass the target: %s",
		branch, f.repo, f.host, why, hint)
}

// validatedNumbers parses the CLI's JSON list and returns the number of every
// entry whose head lives in this repository. Each entry first passes the same
// strict checks a pasted URL would (a decimal number in range, the asked-for
// head branch, and a web URL that parses as this provider, host, repository
// and number); one entry failing any of them rejects the whole answer. An
// entry from a fork is well-formed but is someone else's pull request that
// merely shares the branch name, so it is left out rather than refused.
func validatedNumbers(stdout string, f *found, branch string, env map[string]string) ([]string, bool) {
	dec := json.NewDecoder(bytes.NewReader([]byte(stdout)))
	dec.UseNumber()
	var entries []map[string]any
	if err := dec.Decode(&entries); err != nil || len(entries) >= lookupMax || !atEOF(dec) {
		return nil, false
	}
	keys := lookupFieldsFor(f.provider)
	var nums []string
	for _, e := range entries {
		num, same, ok := checkEntry(e, &keys, f, branch, env)
		if !ok {
			return nil, false
		}
		if same {
			nums = append(nums, num)
		}
	}
	slices.Sort(nums)
	return nums, true
}

// checkEntry validates one listed pull request and reports whether its head
// lives in the origin's own repository.
func checkEntry(e map[string]any, keys *lookupFields, f *found, branch string, env map[string]string) (num string, same, ok bool) {
	num, ok = validEntry(e, keys, f, branch, env)
	if !ok {
		return "", false, false
	}
	same, ok = sameRepoHead(e, f)
	return num, same, ok
}

func validEntry(e map[string]any, keys *lookupFields, f *found, branch string, env map[string]string) (string, bool) {
	n, isNum := e[keys.number].(json.Number)
	head, isHead := e[keys.head].(string)
	raw, isURL := e[keys.url].(string)
	if !isNum || !isHead || !isURL || head != branch || !core.ValidPrNumber(n.String()) {
		return "", false
	}
	t, ref := core.ParseTargetURL(raw, env)
	if ref != nil || t.Provider != f.provider || !strings.EqualFold(t.Repo, f.repo) || t.Pr != n.String() {
		return "", false
	}
	if u, err := url.Parse(raw); err != nil || core.URLAuthority(u) != f.host {
		return "", false
	}
	return n.String(), true
}

// sameRepoHead reports whether the entry's head branch lives in the origin's
// own repository rather than a fork; ok is false when the entry does not say.
// GitHub names the head repository outright. GitLab's list is scoped to the
// target project, so a merge request is same-project when its source project
// is that target project.
func sameRepoHead(e map[string]any, f *found) (same, ok bool) {
	if f.provider == providerGitLab {
		src, srcOK := e["source_project_id"].(json.Number)
		dst, dstOK := e["target_project_id"].(json.Number)
		return srcOK && dstOK && src == dst, srcOK && dstOK
	}
	cross, isBool := e["isCrossRepository"].(bool)
	if !isBool {
		return false, false
	}
	if cross {
		return false, true
	}
	login := nested(e, "headRepositoryOwner", "login")
	name := nested(e, "headRepository", "name")
	return login != "" && name != "" && strings.EqualFold(login+"/"+name, f.repo), login != "" && name != ""
}

// nested reads one string out of an object-valued field, "" when absent or of
// another type.
func nested(e map[string]any, field, key string) string {
	if obj, ok := e[field].(map[string]any); ok {
		if v, ok := obj[key].(string); ok {
			return v
		}
	}
	return ""
}

// atEOF reports that nothing but whitespace follows the decoded value: More
// alone passes a stray closing delimiter, so the next token must be the end.
func atEOF(dec *json.Decoder) bool {
	_, err := dec.Token()
	return errors.Is(err, io.EOF)
}
