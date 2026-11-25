package target

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/stainless-code/verdict/internal/core"
)

const (
	lookupTimeout = 20 * time.Second
	lookupCap     = 64 << 10
	// lookupLimit asks for one more entry than a unique match, which is all
	// "ambiguous" needs to be proven.
	lookupLimit = "2"
)

// lookupFields names, per host CLI, the JSON keys of one listed pull request:
// its number, its head branch, and its web URL.
type lookupFields struct{ number, head, url string }

var (
	githubFields = lookupFields{"number", "headRefName", "url"}
	gitlabFields = lookupFields{"iid", "source_branch", "web_url"}
)

// branchLookupArgv is the host CLI call that lists the open pull requests
// whose head is branch. The branch rides in the same argument as its flag, so
// a name can never be read as a flag of its own. It is nil for a host with no
// lookup; core.BranchLookupProviders is the table a test pins it to.
func branchLookupArgv(provider, repo, branch string) []string {
	switch provider {
	case "github":
		return []string{
			"gh", "pr", "list", "--repo", repo, "--head=" + branch, "--state", "open",
			"--json", "number,headRefName,url", "--limit", lookupLimit,
		}
	case "gitlab":
		return []string{
			"glab", "mr", "list", "-R", repo, "--source-branch=" + branch,
			"-F", "json", "--per-page", lookupLimit,
		}
	default:
		return nil
	}
}

func lookupFieldsFor(provider string) lookupFields {
	if provider == "gitlab" {
		return gitlabFields
	}
	return githubFields
}

// hostPr asks the origin's host which open pull request has branch as its
// head. Exactly one validated match settles the target; none, several, a host
// error, and an answer that fails validation each refuse naming the args.
func hostPr(ctx context.Context, d Deps, q *Request, f *found, branch, hint string) (string, *core.RefusalError) {
	argv := branchLookupArgv(f.provider, f.repo, branch)
	if argv == nil {
		return "", noHostMatch(f, branch, hint, "has no branch lookup")
	}
	ctx, cancel := context.WithTimeout(ctx, lookupTimeout)
	defer cancel()
	res, err := d.Commander.Run(ctx, &core.Command{Argv: argv, OutputCap: lookupCap})
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

// validatedNumbers parses the CLI's JSON list and returns every entry's number
// once each passes the same strict checks a pasted URL would: a decimal number
// in range, the asked-for head branch, and a web URL the shared parser reads
// as this provider, repository and number. One entry failing any check
// rejects the whole answer.
func validatedNumbers(stdout string, f *found, branch string, env map[string]string) ([]string, bool) {
	dec := json.NewDecoder(bytes.NewReader([]byte(stdout)))
	dec.UseNumber()
	var entries []map[string]any
	if err := dec.Decode(&entries); err != nil || dec.More() {
		return nil, false
	}
	keys := lookupFieldsFor(f.provider)
	var nums []string
	for _, e := range entries {
		num, ok := validEntry(e, &keys, f, branch, env)
		if !ok {
			return nil, false
		}
		nums = append(nums, num)
	}
	slices.Sort(nums)
	return nums, true
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
	return n.String(), true
}
