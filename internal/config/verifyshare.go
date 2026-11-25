package config

import (
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
)

// shareSep separates a share entry's segments: entries are slash paths on
// every host.
const shareSep = "/"

// buildVerify parses the verify section: share is an optional list of
// repository-relative directories the verify snapshot exposes read-only.
func buildVerify(raw any, path string) (*core.ConfigVerify, *core.RefusalError) {
	obj, ref := asObject(raw, path)
	if ref != nil {
		return nil, ref
	}
	share, ref := presentStrings(obj, "share", path, shareList)
	if ref != nil {
		return nil, ref
	}
	return &core.ConfigVerify{Share: share}, nil
}

// shareList validates every share entry in order.
func shareList(raw any, path string) ([]string, *core.RefusalError) {
	items, ok := raw.([]any)
	if !ok {
		return nil, refuse(CodeBadConfig, "%s: want an array of directory paths, got %s", path, jsonKind(raw))
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		entry, ref := shareEntry(item, out, path)
		if ref != nil {
			return nil, ref
		}
		out = append(out, entry)
	}
	return out, nil
}

// shareEntry validates one entry and refuses one that repeats or nests
// inside an earlier one: a nested share would be exposed twice, once through
// its parent, and no mechanism can make both views agree.
func shareEntry(item any, prior []string, path string) (string, *core.RefusalError) {
	entry, ok := item.(string)
	if !ok {
		return "", refuse(CodeBadConfig, "%s: want a string path, got %s", path, jsonKind(item))
	}
	if ref := checkShareEntry(entry, path); ref != nil {
		return "", ref
	}
	if i := slices.IndexFunc(prior, func(p string) bool { return sharesOverlap(entry, p) }); i >= 0 {
		return "", refuse(CodeBadConfig, "%s: %q overlaps %q — list each directory once, never one inside another",
			path, entry, prior[i])
	}
	return entry, nil
}

func sharesOverlap(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+shareSep) || strings.HasPrefix(b, a+shareSep)
}

// checkShareEntry refuses anything but a clean repository-relative slash
// path: no absolute or drive spelling, no backslash, no empty, `.`, or `..`
// segment, and no `.git` segment, which is git's own and never a dependency.
func checkShareEntry(entry, path string) *core.RefusalError {
	if entry == "" || strings.HasPrefix(entry, shareSep) || strings.HasPrefix(entry, "-") ||
		strings.ContainsAny(entry, `\:`) || !cleanOrgString(entry) {
		return refuse(CodeBadConfig, "%s: %q is not a repository-relative slash path", path, entry)
	}
	for seg := range strings.SplitSeq(entry, shareSep) {
		switch {
		case seg == "" || seg == "." || seg == "..":
			return refuse(CodeBadConfig, "%s: %q is not a clean path inside the checkout (no empty, '.', or '..' segment)",
				path, entry)
		case strings.EqualFold(seg, ".git"):
			return refuse(CodeBadConfig, "%s: %q names git's own directory, which verify never shares", path, entry)
		}
	}
	return nil
}
