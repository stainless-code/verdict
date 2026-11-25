package contenttree

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/gitcmd"
	"github.com/stainless-code/verdict/internal/jq"
)

// Filtered is one filter driver the content's attributes select and the
// paths they select it for. verdict never runs a filter program, so these
// paths are named by their working-tree bytes (line endings cleaned), which
// may differ from what the configured filter would produce — for git-lfs,
// the real file where a commit holds its pointer — and a tree holding one
// matches only a commit that stores those same bytes.
type Filtered struct {
	Driver string
	Paths  []string
}

// filterListing lists the configured filter drivers' keys
// ([gitcmd.ListFilters]), read with the same environment as every other
// call, so a driver configured through GIT_CONFIG_COUNT is listed too.
func (g Git) filterListing(ctx context.Context) (string, error) {
	res, err := g.exec(ctx, gitcmd.ListFilters...)
	switch {
	case err != nil:
		return "", err
	case res.ExitCode == 1 && res.Stdout == "":
		return "", nil // no filter.* key at all
	case res.ExitCode != 0:
		return "", jq.NewErrf("git config exited %d: %s", res.ExitCode, reason(res))
	case res.StdoutTruncated:
		return "", jq.NewErrf("git config printed more than %d bytes", outputCap)
	}
	return res.Stdout, nil
}

// staged is what naming a snapshot's copies reports beside the ids: the
// paths a filter driver would have handled, and the paths written as git's
// checkout expands them ([Git.expand]).
type staged struct {
	filtered []Filtered
	expanded []string
}

// batchIDs names each batched file by the blob `git add` would make of the
// checkout's bytes ([Git.cleaned]); a snapshot's re-check (exact) names the
// copy's bytes as they are ([Git.exactIDs]). With copyTo set, the copies
// are made to hold what a checkout of those blobs holds ([Git.fillCopies]).
func (g Git) batchIDs(ctx context.Context, b *hashBatch, copyTo string) (map[string]string, staged, error) {
	if g.recheck != nil {
		ids, err := g.exactIDs(ctx, b)
		return ids, staged{}, err
	}
	var raw map[string]string
	if copyTo != "" {
		var err error
		if raw, err = g.rawIDs(ctx, b); err != nil {
			return nil, staged{}, err
		}
	}
	clean, drivers, err := g.cleaned(ctx, b.index, b.entries)
	if err != nil {
		return nil, staged{}, err
	}
	ids, stale, changed := compareCleaned(b, clean, raw)
	if copyTo == "" {
		return ids, staged{}, nil
	}
	st, err := g.fillCopies(ctx, b, clean, stale, changed, drivers, copyTo)
	return ids, st, err
}

// exactIDs names each batched file's bytes as they are, except the reclean
// paths, which are cleaned back to the blob their expansion came from.
func (g Git) exactIDs(ctx context.Context, b *hashBatch) (map[string]string, error) {
	ids, err := g.rawIDs(ctx, b)
	if err != nil || len(g.recheck.reclean) == 0 {
		return ids, err
	}
	var files []entry
	keys := map[string]string{}
	for i, e := range b.entries {
		if slices.Contains(g.recheck.reclean, e.path) {
			files = append(files, e)
			keys[e.path] = b.files[i]
		}
	}
	clean, _, err := g.cleaned(ctx, b.index, files)
	for path, file := range keys {
		ids[file] = clean[path]
	}
	return ids, err
}

// fillCopies makes the copies hold what a checkout of their cleaned blobs
// holds — the stale copies rewritten with their blob, then the paths git's
// checkout expands written as it writes them — and names the changed paths
// a configured driver would have handled.
func (g Git) fillCopies(ctx context.Context, b *hashBatch, clean, stale, changed map[string]string,
	drivers []string, copyTo string,
) (staged, error) {
	if err := g.writeCleaned(ctx, stale); err != nil {
		return staged{}, err
	}
	paths := make([]string, len(b.entries))
	for i, e := range b.entries {
		paths[i] = e.path
	}
	attrs, err := g.attributes(ctx, paths)
	if err != nil {
		return staged{}, err
	}
	expanded := expanding(attrs)
	if err := g.expand(ctx, b, clean, expanded, copyTo); err != nil {
		return staged{}, err
	}
	filtered, err := g.filtered(ctx, attrs, changed, drivers)
	return staged{filtered: filtered, expanded: expanded}, err
}

