package fetchcore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// modeScan names the synthesized whole-tree diff source: `fetch-local --scan`
// reviews files rather than a git change, so the bundle needs no diff to exist.
const modeScan = "scan"

// codeBadScan is the scan selector's refusal: an operand outside the
// repository, inside a managed directory, a symlink, an unreadable file, or a
// tree whose synthesized diff would exceed what a bundle reader can load.
const codeBadScan = "bad-scan"

// Scan diff literals. The shape is git's own added-file section, so every
// downstream reader (hunks.tsv, patch identity, exclusions, coverage) parses a
// scan exactly like a fetched diff.
const (
	// scanSniffBytes is git's binary sniff window: a NUL in the first 8000
	// bytes marks the file binary and only its marker line is emitted.
	scanSniffBytes = 8000
	// scanProjectedPerFile is the fixed per-section overhead the projection
	// charges (header, mode line, hunk header, no-newline marker).
	scanProjectedPerFile = 128
	// scanByteFactor and scanPathByteFactor bound a section's growth: a "+" and
	// its newline per content byte, plus the three path tokens a section
	// spells (`a/...`, `b/...`, and the `+++ b/...` line) at their quoted
	// worst case, each twice the path's own bytes.
	scanByteFactor     = 2
	scanPathByteFactor = 12
	// scanQuoteLow and scanQuoteHigh bracket the bytes a C-quoted path escapes
	// as octal: control bytes below 0x20 and every byte from DEL up.
	scanQuoteLow  = 0x20
	scanQuoteHigh = 0x7f
	// scanUnbornOID is git's `status --porcelain=v2 --branch` marker for a
	// branch that holds no commit yet: the only positive proof of an unborn
	// HEAD, as opposed to a read that failed for another reason.
	scanUnbornOID       = "# branch.oid (initial)"
	scanRootName        = "."
	scanParentName      = ".."
	scanHeaderFormat    = "diff --git %s %s\n"
	scanModeLine        = "new file mode 100644\n"
	scanNullSide        = "--- /dev/null\n"
	scanNewSidePrefix   = "+++ "
	scanHunkFormat      = "@@ -0,0 +1,%d @@\n"
	scanNoNewlineMarker = "\\ No newline at end of file\n"
	scanBinaryFormat    = "Binary files /dev/null and %s differ\n"
	scanSideAdd         = "a/"
	scanSideDelete      = "b/"
	scanNumstatBinary   = "-\t-\t"
	scanNumstatText     = "\t0\t"
)

// scanManagedDirs are the repository-managed names a scan never reads: the
// object store and the review state each belong to a tool, not to a reviewer. A
// linked worktree or a submodule carries `.git` as a regular file naming the
// real git directory, so a managed name is skipped whatever its mode.
var scanManagedDirs = map[string]bool{".git": true, ".verdict": true}

// scanFile is one regular file a scan selected: its repository-relative
// slash-form path and the size the projection and the read bound use.
type scanFile struct {
	rel  string
	size int64
}

// scanTarget is one validated --scan operand: the directory entry the rooted
// chain found and its repository-relative slash path.
type scanTarget struct {
	info fs.FileInfo
	rel  string
}

// scanSkips counts the entries a walk left out, so the run can say so once
// instead of dropping them silently.
type scanSkips struct {
	symlinks        int
	special         int
	unrepresentable int
}

// scanCollector accumulates the selected files across every --scan operand,
// deduping overlapping roots and bounding the tree by the projection of the
// diff it would synthesize.
type scanCollector struct {
	seen      map[string]bool
	files     []scanFile
	skips     scanSkips
	projected int64
}

// localScan synthesizes the added-file diff in memory and returns the file list
// and numstat the shared gates read. It writes nothing: the exclusion pass
// publishes full.diff, so a refusal after the scan can never leave unfiltered
// content in the bundle.
func localScan(ctx context.Context, r *session, req *LocalRequest, f *LocalFlags) (string, string, string, *core.RefusalError) {
	root, ref := scanRoot(req)
	if ref != nil {
		return "", "", "", ref
	}
	files, skips, ref := collectScanFiles(ctx, root, f.Scan)
	if ref != nil {
		return "", "", "", ref
	}
	scanWarnings(r, f.Scan, files, skips)
	raw, listed, numstat, ref := scanDiff(ctx, root, files)
	if ref != nil {
		return "", "", "", ref
	}
	if err := ctx.Err(); err != nil {
		return "", "", "", &core.RefusalError{Code: codeCanceled, Message: msgCanceledFetch}
	}
	return raw, listed, numstat, nil
}

