package contenttree

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/jq"
)

// The index modes: a submodule commit, a symbolic link, and the two files.
const (
	gitlinkMode = "160000"
	symlinkMode = "120000"
	execMode    = "100755"
	fileMode    = "100644"
)

// ownerExec is the owner-execute bit that makes a file 100755.
const ownerExec = 0o100

// stageFields is the field count of an `ls-files --stage` record's metadata.
const stageFields = 3

// recordFmt is one `update-index --index-info` record: mode, id, path.
const recordFmt = "%s %s\t%s" + nul

// entry is one index path: its mode and recorded id.
type entry struct {
	path string
	mode string
	id   string
}

// name builds the content tree in a throwaway index from the listed entries.
// With copyTo set, each file is copied there first and the copy is what gets
// hashed, and the kept entries' index bytes are written there too.
func (g Git) name(ctx context.Context, entries, kept []entry, copyTo string) (string, error) {
	index, cleanup, err := tempIndex()
	if err != nil {
		return "", err
	}
	defer cleanup()
	var records bytes.Buffer
	if err := g.records(ctx, &records, entries, kept, copyTo); err != nil {
		return "", err
	}
	if err := g.writeIndex(ctx, index, records.Bytes()); err != nil {
		return "", err
	}
	res, err := g.run(ctx, index, "write-tree")
	if err != nil {
		return "", err
	}
	return objectID("write-tree", res.Stdout)
}

// records renders every entry's index record, copying as it goes when
// copyTo is set.
func (g Git) records(ctx context.Context, records *bytes.Buffer, entries, kept []entry, copyTo string) error {
	if err := g.restore(ctx, kept, copyTo); err != nil {
		return err
	}
	if err := g.stage(ctx, records, entries, copyTo); err != nil {
		return err
	}
	for _, e := range kept {
		fmt.Fprintf(records, recordFmt, e.mode, e.id, e.path)
	}
	return nil
}

// writeIndex loads the records into the throwaway index.
func (g Git) writeIndex(ctx context.Context, index string, records []byte) error {
	if len(records) == 0 {
		return nil
	}
	cmd := g.command(index, "update-index", flagZ, "--index-info")
	cmd.Stdin = bytes.NewReader(records)
	_, err := g.check(ctx, cmd)
	return err
}

// list reads the user's index (without writing it) and splits off the
// entries under verdict's state, which keep their index version. Listing
// hashes nothing, so no filter runs.
func (g Git) list(ctx context.Context) (entries, kept []entry, err error) {
	cmd := g.command("", cmdLsFiles, flagZ, "--stage")
	cmd.OutputCap = listCap
	staged, err := g.check(ctx, cmd)
	if err != nil {
		return nil, nil, err
	}
	all, err := parseStage(staged.Stdout)
	if err != nil {
		return nil, nil, err
	}
	kept = slices.DeleteFunc(slices.Clone(all), func(e entry) bool { return !isExcluded(e.path) || e.mode == gitlinkMode })
	return slices.DeleteFunc(all, func(e entry) bool { return isExcluded(e.path) }), kept, nil
}

// leftOut lists what the snapshot does not hold: untracked, unignored paths
// and the top-level entries .gitignore excludes. It is best effort — a
// listing git cannot produce only shortens a warning.
func (g Git) leftOut(ctx context.Context) (untracked, ignored []string) {
	untracked = g.paths(ctx, cmdLsFiles, flagZ, "--others", "--exclude-standard")
	for _, path := range g.paths(ctx, cmdLsFiles, flagZ, "--others", "--ignored", "--exclude-standard", "--directory") {
		top, _, _ := strings.Cut(strings.TrimSuffix(path, "/"), "/")
		if top != config.VerdictDir && !slices.Contains(ignored, top) {
			ignored = append(ignored, top)
		}
	}
	return slices.DeleteFunc(untracked, isExcluded), ignored
}

func (g Git) paths(ctx context.Context, args ...string) []string {
	cmd := g.command("", args...)
	cmd.OutputCap = listCap
	res, err := g.check(ctx, cmd)
	if err != nil {
		return nil
	}
	return slices.DeleteFunc(splitNUL(res.Stdout), func(p string) bool { return p == "" })
}

func splitNUL(out string) []string {
	return strings.Split(strings.TrimSuffix(out, nul), nul)
}

func gitlinks(entries []entry) []string {
	var out []string
	for _, e := range entries {
		if e.mode == gitlinkMode {
			out = append(out, e.path)
		}
	}
	return out
}