// compareCleaned keys each batched file's cleaned id (clean, by path) by
// file, and returns the copies whose bytes (raw, nil when there are none)
// are not their cleaned blob, and, by path, the cleaned ids that are not
// what the index records. A file whose bytes are what the index holds — an
// LFS pointer left unsmudged — is named as a commit names it, driver or not.
func compareCleaned(b *hashBatch, clean, raw map[string]string) (ids, stale, changed map[string]string) {
	ids = make(map[string]string, len(b.files))
	stale, changed = map[string]string{}, map[string]string{}
	for i, file := range b.files {
		e := b.entries[i]
		ids[file] = clean[e.path]
		if raw != nil && ids[file] != raw[file] {
			stale[file] = ids[file]
		}
		if ids[file] != e.id {
			changed[e.path] = ids[file]
		}
	}
	return ids, stale, changed
}

// cleaned returns, by path, the blob id `git add` would give each file's
// working-tree bytes: git's own built-in clean step — core.autocrlf,
// core.eol, the text/eol/crlf attributes, ident, working-tree-encoding —
// with every configured filter driver switched off ([gitcmd.FilterOptions]),
// so no clean or process program (git-lfs included) runs. It also returns
// the drivers the listing named.
//
// The files are re-added into a throwaway index seeded with every entry of
// the user's index (seed), so the clean step reads what `git add` reads
// there: git leaves CRLF alone in a file whose indexed blob already holds
// CRLF (`hash-object` reads no index), and a tracked .gitattributes missing
// from disk is read from its indexed copy. A seeded entry has no stat data,
// so update-index hashes every file afresh instead of trusting a cache.
func (g Git) cleaned(ctx context.Context, seed, files []entry) (ids map[string]string, drivers []string, err error) {
	listing, err := g.filterListing(ctx)
	if err != nil {
		return nil, nil, err
	}
	index, cleanup, err := tempIndex()
	if err != nil {
		return nil, nil, err
	}
	defer cleanup()
	if err := g.readd(ctx, index, seed, files, gitcmd.FilterOptions(listing)); err != nil {
		return nil, nil, err
	}
	list := g.command(index, cmdLsFiles, flagZ, "--stage")
	list.OutputCap = listCap
	res, err := g.check(ctx, list)
	if err != nil {
		return nil, nil, err
	}
	staged, err := parseStage(res.Stdout)
	if err != nil {
		return nil, nil, err
	}
	ids = make(map[string]string, len(staged))
	for _, e := range staged {
		ids[e.path] = e.id
	}
	return ids, gitcmd.FilterDrivers(listing), nil
}

// readd seeds index with the seed entries and re-adds each file from the
// working tree under filterOff.
func (g Git) readd(ctx context.Context, index string, seed, files []entry, filterOff []string) error {
	var records, paths bytes.Buffer
	for _, e := range seed {
		fmt.Fprintf(&records, recordFmt, e.mode, e.id, e.path)
	}
	for _, e := range files {
		paths.WriteString(e.path + nul)
	}
	if err := g.writeIndex(ctx, index, records.Bytes()); err != nil {
		return err
	}
	update := g.command(index, slices.Concat(filterOff, []string{"update-index", flagZ, "--stdin"})...)
	update.Stdin = &paths
	_, err := g.check(ctx, update)
	return err
}

// filtered names the changed paths whose `filter` attribute selects one of
// the configured drivers and whose cleaned blob is not HEAD's either: the
// ones git would have run a program for, where that changes the content.
func (g Git) filtered(ctx context.Context, attrs map[string]pathAttrs, changed map[string]string, drivers []string) ([]Filtered, error) {
	byDriver := map[string][]string{}
	for path := range changed {
		if driver := attrs[path].filter; slices.Contains(drivers, driver) {
			byDriver[driver] = append(byDriver[driver], path)
		}
	}
	if len(byDriver) == 0 {
		return nil, nil
	}
	head, err := g.headBlobs(ctx, slices.Concat(slices.Collect(maps.Values(byDriver))...))
	if err != nil {
		return nil, err
	}
	return unlikeHead(byDriver, changed, head), nil
}