// scanWarnings reports what the scan selected and what it left out: the roots
// it read, the symlinks and special entries a walk never touches, and the file
// count the bundle now carries.
func scanWarnings(r *session, operands []string, files []scanFile, skips scanSkips) {
	fmt.Fprintf(r.Human, "scan: %d file(s) from %s\n", len(files), strings.Join(operands, spaceSep))
	if skips.symlinks > 0 {
		r.Warn(fmt.Sprintf("scan skipped %d symlink(s) — a scan never follows a link", skips.symlinks))
	}
	if skips.special > 0 {
		r.Warn(fmt.Sprintf("scan skipped %d non-regular entr(y/ies) — only regular files are reviewed", skips.special))
	}
	if skips.unrepresentable > 0 {
		r.Warn(fmt.Sprintf("scan skipped %d path(s) carrying a tab or newline — a diff line cannot carry them", skips.unrepresentable))
	}
}

// scanRoot resolves the physical repository root every scan confines to.
func scanRoot(req *LocalRequest) (string, *core.RefusalError) {
	root := req.RepoRootPath()
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", scanRefusal("could not resolve the repository root %s: %v", root, err)
	}
	physical, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", scanRefusal("could not resolve the repository root %s: %v", abs, err)
	}
	return physical, nil
}

// collectScanFiles resolves every --scan operand and returns the deduped,
// path-sorted file set. Operands are validated through one rooted, no-follow
// walk before a single file is read, so a bad selector refuses with no bundle
// bytes written.
func collectScanFiles(ctx context.Context, root string, operands []string) ([]scanFile, scanSkips, *core.RefusalError) {
	c := &scanCollector{seen: map[string]bool{}}
	rootFS, err := os.OpenRoot(root)
	if err != nil {
		return nil, c.skips, scanRefusal("could not open the repository root %s: %v", root, err)
	}
	defer rootFS.Close()
	if ref := c.collectOperands(ctx, rootFS, root, operands); ref != nil {
		return nil, c.skips, ref
	}
	slices.SortFunc(c.files, func(a, b scanFile) int { return strings.Compare(a.rel, b.rel) })
	return c.files, c.skips, nil
}

// collectOperands validates and collects every operand in the order it was
// given, so an overlapping or repeated root contributes each file once.
func (c *scanCollector) collectOperands(
	ctx context.Context, rootFS *os.Root, root string, operands []string,
) *core.RefusalError {
	for _, operand := range operands {
		if err := ctx.Err(); err != nil {
			return &core.RefusalError{Code: codeCanceled, Message: msgCanceledFetch}
		}
		target, ref := resolveScanOperandIn(rootFS, root, operand)
		if ref != nil {
			return ref
		}
		if ref := c.collectTarget(ctx, rootFS, target); ref != nil {
			return ref
		}
	}
	return nil
}

// collectTarget collects one validated operand: a directory walks, a regular
// file joins the set directly.
func (c *scanCollector) collectTarget(ctx context.Context, rootFS *os.Root, target scanTarget) *core.RefusalError {
	if !target.info.IsDir() {
		return c.add(target.rel, target.info)
	}
	dir, err := readcommon.OpenDirectoryIn(rootFS, target.rel)
	if err != nil {
		return scanRefusal("could not open %s: %v", target.rel, err)
	}
	defer dir.Close()
	return c.walk(ctx, dir, target.rel)
}

// walk descends one opened directory in lexical order. Every child directory is
// opened from its parent with an identity check, so a directory swapped for a
// symlink after the listing is never followed or enumerated.
func (c *scanCollector) walk(ctx context.Context, dir *os.Root, rel string) *core.RefusalError {
	entries, err := fs.ReadDir(dir.FS(), scanRootName)
	if err != nil {
		return scanRefusal("could not read %s: %v", scanDisplay(rel), err)
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return &core.RefusalError{Code: codeCanceled, Message: msgCanceledFetch}
		}
		if ref := c.walkEntry(ctx, dir, rel, entry); ref != nil {
			return ref
		}
	}
	return nil
}

