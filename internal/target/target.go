// Package target is the one resolver every pull-request verb shares for its
// `<owner/repo> <pr>` positionals. A pasted pull or merge request URL stands
// for the pair (and implies the provider); an omitted pair is inferred from
// local state only: the `origin` remote for the repository, and a bundle
// already recorded under .verdict/state for the current branch's pull
// request. Nothing here touches the network, and nothing is guessed: an
// unmapped host, an ambiguous branch, or an unreadable checkout refuses with
// the exact arguments to pass.
package target

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/readcommon"
)

const (
	gitTimeout = 10 * time.Second
	gitCap     = 4096
	// bundleScanLimit bounds the bundle directory walk: past it the branch
	// cannot be proven to name exactly one pull request.
	bundleScanLimit = 2048
	sentinel        = "--"
	flagProvider    = "--provider"
)

// providerFlagVerbs are the verbs whose own flag parser takes --provider; a
// URL's provider reaches them as that flag. Every other verb reads the
// provider from the fetched bundle, so the URL names only repo and number.
var providerFlagVerbs = map[string]bool{"fetch": true, "threads": true, "drain": true, "pr-wait": true}

// Request is one invocation's positionals as the front door split them.
type Request struct {
	Env map[string]string
	// Repo and Pr are the first two positional tokens as typed: a pair, a
	// URL in Repo, a bare number in Repo, or empty.
	Repo, Pr string
	Verb     string
	Rest     []string
	// RepoOnly marks the verbs that take `<owner/repo>` and no number.
	RepoOnly bool
}

// Deps carries the two seams inference reads through, so tests script git and
// the checkout root.
type Deps struct {
	Commander core.Commander
	RepoRoot  func(context.Context) (string, error)
}

// Resolved is the target the verb runs against. Note is non-empty exactly when
// the target was derived rather than typed, so the front door can echo it.
// Refusal is set when the invocation named or implied a target that cannot be
// resolved; an empty Repo with no Refusal is an invocation inference could not
// even attempt (no checkout, no origin), which stays the usage error.
type Resolved struct {
	Refusal  *core.RefusalError
	Repo, Pr string
	Note     string
	Rest     []string
}

// Resolve applies the target rules to one invocation.
func Resolve(ctx context.Context, d Deps, q *Request) Resolved {
	out := &Resolved{Repo: q.Repo, Pr: q.Pr, Rest: q.Rest}
	switch {
	case core.IsTargetURL(q.Repo):
		fromURL(q, out)
	case q.RepoOnly:
		if q.Repo == "" {
			fromOrigin(ctx, d, q, out, "")
		}
	case q.Repo == "" && q.Pr == "":
		inferBoth(ctx, d, q, out)
	case q.Pr == "" && core.ValidPrNumber(q.Repo):
		fromOrigin(ctx, d, q, out, q.Repo)
	case q.Pr == "":
		inferPr(ctx, d, q, out, &found{repo: q.Repo})
	}
	return *out
}

func fromURL(q *Request, out *Resolved) {
	out.Repo, out.Pr = "", ""
	if q.Pr != "" {
		out.Refusal = &core.RefusalError{Code: core.CodeBadTargetURL, Message: fmt.Sprintf(
			"the pull request URL already names its number; drop %q and pass the URL alone", q.Pr)}
		return
	}
	t, ref := core.ParseTargetURL(q.Repo, q.Env)
	if ref != nil {
		out.Refusal = ref
		return
	}
	out.Repo, out.Note = t.Repo, describe(t.Repo, t.Pr, t.Provider, "from the URL")
	if !q.RepoOnly {
		out.Pr = t.Pr
	}
	if providerFlagVerbs[q.Verb] {
		out.Rest, out.Refusal = withProvider(q.Rest, t.Provider)
	}
}

