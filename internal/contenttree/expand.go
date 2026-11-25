package contenttree

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/gitcmd"
	"github.com/stainless-code/verdict/internal/jq"
)

// The attributes a snapshot reads for each file, and what their values mean.
const (
	attrFilter   = "filter"
	attrIdent    = "ident"
	attrEncoding = "working-tree-encoding"
	attrSet      = "set"
	attrUnset    = "unset"
	attrNone     = "unspecified"
	// attrFields is the field count of one `check-attr -z` record.
	attrFields = 3
)

// pathAttrs is what a path's attributes select: the filter driver it names
// and whether git's checkout expands its blob — `ident` writes `$Id: <id> $`
// and `working-tree-encoding` re-encodes it — so its committed form is not
// what any checkout holds.
type pathAttrs struct {
	filter  string
	expands bool
}

// attributes reads the filter, ident, and working-tree-encoding attributes
// of paths from the checkout, as `git add` reads them.
func (g Git) attributes(ctx context.Context, paths []string) (map[string]pathAttrs, error) {
	if len(paths) == 0 {
		return map[string]pathAttrs{}, nil
	}
	cmd := g.command("", "check-attr", flagZ, "--stdin", attrFilter, attrIdent, attrEncoding)
	cmd.Stdin = strings.NewReader(strings.Join(paths, nul) + nul)
	cmd.OutputCap = listCap
	res, err := g.check(ctx, cmd)
	if err != nil {
		return nil, err
	}
	fields := splitNUL(res.Stdout)
	if len(fields)%attrFields != 0 {
		return nil, jq.NewErrf("git check-attr printed %d fields, not records of %d", len(fields), attrFields)
	}
	attrs := make(map[string]pathAttrs, len(paths))
	for i := 0; i < len(fields); i += attrFields {
		attrs[fields[i]] = withAttr(attrs[fields[i]], fields[i+1], fields[i+2])
	}
	return attrs, nil
}

// withAttr adds one attribute's value to a.
func withAttr(a pathAttrs, name, value string) pathAttrs {
	switch name {
	case attrFilter:
		a.filter = value
	case attrIdent:
		a.expands = a.expands || value == attrSet
	case attrEncoding:
		a.expands = a.expands || !slices.Contains([]string{attrNone, attrUnset, attrSet}, value)
	}
	return a
}

// expanding lists, sorted, the paths whose checkout expands their blob.
func expanding(attrs map[string]pathAttrs) []string {
	var out []string
	for _, path := range slices.Sorted(maps.Keys(attrs)) {
		if attrs[path].expands {
			out = append(out, path)
		}
	}
	return out
}

// expand writes each expanded path's cleaned blob (clean, by path) into
// copyTo as git's checkout writes it, so a command sees `$Id: <id> $` or the
// working-tree encoding where a checkout would, not the committed form. It
// is git's own built-in conversion: `checkout-index` from a throwaway index
// holding those blobs, with every configured filter driver switched off
// ([gitcmd.FilterOptions]) and no hook (checkout-index runs none). Line
// endings on those paths follow the checkout's own settings too.
func (g Git) expand(ctx context.Context, b *hashBatch, clean map[string]string, expanded []string, copyTo string) error {
	if len(expanded) == 0 {
		return nil
	}
	listing, err := g.filterListing(ctx)
	if err != nil {
		return err
	}
	index, cleanup, err := tempIndex()
	if err != nil {
		return err
	}
	defer cleanup()
	var records, paths bytes.Buffer
	for i, file := range b.files {
		if path := b.entries[i].path; slices.Contains(expanded, path) {
			fmt.Fprintf(&records, recordFmt, b.modes[file][0], clean[path], path)
			paths.WriteString(path + nul)
		}
	}
	if err := g.writeIndex(ctx, index, records.Bytes()); err != nil {
		return err
	}
	prefix := "--prefix=" + filepath.ToSlash(copyTo) + "/"
	cmd := g.command(index, slices.Concat(gitcmd.FilterOptions(listing),
		[]string{"checkout-index", "--force", flagZ, "--stdin", prefix})...)
	cmd.Stdin = &paths
	_, err = g.check(ctx, cmd)
	return err
}

// headBlobs returns, by path, the blob HEAD records for each of paths; a
// path HEAD lacks (or an unborn HEAD) is absent. A path holding a newline
// cannot be asked by name and is absent too.
func (g Git) headBlobs(ctx context.Context, paths []string) (map[string]string, error) {
	asked := slices.DeleteFunc(slices.Clone(paths), func(p string) bool { return strings.ContainsAny(p, "\n\r") })
	if len(asked) == 0 {
		return map[string]string{}, nil
	}
	var names strings.Builder
	for _, p := range asked {
		names.WriteString("HEAD:" + p + "\n")
	}
	cmd := g.command("", "cat-file", "--batch-check=%(objectname)")
	cmd.Stdin = strings.NewReader(names.String())
	cmd.OutputCap = listCap
	res, err := g.check(ctx, cmd)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSuffix(res.Stdout, "\n"), "\n")
	if len(lines) != len(asked) {
		return nil, jq.NewErrf("git cat-file printed %d lines for %d paths", len(lines), len(asked))
	}
	out := make(map[string]string, len(asked))
	for i, p := range asked {
		if id, err := objectID("cat-file", lines[i]); err == nil {
			out[p] = id
		}
	}
	return out, nil
}