// walkEntry files one entry: a managed tree or file is pruned, a symlink is
// counted and skipped, and a regular file joins the set.
func (c *scanCollector) walkEntry(ctx context.Context, dir *os.Root, rel string, entry fs.DirEntry) *core.RefusalError {
	name := entry.Name()
	child := scanJoin(rel, name)
	if _, managed := scanManagedNameOf(dir, name); managed {
		return nil
	}
	if entry.IsDir() {
		next, err := readcommon.OpenDirectoryIn(dir, name)
		if err != nil {
			return scanRefusal("could not open %s: %v", child, err)
		}
		defer next.Close()
		return c.walk(ctx, next, child)
	}
	if entry.Type()&os.ModeSymlink != 0 {
		c.skips.symlinks++
		return nil
	}
	if !entry.Type().IsRegular() {
		c.skips.special++
		return nil
	}
	info, err := dir.Lstat(name)
	if err != nil {
		return scanRefusal("could not stat %s: %v", child, err)
	}
	return c.add(child, info)
}

// add records one regular file under its repository-relative slash path,
// deduping repeats and refusing once the synthesized diff would pass the
// bundle read cap. The per-file guard runs before the projection arithmetic, so
// a sparse or corrupt size can never overflow it.
func (c *scanCollector) add(rel string, info fs.FileInfo) *core.RefusalError {
	if strings.ContainsAny(rel, "\t\r\n") {
		c.skips.unrepresentable++
		return nil
	}
	if c.seen[rel] {
		return nil
	}
	if info.Size() < 0 || info.Size() > maxFetchRead {
		return scanRefusal("--scan %s is over the %d-byte diff read cap — narrow --scan to a subdirectory", rel, int64(maxFetchRead))
	}
	c.seen[rel] = true
	file := scanFile{rel: rel, size: info.Size()}
	c.files = append(c.files, file)
	c.projected += scanProjected(file)
	if c.projected > maxFetchRead {
		return scanRefusal("--scan exceeds the %d-byte diff read cap — narrow --scan to a subdirectory", int64(maxFetchRead))
	}
	return nil
}

// scanProjected is the upper bound of one file's synthesized section: every
// byte can become a "+" line with its newline, plus the fixed section overhead
// and the quoted path tokens' worst-case expansion.
func scanProjected(file scanFile) int64 {
	return scanByteFactor*file.size + scanPathByteFactor*int64(len(file.rel)) + scanProjectedPerFile
}

// resolveScanOperand opens the repository root and resolves one operand, the
// entry point tests and single-operand callers share.
func resolveScanOperand(root, operand string) (scanTarget, *core.RefusalError) {
	rootFS, err := os.OpenRoot(root)
	if err != nil {
		return scanTarget{}, scanRefusal("could not open the repository root %s: %v", root, err)
	}
	defer rootFS.Close()
	return resolveScanOperandIn(rootFS, root, operand)
}

// resolveScanOperandIn validates one operand against the root without following
// a link: lexical containment comes first, so no metadata is asked about a
// path outside the repository, and then every raw component is walked through
// a rooted, no-follow chain, so a `..` can never erase a link it crossed and a
// case variant on a case-insensitive volume cannot alias a managed entry. The
// chain also spells each component the way the directory stores it, so two
// spellings of one entry dedupe instead of landing twice in the diff.
//
// A relative operand inherits the working directory's own components before the
// operand's, and the resolved path is what the managed-entry rule reads: a
// working directory inside `.verdict` refuses an operand that stays there,
// while `../src/main.go` steps back out and scans the ordinary tree.
func resolveScanOperandIn(rootFS *os.Root, root, operand string) (scanTarget, *core.RefusalError) {
	if operand == "" || strings.ContainsRune(operand, 0) {
		return scanTarget{}, scanRefusal("--scan %q: an empty or control-character path is not reviewable", operand)
	}
	base, rest, ref := scanOperandComponents(root, operand)
	if ref != nil {
		return scanTarget{}, ref
	}
	chain := newScanChain(rootFS)
	defer chain.close()
	if ref := chain.walk(operand, base, false); ref != nil {
		return scanTarget{}, ref
	}
	if ref := chain.walk(operand, rest, true); ref != nil {
		return scanTarget{}, ref
	}
	return chain.target(operand)
}