// withProvider hands a URL's provider to the verb as --provider, refusing any
// --provider the operator typed that names a different one. Every occurrence
// before the sentinel is checked, and the "--provider=x" spelling (which most
// verbs' flag parsers reject) is rewritten to the spaced form they all take.
func withProvider(rest []string, provider string) ([]string, *core.RefusalError) {
	head, tail := rest, []string(nil)
	if at := slices.Index(rest, sentinel); at >= 0 {
		head, tail = rest[:at], rest[at:]
	}
	out, seen, ref := normalizeProvider(head, provider)
	if ref != nil {
		return rest, ref
	}
	if !seen {
		out = slices.Concat([]string{flagProvider, provider}, out)
	}
	return slices.Concat(out, tail), nil
}

// normalizeProvider rewrites each --provider in args to the spaced form and
// reports whether any was present; one that names another provider refuses.
func normalizeProvider(args []string, provider string) (out []string, seen bool, ref *core.RefusalError) {
	for i := 0; i < len(args); i++ {
		got, spaced, ok := providerOperand(args, i)
		if !ok {
			out = append(out, args[i])
			continue
		}
		if got != provider {
			return nil, false, &core.RefusalError{Code: core.CodeBadTargetURL, Message: fmt.Sprintf(
				"the URL is a %s pull request but --provider says %s; drop --provider, or pass the URL's own host", provider, got)}
		}
		seen = true
		out = append(out, flagProvider, got)
		if spaced {
			i++
		}
	}
	return out, seen, nil
}

// providerOperand reads the --provider operand at rest[i], in either
// spelling; spaced reports that it consumed the next token too.
func providerOperand(rest []string, i int) (value string, spaced, ok bool) {
	if rest[i] == flagProvider && i+1 < len(rest) {
		return rest[i+1], true, true
	}
	value, ok = strings.CutPrefix(rest[i], flagProvider+"=")
	return value, false, ok
}

func describe(repo, pr, provider, how string) string {
	target := repo
	if pr != "" {
		target += "#" + pr
	}
	if provider != "" {
		return fmt.Sprintf("target: %s (%s, %s)", target, provider, how)
	}
	return fmt.Sprintf("target: %s (%s)", target, how)
}

// found is the repository an inference step settled, with the provider when
// it came from the origin remote.
type found struct{ repo, provider, host string }

// fromOrigin fills the repository from origin; a non-empty pr completes the
// target (the `verdict report 12` spelling), otherwise the verb is repo-only.
func fromOrigin(ctx context.Context, d Deps, q *Request, out *Resolved, pr string) {
	f, ok := originRepo(ctx, d, q, out)
	if !ok {
		return
	}
	out.Repo, out.Pr = f.repo, pr
	out.Note = describe(f.repo, pr, f.provider, "repository from the origin remote")
}

func inferBoth(ctx context.Context, d Deps, q *Request, out *Resolved) {
	if f, ok := originRepo(ctx, d, q, out); ok {
		inferPr(ctx, d, q, out, f)
	}
}

// inferPr finds the one bundle that records the current branch for f.repo.
func inferPr(ctx context.Context, d Deps, q *Request, out *Resolved, f *found) {
	branch, ok := currentBranch(ctx, d)
	if !ok {
		return
	}
	hint := fmt.Sprintf("verdict %s %s <pr>", q.Verb, f.repo)
	if branch == "" {
		out.Refusal = core.TargetUnresolved("HEAD is detached, so no branch names a pull request; pass the target: %s", hint)
		return
	}
	hits, more := branchBundles(ctx, d, f, branch)
	labels := make([]string, len(hits))
	for i, h := range hits {
		labels[i] = h.label
	}
	switch {
	case more:
		out.Refusal = core.TargetUnresolved(
			"too many bundles under .verdict/state to match branch %q to one pull request; pass the target: %s", branch, hint)
	case len(hits) == 0:
		out.Refusal = core.TargetUnresolved("no bundle under .verdict/state records branch %q as the head of a pull request "+
			"for %s; fetch it first, or pass the target: %s", branch, f.repo, hint)
	case len(hits) > 1:
		out.Refusal = core.TargetUnresolved("branch %q heads more than one recorded pull request (%s); pass the one you mean: %s",
			branch, strings.Join(labels, ", "), hint)
	default:
		out.Repo, out.Pr = f.repo, hits[0].num
		out.Note = describe(f.repo, hits[0].num, f.provider,
			fmt.Sprintf("inferred from branch %q recorded in .verdict/state/%s", branch, hits[0].num))
	}
}

