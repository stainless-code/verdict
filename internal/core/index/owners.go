package indexcore

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
)

// The repository-root path separator every pattern and path uses.
const pathSeparator = "/"

// GitHub's CODEOWNERS search order: the first file present at the tree wins.
var codeownersLocations = []string{".github/CODEOWNERS", "CODEOWNERS", "docs/CODEOWNERS"}

// ownerRule is one parsed pattern and the owners it names.
type ownerRule struct {
	re     *regexp.Regexp
	owners []string
}

// codeownersFile is one CODEOWNERS blob read at a tree.
type codeownersFile struct {
	source      string
	rules       []ownerRule
	unsupported int
}

// codeownersWarning names the pattern shapes the matcher does not evaluate, so
// a rule that would change ownership on GitHub is never silently ignored.
const codeownersWarning = "» index: CODEOWNERS has %d rule line(s) the index does not evaluate " +
	"(negation, escape, or character class); those lines match nothing"

// readCodeowners reads the first CODEOWNERS present at the tree, in GitHub's
// order, from the git object store rather than the worktree. It returns nil
// when the tree carries none.
func (st *state) readCodeowners(
	ctx context.Context, dir string, entries []treeEntry,
) (*codeownersFile, *core.RefusalError) {
	entry, source, ok := codeownersEntry(entries)
	if !ok {
		return nil, nil
	}
	blobs, ref := st.gitBatch(ctx, dir, []string{entry.Blob})
	if ref != nil {
		return nil, ref
	}
	src, ok := blobs[entry.Blob]
	if !ok {
		return nil, &core.RefusalError{
			Code: codeIndexFailed, Message: "git cat-file did not return " + source + " (" + entry.Blob + ")",
		}
	}
	rules, unsupported := parseCodeowners(string(src))
	return &codeownersFile{source: source, rules: rules, unsupported: unsupported}, nil
}

// codeownersEntry finds the first CODEOWNERS location present at the tree.
func codeownersEntry(entries []treeEntry) (treeEntry, string, bool) {
	for _, location := range codeownersLocations {
		if entry, ok := entryByPath(entries, location); ok {
			return entry, location, true
		}
	}
	return treeEntry{}, "", false
}

// entryByPath finds one tree entry by its slash path.
func entryByPath(entries []treeEntry, p string) (treeEntry, bool) {
	for _, e := range entries {
		if e.Path == p {
			return e, true
		}
	}
	return treeEntry{}, false
}

// parseCodeowners reads one CODEOWNERS body: one pattern and its owners per
// line, last matching rule wins. A line whose pattern shape the matcher cannot
// evaluate is counted and skipped; a pattern with no owners is not a rule.
//
//nolint:revive // one branch per line shape keeps the grammar auditable
func parseCodeowners(src string) ([]ownerRule, int) {
	var rules []ownerRule
	unsupported := 0
	for line := range strings.SplitSeq(src, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		re, ok := compileOwnerPattern(fields[0])
		if !ok {
			unsupported++
			continue
		}
		rules = append(rules, ownerRule{re: re, owners: fields[1:]})
	}
	return rules, unsupported
}

// compileOwnerPattern turns one GitHub-style path pattern into a matcher. A
// leading slash anchors at the repository root; a pattern with no slash matches
// a path segment at any depth; a trailing slash matches everything below the
// directory. `*` stays inside one segment, `**` crosses segments only in
// gitignore's three positional forms (`**/`, `/**`, `/**/`) and is a plain `*`
// anywhere else, `?` matches one character. Negation, escapes, and character
// classes are refused here so the caller can warn about them.
func compileOwnerPattern(pattern string) (*regexp.Regexp, bool) {
	if pattern == "" || strings.HasPrefix(pattern, "!") || strings.ContainsAny(pattern, `\[]`) {
		return nil, false
	}
	dir := strings.HasSuffix(pattern, pathSeparator)
	pattern = strings.TrimSuffix(pattern, pathSeparator)
	anchored := strings.HasPrefix(pattern, pathSeparator) || strings.Contains(pattern, pathSeparator)
	pattern = strings.TrimPrefix(pattern, pathSeparator)
	if pattern == "" {
		return nil, false
	}
	body := ownerGlob(pattern)
	var expr string
	switch {
	case dir && anchored:
		expr = "^" + body + "/.*$"
	case dir:
		expr = "(?:^|/)" + body + "/.*$"
	case anchored:
		expr = "^" + body + "$"
	default:
		expr = "(?:^|/)" + body + "$"
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return nil, false
	}
	return re, true
}