// scanOperandComponents splits one operand into the trusted components leading
// to the spelling's directory and the components still to validate. A relative
// operand is anchored at the physical working directory; an absolute operand
// must start at the physical root, the only absolute spelling a scan accepts.
// Nothing is collapsed here, so a `..` behind a link stays visible to the
// chain.
func scanOperandComponents(root, operand string) ([]string, []string, *core.RefusalError) {
	if filepath.IsAbs(operand) {
		rest, ref := scanAbsoluteComponents(root, operand)
		return nil, rest, ref
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, nil, scanRefusal("--scan %s: could not resolve the working directory: %v", operand, err)
	}
	physical, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return nil, nil, scanRefusal("--scan %s: could not resolve the working directory: %v", operand, err)
	}
	rel, inside := scanRel(root, physical)
	if !inside {
		return nil, nil, scanRefusal("--scan %s resolves outside the repository root %s", operand, root)
	}
	_, rest := scanSplitPath(operand)
	return scanSplitRel(rel), rest, nil
}

// scanAbsoluteComponents validates an absolute operand against the root's own
// spelling and returns the components the chain still walks. The operand's
// leading components must name the repository root; nothing else is inside.
func scanAbsoluteComponents(root, operand string) ([]string, *core.RefusalError) {
	volume, rest := scanSplitPath(operand)
	rootVolume, rootParts := scanSplitPath(root)
	if !strings.EqualFold(volume, rootVolume) || len(rest) < len(rootParts) {
		return nil, scanRefusal("--scan %s resolves outside the repository root %s", operand, root)
	}
	if ref := scanRootPrefixRefusal(operand, rest[:len(rootParts)], rootParts, root); ref != nil {
		return nil, ref
	}
	return rest[len(rootParts):], nil
}

// scanRootPrefixRefusal accepts an absolute operand's retained prefix only when
// it names the repository root itself. An exactly equal prefix needs no
// metadata; a case-insensitive-only match is accepted only when the operating
// system reports both spellings as the same directory, so `/work/repo` never
// stands in for `/work/Repo` on a case-sensitive volume.
func scanRootPrefixRefusal(operand string, prefix, rootParts []string, root string) *core.RefusalError {
	if slices.Equal(prefix, rootParts) {
		return nil
	}
	outside := scanRefusal("--scan %s resolves outside the repository root %s", operand, root)
	if !slices.EqualFunc(prefix, rootParts, strings.EqualFold) {
		return outside
	}
	volume := filepath.VolumeName(operand)
	spelled := volume + string(filepath.Separator) + strings.Join(prefix, string(filepath.Separator))
	spelledInfo, spelledErr := os.Stat(spelled)
	rootInfo, rootErr := os.Stat(root)
	if spelledErr != nil || rootErr != nil || !os.SameFile(spelledInfo, rootInfo) {
		return outside
	}
	return nil
}

// scanSplitPath splits an absolute path into its volume and slash components
// without cleaning: `.` and `..` survive for the chain to validate in order.
func scanSplitPath(abs string) (string, []string) {
	volume := filepath.VolumeName(abs)
	rest := strings.TrimPrefix(filepath.ToSlash(abs[len(volume):]), pathSep)
	if rest == "" {
		return volume, nil
	}
	return volume, strings.Split(rest, pathSep)
}

// scanSplitRel splits a repository-relative slash path into components.
func scanSplitRel(rel string) []string {
	if rel == "" {
		return nil
	}
	return strings.Split(rel, pathSep)
}

// scanChain is the rooted stack one operand resolves through: handles[0] is the
// repository root, and each deeper handle was opened from its parent with
// readcommon.OpenDirectoryIn, so no step can follow a link. names caches each
// open directory's stored entry names, read lazily, so a component can be
// spelled the way the volume stores it without re-listing a parent.
type scanChain struct {
	handles []*os.Root
	names   [][]string
	info    fs.FileInfo
	walked  []string
}