type branchHit struct {
	num   string
	label string
}

// branchBundles lists the PR bundles whose recorded head ref is branch and
// whose recorded repository, when it names one, is repo, in PR order. A hit's
// label reads "<pr> (<state>)"; more reports a directory too large to prove
// the match unique.
func branchBundles(ctx context.Context, d Deps, f *found, branch string) (hits []branchHit, more bool) {
	root, err := d.RepoRoot(ctx)
	if err != nil {
		return nil, false
	}
	state := readcommon.StateDir(root)
	entries, full, err := readcommon.ReadDirLimit(state, bundleScanLimit)
	if err != nil {
		return nil, false
	}
	for _, e := range entries {
		if hit, ok := entryHit(state, e, f, branch); ok {
			hits = append(hits, hit)
		}
	}
	slices.SortFunc(hits, func(a, b branchHit) int {
		return cmp.Or(cmp.Compare(len(a.num), len(b.num)), strings.Compare(a.num, b.num))
	})
	return hits, full
}

// entryHit matches one state-directory entry: only a directory named by a pull
// request number can be a PR bundle.
func entryHit(state string, e os.DirEntry, f *found, branch string) (branchHit, bool) {
	if !e.IsDir() || !core.ValidPrNumber(e.Name()) {
		return branchHit{}, false
	}
	return bundleHit(state+"/"+e.Name(), e.Name(), f, branch)
}

// bundleHit reports whether one bundle records branch as its head ref for f.
func bundleHit(dir, num string, f *found, branch string) (branchHit, bool) {
	b := readcommon.OpenBundle(dir)
	if cmp.Or(b.StrField("headRefName"), b.StrField("head_ref")) != branch {
		return branchHit{}, false
	}
	if proj := readcommon.BundleProject(dir); proj != "" && !strings.EqualFold(proj, f.repo) {
		return branchHit{}, false
	}
	// Two hosts can carry the same owner/repo; the bundle's recorded host must
	// be the origin's when both are known (an explicit repo knows no host).
	if host := readcommon.BundleHost(dir); host != "" && f.host != "" && host != f.host {
		return branchHit{}, false
	}
	label := num
	if state := b.State(); state != "" {
		label += " (" + state + ")"
	}
	return branchHit{num: num, label: label}, true
}

// originRepo reads provider and repository from the origin remote. ok is
// false when git cannot answer (no checkout, no origin), which leaves the
// invocation to the usage error, or when the origin cannot be mapped, which
// sets the refusal.
func originRepo(ctx context.Context, d Deps, q *Request, out *Resolved) (f *found, ok bool) {
	raw, ok := git(ctx, d, "config", "--get", "remote.origin.url")
	if !ok || raw == "" {
		return nil, false
	}
	provider, repo, rerr := core.RemoteTarget(raw, q.Env)
	if rerr != nil {
		out.Refusal = core.TargetUnresolved(
			"%s; or pass the target: verdict %s <owner/repo> <pr> [--provider <name>]", rerr.Message, q.Verb)
		return nil, false
	}
	return &found{repo: repo, provider: provider, host: core.RemoteHost(raw)}, true
}

// currentBranch is the checked-out branch name; empty for a detached HEAD. ok
// is false when git cannot answer.
func currentBranch(ctx context.Context, d Deps) (name string, ok bool) {
	return git(ctx, d, "branch", "--show-current")
}

func git(ctx context.Context, d Deps, args ...string) (string, bool) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	res, err := d.Commander.Run(ctx, &core.Command{Argv: slices.Concat([]string{"git"}, args), OutputCap: gitCap})
	if err != nil || res.ExitCode != 0 || res.StdoutTruncated || ctx.Err() != nil {
		return "", false
	}
	return strings.TrimSpace(res.Stdout), true
}