// ownerGlobDoubleStar is the marker [ownerGlob] translates positionally: it
// crosses segments only in `**/`, `/**`, and `/**/`, and is a plain `*`
// anywhere else.
const ownerGlobDoubleStar = "**"

// ownerGlob converts the glob body of one pattern to a regexp fragment.
func ownerGlob(pattern string) string {
	var sb strings.Builder
	for i := 0; i < len(pattern); {
		rest := pattern[i:]
		switch {
		case strings.HasPrefix(rest, "**/"):
			sb.WriteString("(?:.*/)?")
			i += len("**/")
		case rest == ownerGlobDoubleStar && i > 0 && pattern[i-1] == pathSeparator[0]:
			sb.WriteString(".*")
			i += len(ownerGlobDoubleStar)
		case strings.HasPrefix(rest, ownerGlobDoubleStar):
			sb.WriteString("[^/]*")
			i += len(ownerGlobDoubleStar)
		case rest[0] == '*':
			sb.WriteString("[^/]*")
			i++
		case rest[0] == '?':
			sb.WriteString("[^/]")
			i++
		default:
			sb.WriteString(regexp.QuoteMeta(rest[:1]))
			i++
		}
	}
	return sb.String()
}

// matchOwners returns the owners of the last rule matching p, or nil when no
// rule matches.
func matchOwners(rules []ownerRule, p string) []string {
	var owners []string
	for i := range rules {
		if rules[i].re.MatchString(p) {
			owners = rules[i].owners
		}
	}
	return owners
}

// ownersAt answers one path's owners at a tree. An absent CODEOWNERS file or a
// path no rule matches is an empty owner list with the source reported, never
// an owner named nobody.
func (st *state) ownersAt(
	ctx context.Context, dir string, entries []treeEntry, p string,
) (*Target, *core.RefusalError) {
	target := &Target{Path: p}
	file, ref := st.readCodeowners(ctx, dir, entries)
	if ref != nil {
		return nil, ref
	}
	if file == nil {
		return target, nil
	}
	target.OwnersSource = file.source
	target.Owners = matchOwners(file.rules, p)
	if file.unsupported > 0 {
		st.warn(fmt.Sprintf(codeownersWarning, file.unsupported))
	}
	return target, nil
}

// normalizePath renders one operator-supplied repository path as the slash
// path the index stores.
func normalizePath(p string) string {
	p = strings.TrimPrefix(strings.TrimSpace(filepath.ToSlash(p)), "./")
	if p == "" {
		return ""
	}
	p = path.Clean(p)
	if p == "." || p == ".." || strings.HasPrefix(p, "../") {
		return ""
	}
	return p
}

// owners executes `index owners`: the CODEOWNERS rule that owns one path at
// the queried tree.
func (st *state) owners(ctx context.Context) (*Response, *core.RefusalError) {
	p := normalizePath(st.req.Path)
	if p == "" {
		return nil, &core.RefusalError{Code: codeBadFlags, Message: "index owners wants one repository path"}
	}
	root := st.sourceRoot()
	st.trust = st.readTrust(ctx, root)
	tree, commit, ref := st.queryRef(ctx, root)
	if ref != nil {
		return nil, ref
	}
	stale := st.warnStale(ctx, root, tree)
	entries, ref := st.lsTree(ctx, root, tree)
	if ref != nil {
		return nil, ref
	}
	if _, ok := entryByPath(entries, p); !ok {
		return nil, &core.RefusalError{
			Code: codeBadFlags, Message: "index owners: " + p + " is not a file at " + tree,
		}
	}
	target, ref := st.ownersAt(ctx, root, entries, p)
	if ref != nil {
		return nil, ref
	}
	st.line(ownerLine(target, tree))
	return &Response{
		At:   st.req.TimeUTC(),
		Next: "run verdict index co-changed " + quotePath(p) + " to see what moves with it",
		Data: &Data{
			Kind: QueryOwners, Path: p, Tree: tree, Commit: commit, Stale: stale, Target: target,
		},
	}, nil
}

// ownerLine renders one path's ownership without inventing an owner.
func ownerLine(target *Target, tree string) string {
	switch {
	case target.OwnersSource == "":
		return fmt.Sprintf("index: %s has no owners: no CODEOWNERS at %s", quotePath(target.Path), tree)
	case target.Owners == nil:
		return fmt.Sprintf("index: %s has no owner rule in %s at %s",
			quotePath(target.Path), target.OwnersSource, tree)
	default:
		return fmt.Sprintf("index: %s owned by %s (from %s at %s)",
			quotePath(target.Path), strings.Join(target.Owners, " "), target.OwnersSource, tree)
	}
}