// newScanChain starts a chain at the caller's root handle.
func newScanChain(rootFS *os.Root) *scanChain {
	return &scanChain{handles: []*os.Root{rootFS}, names: make([][]string, 1)}
}

// close releases the subdirectory handles the chain opened; the root handle
// belongs to the caller.
func (ch *scanChain) close() {
	for _, handle := range ch.handles[1:] {
		_ = handle.Close()
	}
}

// dir is the handle at the chain's current depth.
func (ch *scanChain) dir() *os.Root { return ch.handles[len(ch.handles)-1] }

// walk steps every component in order. validated says whether a component is
// part of the operand (managed names and their case aliases are refused) or
// only the physical working directory the operand was spelled from; a working
// directory component that survives into the resolved path still refuses in
// target, so spelling an operand from inside a managed directory is refused
// while stepping back out of one is not.
func (ch *scanChain) walk(operand string, components []string, validated bool) *core.RefusalError {
	for i, part := range components {
		if ref := ch.step(operand, part, i == len(components)-1, validated); ref != nil {
			return ref
		}
	}
	return nil
}

// step applies one component: a dot is a no-op, a parent pops one level, and
// anything else descends.
func (ch *scanChain) step(operand, part string, last, validated bool) *core.RefusalError {
	switch part {
	case "", scanRootName:
		return nil
	case scanParentName:
		return ch.up(operand)
	default:
		return ch.down(operand, part, last, validated)
	}
}

// up pops one validated level. It refuses at the root, so an operand can never
// name a path outside the repository.
func (ch *scanChain) up(operand string) *core.RefusalError {
	if len(ch.walked) == 0 {
		return scanRefusal("--scan %s resolves outside the repository root", operand)
	}
	if len(ch.handles) > 1 {
		_ = ch.handles[len(ch.handles)-1].Close()
		ch.handles = ch.handles[:len(ch.handles)-1]
	}
	ch.names = ch.names[:len(ch.handles)]
	ch.walked = ch.walked[:len(ch.walked)-1]
	ch.info = nil
	return nil
}

// down opens one component. A directory is opened from its parent with the
// confined no-follow helper; a symlink, a special file, and a non-final file
// component all refuse. The component is recorded under the spelling the
// directory stores, so `NOTES.txt` and `notes.txt` are one entry where the
// volume aliases them and two where it does not.
func (ch *scanChain) down(operand, name string, last, validated bool) *core.RefusalError {
	parent := ch.dir()
	if validated {
		if ref := scanManagedRefusal(parent, operand, name); ref != nil {
			return ref
		}
	}
	info, err := parent.Lstat(name)
	if err != nil {
		return scanStatRefusal(operand, err)
	}
	if ref := scanEntryRefusal(operand, info, last); ref != nil {
		return ref
	}
	name = ch.storedName(parent, info, name)
	if info.IsDir() {
		return ch.descend(operand, name, info)
	}
	ch.walked = append(ch.walked, name)
	ch.info = info
	return nil
}

// storedName spells one resolved component the way its directory stores it. An
// exact entry keeps its own spelling and costs no extra metadata; a case
// variant is accepted only when the volume reports the same entry, and then the
// stored spelling wins. A directory that cannot be listed keeps the literal
// spelling: the chain's own stat, no-follow opens, and revalidation still bound
// the read.
func (ch *scanChain) storedName(parent *os.Root, info fs.FileInfo, name string) string {
	depth := len(ch.handles) - 1
	if ch.names[depth] == nil {
		ch.names[depth] = scanEntryNames(parent)
	}
	return scanFoldedName(parent, ch.names[depth], info, name)
}

// scanFoldedName maps one spelling onto the stored entry it aliases: the exact
// entry wins, and a case variant wins only with the volume's confirmation.
func scanFoldedName(parent *os.Root, names []string, info fs.FileInfo, name string) string {
	if slices.Contains(names, name) {
		return name
	}
	for _, candidate := range names {
		if !strings.EqualFold(candidate, name) {
			continue
		}
		aliasInfo, err := parent.Lstat(candidate)
		if err == nil && os.SameFile(aliasInfo, info) {
			return candidate
		}
	}
	return name
}