// parseStage parses `ls-files -z --stage` records. An unmerged path lists
// once per stage; it is named once, by its bytes.
func parseStage(out string) ([]entry, error) {
	var entries []entry
	for _, rec := range splitNUL(out) {
		if rec == "" {
			continue
		}
		e, err := parseStageRecord(rec)
		if err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return slices.CompactFunc(entries, func(a, b entry) bool { return a.path == b.path }), nil
}

// parseStageRecord parses "<mode> <id> <stage>\t<path>".
func parseStageRecord(rec string) (entry, error) {
	meta, path, ok := strings.Cut(rec, "\t")
	fields := strings.Fields(meta)
	if !ok || len(fields) != stageFields || path == "" {
		return entry{}, jq.NewErrf("git ls-files printed %q, not an index entry", truncate(rec))
	}
	if _, err := objectID(cmdLsFiles, fields[1]); err != nil {
		return entry{}, err
	}
	return entry{path: path, mode: fields[0], id: fields[1]}, nil
}

func isExcluded(path string) bool {
	return slices.ContainsFunc(excluded, func(ex string) bool {
		return path == ex || strings.HasPrefix(path, ex+"/")
	})
}

// hashBatch collects the files one `hash-object --stdin-paths` run hashes,
// with each one's index mode and path.
type hashBatch struct {
	modes map[string][2]string
	files []string
}

func (b *hashBatch) add(file, mode, path string) {
	b.files = append(b.files, file)
	b.modes[file] = [2]string{mode, path}
}

// stage renders the index records for the working tree's state of each
// entry, copying each file to copyTo first when it is set.
func (g Git) stage(ctx context.Context, records *bytes.Buffer, entries []entry, copyTo string) error {
	batch := hashBatch{modes: map[string][2]string{}}
	for _, e := range entries {
		if err := g.stageEntry(ctx, records, &batch, e, copyTo); err != nil {
			return err
		}
	}
	return g.hashFiles(ctx, records, &batch)
}

// stageEntry handles one entry: a submodule keeps its recorded commit (its
// content is not this checkout's), a path gone from disk is left out, a link
// is hashed as its target, and a file joins the batch.
func (g Git) stageEntry(ctx context.Context, records *bytes.Buffer, batch *hashBatch, e entry, copyTo string) error {
	if e.mode == gitlinkMode {
		fmt.Fprintf(records, recordFmt, e.mode, e.id, e.path)
		return makeDir(copyTo, e.path)
	}
	info, err := os.Lstat(g.path(e.path))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return jq.Sealed("inspect "+e.path, err)
	}
	mode, src, err := g.source(e, info, copyTo)
	if err != nil {
		return err
	}
	if mode == symlinkMode && src == "" {
		return g.stageLink(ctx, records, e.path)
	}
	if src != "" {
		batch.add(src, mode, e.path)
	}
	return nil
}

func (g Git) path(rel string) string {
	return filepath.Join(g.Root, filepath.FromSlash(rel))
}

// source classifies one working-tree path and, with copyTo set, copies it
// there. It returns the index mode and the file to hash ("" to skip).
func (g Git) source(e entry, info fs.FileInfo, copyTo string) (mode, src string, err error) {
	from := g.path(e.path)
	switch {
	case info.Mode()&fs.ModeSymlink != 0:
		return symlinkMode, "", copyLinkTo(from, copyTo, e.path)
	case !info.Mode().IsRegular():
		// A directory, fifo, or socket where a file was: git records none.
		return "", "", nil
	}
	mode = fileModeOf(e, info)
	if copyTo == "" {
		return mode, from, nil
	}
	to := filepath.Join(copyTo, filepath.FromSlash(e.path))
	return mode, to, copyFile(from, to, mode == execMode)
}

// fileModeOf is the index mode of a regular file. Windows has no exec bit
// and (without the privilege) no links, so there git keeps the recorded
// mode, and a link is a file holding its target.
func fileModeOf(e entry, info fs.FileInfo) string {
	switch {
	case runtime.GOOS == "windows" && (e.mode == execMode || e.mode == symlinkMode):
		return e.mode
	case info.Mode()&ownerExec != 0:
		return execMode
	}
	return fileMode
}

// stageLink records a symbolic link's target as its blob, as git does.
func (g Git) stageLink(ctx context.Context, records *bytes.Buffer, path string) error {
	target, err := os.Readlink(g.path(path))
	if err != nil {
		return jq.Sealed("read link "+path, err)
	}
	id, err := g.hashStdin(ctx, strings.NewReader(target))
	if err != nil {
		return err
	}
	fmt.Fprintf(records, recordFmt, symlinkMode, id, path)
	return nil
}

