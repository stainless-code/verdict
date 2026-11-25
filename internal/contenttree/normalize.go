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
// paths are named by their working-tree bytes (line endings cleaned), not by
// what the driver would commit — for git-lfs, the real file, never its
// pointer — and a tree holding one matches no commit that stores them.
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
		reason, _, _ := strings.Cut(strings.TrimSpace(res.Stderr), "\n")
		return "", jq.NewErrf("git config exited %d: %s", res.ExitCode, reason)
	case res.StdoutTruncated:
		return "", jq.NewErrf("git config printed more than %d bytes", outputCap)
	}
	return res.Stdout, nil
}

// batchIDs names each batched file by the blob `git add` would make of the
// checkout's bytes ([Git.cleaned]); a snapshot's re-check (exact) names the
// copy's bytes as they are. With copyTo set, a copy whose bytes are not its
// cleaned blob is overwritten with that blob, so the snapshot holds exactly
// what the tree names, and the paths a filter driver would have handled are
// reported.
func (g Git) batchIDs(ctx context.Context, b *hashBatch, copyTo string) (map[string]string, []Filtered, error) {
	var raw map[string]string
	if g.exact || copyTo != "" {
		var err error
		if raw, err = g.rawIDs(ctx, b); err != nil {
			return nil, nil, err
		}
	}
	if g.exact {
		return raw, nil, nil
	}
	clean, drivers, err := g.cleaned(ctx, b.entries)
	if err != nil {
		return nil, nil, err
	}
	ids, stale, changed := compareCleaned(b, clean, raw)
	if copyTo == "" {
		return ids, nil, nil
	}
	filtered, err := g.fillCopies(ctx, stale, changed, drivers)
	return ids, filtered, err
}

// fillCopies writes the stale copies' cleaned blobs and names the changed
// paths a configured driver would have handled.
func (g Git) fillCopies(ctx context.Context, stale map[string]string, changed, drivers []string) ([]Filtered, error) {
	if err := g.writeCleaned(ctx, stale); err != nil {
		return nil, err
	}
	return g.filtered(ctx, changed, drivers)
}

// compareCleaned keys each batched file's cleaned id (clean, by path) by
// file, and returns the copies whose bytes (raw, nil when there are none)
// are not their cleaned blob, and the paths whose cleaned blob is not what
// the index records. A file whose bytes are what the index holds — an LFS
// pointer left unsmudged — is named as a commit names it, driver or not.
func compareCleaned(b *hashBatch, clean, raw map[string]string) (ids, stale map[string]string, changed []string) {
	ids = make(map[string]string, len(b.files))
	stale = map[string]string{}
	for i, file := range b.files {
		e := b.entries[i]
		ids[file] = clean[e.path]
		if raw != nil && ids[file] != raw[file] {
			stale[file] = ids[file]
		}
		if ids[file] != e.id {
			changed = append(changed, e.path)
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
// The files are re-added into a throwaway index seeded with their entries
// from the user's index: git leaves CRLF alone in a file whose indexed blob
// already holds CRLF, and only an index that holds that blob reproduces the
// rule (`hash-object` does not read one). A seeded entry has no stat data,
// so update-index hashes every file afresh instead of trusting a cache.
func (g Git) cleaned(ctx context.Context, files []entry) (ids map[string]string, drivers []string, err error) {
	listing, err := g.filterListing(ctx)
	if err != nil {
		return nil, nil, err
	}
	index, cleanup, err := tempIndex()
	if err != nil {
		return nil, nil, err
	}
	defer cleanup()
	if err := g.readd(ctx, index, files, gitcmd.FilterOptions(listing)); err != nil {
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

// readd seeds index with files' entries and re-adds each from the working
// tree under filterOff.
func (g Git) readd(ctx context.Context, index string, files []entry, filterOff []string) error {
	var seed, paths bytes.Buffer
	for _, e := range files {
		fmt.Fprintf(&seed, recordFmt, e.mode, e.id, e.path)
		paths.WriteString(e.path + nul)
	}
	if err := g.writeIndex(ctx, index, seed.Bytes()); err != nil {
		return err
	}
	update := g.command(index, slices.Concat(filterOff, []string{"update-index", flagZ, "--stdin"})...)
	update.Stdin = &paths
	_, err := g.check(ctx, update)
	return err
}

// attrFields is the field count of one `check-attr -z` record.
const attrFields = 3

// filtered names the paths whose `filter` attribute selects one of the
// configured drivers: the ones git would have run a program for.
func (g Git) filtered(ctx context.Context, paths, drivers []string) ([]Filtered, error) {
	if len(paths) == 0 || len(drivers) == 0 {
		return nil, nil
	}
	cmd := g.command("", "check-attr", flagZ, "--stdin", "filter")
	cmd.Stdin = strings.NewReader(strings.Join(paths, nul) + nul)
	cmd.OutputCap = listCap
	res, err := g.check(ctx, cmd)
	if err != nil {
		return nil, err
	}
	return byDriver(splitNUL(res.Stdout), drivers)
}

// byDriver groups `check-attr -z` records' paths by the driver their filter
// attribute names, keeping only drivers, sorted by driver.
func byDriver(fields, drivers []string) ([]Filtered, error) {
	if len(fields)%attrFields != 0 {
		return nil, jq.NewErrf("git check-attr printed %d fields, not records of %d", len(fields), attrFields)
	}
	byDriver := map[string][]string{}
	for i := 0; i < len(fields); i += attrFields {
		if driver := fields[i+2]; slices.Contains(drivers, driver) {
			byDriver[driver] = append(byDriver[driver], fields[i])
		}
	}
	out := make([]Filtered, 0, len(byDriver))
	for _, driver := range slices.Sorted(maps.Keys(byDriver)) {
		out = append(out, Filtered{Driver: driver, Paths: byDriver[driver]})
	}
	return out, nil
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