// scanEntryNames lists one open directory's stored entry names, empty when the
// listing fails.
func scanEntryNames(dir *os.Root) []string {
	entries, err := fs.ReadDir(dir.FS(), scanRootName)
	if err != nil {
		return []string{}
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

// scanStatRefusal names a component the chain could not stat.
func scanStatRefusal(operand string, err error) *core.RefusalError {
	if errors.Is(err, fs.ErrNotExist) {
		return scanRefusal("--scan %s: no such path inside the repository", operand)
	}
	return scanRefusal("--scan %s: %v", operand, err)
}

// scanEntryRefusal refuses what the chain cannot step through: a link, a
// special file, or a file component that is not the last one.
func scanEntryRefusal(operand string, info fs.FileInfo, last bool) *core.RefusalError {
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		return scanRefusal("--scan %s is a symlink — scan the real path inside the repository instead", operand)
	case info.IsDir(), info.Mode().IsRegular() && last:
		return nil
	case !info.Mode().IsRegular():
		return scanRefusal("--scan %s is not a regular file or directory", operand)
	default:
		return scanRefusal("--scan %s crosses a non-directory — scan the real path inside the repository instead", operand)
	}
}

// descend opens one child directory without following a link and pushes it.
func (ch *scanChain) descend(operand, name string, info fs.FileInfo) *core.RefusalError {
	next, err := readcommon.OpenDirectoryIn(ch.dir(), name)
	if err != nil {
		return scanRefusal("--scan %s crosses a symlink or a non-directory — scan the real path inside the repository instead", operand)
	}
	ch.handles = append(ch.handles, next)
	ch.names = append(ch.names, nil)
	ch.walked = append(ch.walked, name)
	ch.info = info
	return nil
}

// target finalizes the chain into the operand's relative path and entry. A
// managed component that survived the walk refuses here: the operand may have
// been spelled from a working directory inside `.git` or `.verdict`, so only
// the resolved path says whether the review would read managed state.
func (ch *scanChain) target(operand string) (scanTarget, *core.RefusalError) {
	if managed, ok := scanManagedComponent(ch.walked); ok {
		return scanTarget{}, scanRefusal("--scan %s is inside %s, which a review never reads", operand, managed)
	}
	if ch.info == nil {
		info, err := ch.dir().Stat(scanRootName)
		if err != nil {
			return scanTarget{}, scanRefusal("--scan %s: %v", operand, err)
		}
		ch.info = info
	}
	rel := strings.Join(ch.walked, pathSep)
	if rel == "" {
		rel = scanRootName
	}
	return scanTarget{rel: rel, info: ch.info}, nil
}

// scanManagedComponent names the first managed component of a resolved path.
// The chain spells every component as stored, so this exact check catches a
// working directory inside `.git` or `.verdict` on every volume.
func scanManagedComponent(parts []string) (string, bool) {
	for _, part := range parts {
		if scanManagedDirs[part] {
			return part, true
		}
	}
	return "", false
}

// scanManagedRefusal refuses a managed entry named by an operand, including a
// case variant that aliases one on a case-insensitive volume.
func scanManagedRefusal(dir *os.Root, operand, name string) *core.RefusalError {
	managed, ok := scanManagedNameOf(dir, name)
	if !ok {
		return nil
	}
	return scanRefusal("--scan %s is inside %s, which a review never reads", operand, managed)
}

// scanManagedNameOf reports whether name is a managed entry in dir, returning
// the canonical managed name. An exact match is managed everywhere; a case
// variant is managed only when it names the same entry, which a
// case-insensitive volume does and a case-sensitive one does not.
func scanManagedNameOf(dir *os.Root, name string) (string, bool) {
	for managed := range scanManagedDirs {
		if name == managed {
			return managed, true
		}
		if strings.EqualFold(name, managed) && scanAliasesEntry(dir, name, managed) {
			return managed, true
		}
	}
	return "", false
}

// scanAliasesEntry reports whether two names in one directory resolve to the
// same entry.
func scanAliasesEntry(dir *os.Root, first, second string) bool {
	firstInfo, err := dir.Lstat(first)
	if err != nil {
		return false
	}
	secondInfo, err := dir.Lstat(second)
	if err != nil {
		return false
	}
	return os.SameFile(firstInfo, secondInfo)
}

// scanJoin appends one name to a repository-relative slash path.
func scanJoin(rel, name string) string {
	if rel == "" || rel == scanRootName {
		return name
	}
	return rel + pathSep + name
}

// scanDisplay renders a relative path for a message, naming the root itself as
// `.` so the refusal never reads as an empty path.
func scanDisplay(rel string) string {
	if rel == "" {
		return scanRootName
	}
	return rel
}

// scanRel expresses path relative to root as a slash path, reporting whether
// it is confined to root at all.
func scanRel(root, path string) (string, bool) {
	rel, err := filepath.Rel(root, path)
	if err != nil || filepath.IsAbs(rel) {
		return "", false
	}
	if rel == scanParentName || strings.HasPrefix(rel, scanParentName+string(filepath.Separator)) {
		return "", false
	}
	if rel == scanRootName {
		return "", true
	}
	return filepath.ToSlash(rel), true
}

// scanDiff synthesizes the added-file diff for files in the given order,
// returning the raw text plus the listed/numstat pair the downstream gates
// read. Every section is a created file: no content is compared, so a scan
// needs no commit, index, or diff to exist.
func scanDiff(ctx context.Context, root string, files []scanFile) (string, string, string, *core.RefusalError) {
	rootFS, err := os.OpenRoot(root)
	if err != nil {
		return "", "", "", scanRefusal("could not open the repository root %s: %v", root, err)
	}
	defer rootFS.Close()
	var diff, listed, numstat strings.Builder
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return "", "", "", &core.RefusalError{Code: codeCanceled, Message: msgCanceledFetch}
		}
		content, ref := readScanFile(rootFS, file)
		if ref != nil {
			return "", "", "", ref
		}
		scanSection(&diff, &listed, &numstat, file, content)
	}
	// The file list matches git's `--name-only` posture: one path per line and
	// no trailing blank, so readers that split the text never see an empty path.
	return diff.String(), strings.TrimRight(listed.String(), newline), strings.TrimRight(numstat.String(), newline), nil
}