// restore writes the index bytes of each kept entry into copyTo, unfiltered,
// so the snapshot holds what the tree names for verdict's own tracked paths.
func (g Git) restore(ctx context.Context, kept []entry, copyTo string) error {
	if copyTo == "" {
		return nil
	}
	for _, e := range kept {
		if err := g.restoreEntry(ctx, e, copyTo); err != nil {
			return err
		}
	}
	return nil
}

func (g Git) restoreEntry(ctx context.Context, e entry, copyTo string) error {
	cmd := g.command("", "cat-file", "blob", e.id)
	cmd.OutputCap = listCap
	res, err := g.check(ctx, cmd)
	if err != nil {
		return err
	}
	return writeFile(filepath.Join(copyTo, filepath.FromSlash(e.path)), []byte(res.Stdout), e.mode == execMode)
}

// hashFiles writes each batched file's bytes as a blob, unfiltered, and
// appends the index records. --stdin-paths reads lines (a trailing CR
// stripped), so a path holding either is hashed on its own.
func (g Git) hashFiles(ctx context.Context, records *bytes.Buffer, b *hashBatch) error {
	lines := slices.DeleteFunc(slices.Clone(b.files), func(f string) bool { return strings.ContainsAny(f, "\n\r") })
	for _, file := range b.files {
		if strings.ContainsAny(file, "\n\r") {
			if err := g.hashOne(ctx, records, b, file); err != nil {
				return err
			}
		}
	}
	return g.hashLines(ctx, records, b, lines)
}

func (g Git) hashOne(ctx context.Context, records *bytes.Buffer, b *hashBatch, file string) error {
	f, err := os.Open(file)
	if err != nil {
		return jq.Sealed("open "+file, err)
	}
	defer func() { _ = f.Close() }()
	id, err := g.hashStdin(ctx, f)
	if err != nil {
		return err
	}
	fmt.Fprintf(records, recordFmt, b.modes[file][0], id, b.modes[file][1])
	return nil
}

func (g Git) hashLines(ctx context.Context, records *bytes.Buffer, b *hashBatch, files []string) error {
	if len(files) == 0 {
		return nil
	}
	slashed := make([]string, len(files))
	for i, file := range files {
		slashed[i] = filepath.ToSlash(file)
	}
	cmd := g.command("", cmdHashObject, "-w", "--no-filters", "--stdin-paths")
	cmd.Stdin = strings.NewReader(strings.Join(slashed, "\n") + "\n")
	cmd.OutputCap = len(files)*(sha256Hex+1) + outputCap
	res, err := g.check(ctx, cmd)
	if err != nil {
		return err
	}
	ids := strings.Fields(res.Stdout)
	if len(ids) != len(files) {
		return jq.NewErrf("git hash-object printed %d ids for %d files", len(ids), len(files))
	}
	for i, file := range files {
		id, err := objectID(cmdHashObject, ids[i])
		if err != nil {
			return err
		}
		fmt.Fprintf(records, recordFmt, b.modes[file][0], id, b.modes[file][1])
	}
	return nil
}

// Tracked reports whether any index path is rel or lies below it: rel holds
// content the tree names. The match ignores letter case, as check-ignore
// does under core.ignorecase: on a case-insensitive filesystem NODE_MODULES
// and node_modules are one directory, so a tracked path under either
// spelling counts. A listing too long to capture is tracked content all the
// same.
func (g Git) Tracked(ctx context.Context, rel string) (bool, error) {
	res, err := g.exec(ctx, cmdLsFiles, flagZ, "--", ":(icase,literal)"+rel)
	switch {
	case err != nil:
		return false, err
	case res.ExitCode != 0:
		return false, jq.NewErrf("git ls-files exited %d: %s", res.ExitCode, firstLine(res.Stderr))
	}
	return res.Stdout != "" || res.StdoutTruncated, nil
}

// Ignored reports whether the repository's exclude rules ignore rel.
func (g Git) Ignored(ctx context.Context, rel string) (bool, error) {
	res, err := g.exec(ctx, "check-ignore", "-q", "--", rel)
	switch {
	case err != nil:
		return false, err
	case res.ExitCode == 0:
		return true, nil
	case res.ExitCode == 1:
		return false, nil
	}
	return false, jq.NewErrf("git check-ignore exited %d: %s", res.ExitCode, firstLine(res.Stderr))
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}