// unlikeHead keeps, by driver and sorted, the paths whose cleaned id
// (changed) is not HEAD's blob for them.
func unlikeHead(byDriver map[string][]string, changed, head map[string]string) []Filtered {
	var out []Filtered
	for _, driver := range slices.Sorted(maps.Keys(byDriver)) {
		paths := slices.DeleteFunc(byDriver[driver], func(p string) bool { return head[p] == changed[p] })
		if len(paths) > 0 {
			slices.Sort(paths)
			out = append(out, Filtered{Driver: driver, Paths: paths})
		}
	}
	return out
}

// batchHeaderMax bounds one `cat-file --batch` header line: id, type, size.
const batchHeaderMax = sha256Hex + 32

// cleanedChunk bounds the on-disk bytes one `cat-file --batch` run reads
// back, so its capture stays bounded however large the checkout.
const cleanedChunk = 64 << 20

// writeCleaned overwrites each staged copy whose cleaned blob differs from
// its bytes with that blob, so the snapshot holds exactly what the tree
// names. want maps a copy's path to the blob it must hold.
func (g Git) writeCleaned(ctx context.Context, want map[string]string) error {
	files := slices.Sorted(maps.Keys(want))
	for start := 0; start < len(files); {
		end, size, err := nextChunk(files, start)
		if err != nil {
			return err
		}
		if err := g.writeBlobs(ctx, files[start:end], want, size); err != nil {
			return err
		}
		start = end
	}
	return nil
}

// nextChunk is where the chunk of files starting at start ends, and its
// bytes on disk: at least one file, and no more once cleanedChunk is
// reached.
func nextChunk(files []string, start int) (end int, size int64, err error) {
	for end = start; end < len(files) && (end == start || size < cleanedChunk); end++ {
		info, err := os.Stat(files[end])
		if err != nil {
			return 0, 0, jq.Sealed("inspect "+files[end], err)
		}
		size += info.Size()
	}
	return end, size, nil
}

// reencodeGrowth bounds how much cleaning can grow a file: it only drops
// bytes, except working-tree-encoding, whose re-encoding to UTF-8 at most
// triples one (a one-byte charset's character becomes up to three).
const reencodeGrowth = 3

// batchFields is the field count of a `cat-file --batch` header.
const batchFields = 3

// writeBlobs reads the wanted blobs of files in one `cat-file --batch` run
// and writes each over its file. size is the files' bytes on disk, which
// bounds the capture ([reencodeGrowth]).
func (g Git) writeBlobs(ctx context.Context, files []string, want map[string]string, size int64) error {
	var ids strings.Builder
	for _, f := range files {
		ids.WriteString(want[f] + "\n")
	}
	cmd := g.command("", "cat-file", "--batch")
	cmd.Stdin = strings.NewReader(ids.String())
	cmd.OutputCap = int(reencodeGrowth*size) + len(files)*(batchHeaderMax+1) + outputCap
	res, err := g.check(ctx, cmd)
	if err != nil {
		return err
	}
	out := res.Stdout
	for _, f := range files {
		body, rest, err := batchObject(out, want[f])
		if err != nil {
			return err
		}
		if err := os.WriteFile(f, []byte(body), filePerm); err != nil {
			return jq.Sealed("write "+f, err)
		}
		out = rest
	}
	return nil
}

// batchObject splits one `<id> blob <size>\n<bytes>\n` record off out.
func batchObject(out, id string) (body, rest string, err error) {
	header, after, ok := strings.Cut(out, "\n")
	fields := strings.Fields(header)
	if !ok || len(fields) != batchFields || fields[0] != id || fields[1] != "blob" {
		return "", "", jq.NewErrf("git cat-file printed %q for blob %s", truncate(header), id)
	}
	n, err := strconv.Atoi(fields[2])
	if err != nil || n < 0 || n >= len(after) || after[n] != '\n' {
		return "", "", jq.NewErrf("git cat-file printed a malformed record for blob %s", id)
	}
	return after[:n], after[n+1:], nil
}