// scanSection writes one file's added-file section: a binary marker, a
// header-only section for an empty file, or the hunk that carries the lines.
func scanSection(diff, listed, numstat *strings.Builder, file scanFile, content []byte) {
	addSide := scanGitPath(scanSideAdd, file.rel)
	deleteSide := scanGitPath(scanSideDelete, file.rel)
	fmt.Fprintf(diff, scanHeaderFormat, addSide, deleteSide)
	diff.WriteString(scanModeLine)
	listed.WriteString(file.rel + newline)
	if scanIsBinary(content) {
		fmt.Fprintf(diff, scanBinaryFormat, deleteSide)
		numstat.WriteString(scanNumstatBinary + file.rel + newline)
		return
	}
	lines := scanLineCount(content)
	if lines == 0 {
		numstat.WriteString("0" + scanNumstatText + file.rel + newline)
		return
	}
	diff.WriteString(scanNullSide)
	diff.WriteString(scanNewSidePrefix + deleteSide + newline)
	fmt.Fprintf(diff, scanHunkFormat, lines)
	scanAddedLines(diff, content)
	numstat.WriteString(strconv.Itoa(lines) + scanNumstatText + file.rel + newline)
}

// readScanFile reads one selected file through the confined no-follow open:
// the leaf is re-checked for a link, a non-regular type, and a changed inode
// before any byte is read, and the size bound refuses a file that grew past
// the size the projection approved.
func readScanFile(rootFS *os.Root, file scanFile) ([]byte, *core.RefusalError) {
	f, err := readcommon.OpenRegularNoSymlinkIn(rootFS, file.rel)
	if err != nil {
		return nil, scanReadRefusal(file.rel, err)
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, file.size+1))
	if err != nil {
		return nil, scanRefusal("--scan could not read %s: %v", file.rel, err)
	}
	if int64(len(raw)) > file.size {
		return nil, scanRefusal("--scan %s grew past the size the scan read — re-run the scan on a quiet tree", file.rel)
	}
	return raw, nil
}

