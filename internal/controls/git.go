package controls

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

const (
	shortLen    = 12 // abbreviates a commit in messages
	treeFields  = 4  // fields in one `git ls-tree -l` entry
	stderrCap   = 300
	symlinkMode = "120000" // tree mode of a tracked symlink
)

// fullSHA gates the base before it reaches a git argv (a provider-supplied
// value must never parse as a flag or a ref expression).
var fullSHA = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

// readBase reads every inventory path at the PR base commit. The base object
// must already be in the clone: the resolver never fetches, so a shallow
// checkout refuses instead of silently reading the head.
func readBase(ctx context.Context, cmd core.Commander, base string) (map[string][]byte, *core.RefusalError) {
	if !fullSHA.MatchString(base) {
		return nil, refuse(CodeUnavailable, "the bundle does not name a full PR base commit")
	}
	// One ls-tree sees every control at once: the blob size caps it before
	// any read, and mode 120000 (a tracked symlink) refuses.
	tree, err := runGit(ctx, cmd, 0, append([]string{"ls-tree", "-lz", base, "--"}, Inventory...)...)
	if err != nil {
		return nil, refuse(CodeUnavailable, fmt.Sprintf("PR base %s is not readable here (shallow checkout?): %v", base[:shortLen], err))
	}
	return readBlobs(ctx, cmd, base, tree)
}

// readBlobs reads every regular blob the ls-tree output lists.
func readBlobs(ctx context.Context, cmd core.Commander, base, tree string) (map[string][]byte, *core.RefusalError) {
	contents, total := map[string][]byte{}, 0
	for entry := range strings.FieldsFuncSeq(tree, func(r rune) bool { return r == 0 }) {
		path, size, ref := parseEntry(entry)
		if ref != nil {
			return nil, ref
		}
		if total += size; total > MaxTotalBytes {
			return nil, refuse(CodeTooLarge, fmt.Sprintf("controls exceed the %d-byte total cap — trim them at base", MaxTotalBytes))
		}
		body, err := runGit(ctx, cmd, size+1, "show", base+":"+path)
		if err != nil {
			return nil, refuse(CodeUnavailable, fmt.Sprintf("could not read %s at %s: %v", path, base[:shortLen], err))
		}
		contents[path] = []byte(body)
	}
	return contents, nil
}

// parseEntry validates one `ls-tree -l` entry: a regular blob under the
// per-file cap.
func parseEntry(entry string) (string, int, *core.RefusalError) {
	meta, path, _ := strings.Cut(entry, "\t")
	fields := strings.Fields(meta)
	if len(fields) != treeFields || fields[1] != "blob" || fields[0] == symlinkMode {
		return "", 0, refuse(CodeUnavailable, fmt.Sprintf("control %s is not a regular file — replace it with one", path))
	}
	size, err := strconv.Atoi(fields[treeFields-1])
	if err != nil || size > MaxFileBytes {
		return "", 0, refuse(CodeTooLarge, fmt.Sprintf("control %s is over the %d-byte cap", path, MaxFileBytes))
	}
	return path, size, nil
}

// runGit runs one git subcommand through the seam. A failure carries the
// first 300 bytes of stderr, never a bare exit status.
func runGit(ctx context.Context, cmd core.Commander, outputCap int, args ...string) (string, error) {
	res, err := cmd.Run(ctx, core.Command{Argv: append([]string{"git"}, args...), OutputCap: outputCap})
	if err == nil && res.StdoutTruncated {
		err = errors.New("output over its cap")
	}
	if err != nil {
		stderr := strings.TrimSpace(res.Stderr)
		return "", jq.Sealed(stderr[:min(len(stderr), stderrCap)], err)
	}
	return res.Stdout, nil
}