// scanReadRefusal maps one confined-open failure: a link or a changed leaf is
// the scan's own refusal, anything else is the unreadable-file refusal.
func scanReadRefusal(rel string, err error) *core.RefusalError {
	if errors.Is(err, readcommon.ErrSymlinkPath) {
		return scanRefusal("--scan %s became a symlink or a non-regular file while it was read — re-run the scan on a quiet tree", rel)
	}
	return scanRefusal("--scan could not read %s: %v", rel, err)
}

// scanIsBinary applies git's heuristic: a NUL in the first 8000 bytes marks the
// file binary, so only its marker line enters the diff.
func scanIsBinary(content []byte) bool {
	window := content
	if len(window) > scanSniffBytes {
		window = window[:scanSniffBytes]
	}
	return slices.Contains(window, 0)
}

// scanLineCount counts the lines an added-file hunk carries: a trailing newline
// terminates the last line instead of opening another.
func scanLineCount(content []byte) int {
	if len(content) == 0 {
		return 0
	}
	count := bytes.Count(content, []byte(newline))
	if content[len(content)-1] != '\n' {
		count++
	}
	return count
}

// scanAddedLines writes the hunk body line by line, preserving empty lines and
// the no-newline marker exactly where git emits it, without materializing a
// slice of every line.
func scanAddedLines(out *strings.Builder, content []byte) {
	rest := content
	for len(rest) > 0 {
		end := bytes.IndexByte(rest, '\n')
		if end < 0 {
			out.WriteString("+")
			out.Write(rest)
			out.WriteString(newline)
			out.WriteString(scanNoNewlineMarker)
			return
		}
		out.WriteString("+")
		out.Write(rest[:end])
		out.WriteString(newline)
		rest = rest[end+1:]
	}
}

// scanGitPath renders one diff path token. A path carrying a space, quote,
// backslash, control byte, or non-ASCII byte is C-quoted the way git spells it,
// so the header stays unambiguous and the reader's unquoter restores it.
func scanGitPath(prefix, rel string) string {
	token := prefix + rel
	if !scanNeedsQuote(token) {
		return token
	}
	return scanQuote(token)
}

// scanNeedsQuote reports whether a path token cannot stand unquoted.
func scanNeedsQuote(token string) bool {
	for i := range len(token) {
		if scanQuoteByte(token[i]) {
			return true
		}
	}
	return false
}

// scanQuoteEscapes is the C-quote dialect's named escapes: every other byte is
// copied, and the octal form covers the rest.
var scanQuoteEscapes = map[byte]string{
	'"':  `\"`,
	'\\': `\\`,
	'\a': `\a`,
	'\b': `\b`,
	'\t': `\t`,
	'\n': `\n`,
	'\v': `\v`,
	'\f': `\f`,
	'\r': `\r`,
}

// scanQuoteByte reports whether one byte forces the quoted spelling.
func scanQuoteByte(c byte) bool {
	return c == ' ' || c == '"' || c == '\\' || c < scanQuoteLow || c >= scanQuoteHigh
}

// scanQuote renders the git C-quoted spelling of one path token.
func scanQuote(token string) string {
	var out strings.Builder
	out.Grow(len(token) + 2)
	out.WriteString(dblQuote)
	for i := range len(token) {
		scanQuoteByteInto(&out, token[i])
	}
	out.WriteString(dblQuote)
	return out.String()
}

// scanQuoteByteInto appends one byte in git's C-quote dialect.
func scanQuoteByteInto(out *strings.Builder, c byte) {
	if escape, ok := scanQuoteEscapes[c]; ok {
		out.WriteString(escape)
		return
	}
	if c < scanQuoteLow || c >= scanQuoteHigh {
		fmt.Fprintf(out, "\\%03o", c)
		return
	}
	out.WriteByte(c)
}

// scanRefusal builds the scan selector's typed refusal.
func scanRefusal(format string, args ...any) *core.RefusalError {
	return &core.RefusalError{Code: codeBadScan, Message: fmt.Sprintf(format, args...)}
}
