package controls

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

const (
	// contentsCap bounds one contents response: GitHub inlines files up to
	// 1 MiB as base64 (~1.4 MiB) plus metadata, so the size field — not a
	// truncated stream — decides an over-cap refusal.
	contentsCap  = 2 << 20
	typeFile     = "file"
	typeDir      = "dir"
	encodingB64  = "base64"
	httpNotFound = "HTTP 404"
	binGH        = "gh"
	// maxTreeEntries and maxTreeDepth bound one recursive pack walk, not one
	// listing, so a hostile or runaway tree cannot allocate without limit.
	maxTreeEntries = 1024
	maxTreeDepth   = 8
)

// repoSlug gates the repository before it reaches a gh argv.
var repoSlug = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// contentsDoc is the subset of the GitHub contents response the reader trusts.
type contentsDoc struct {
	Type     string `json:"type"`
	Encoding string `json:"encoding"`
	Content  string `json:"content"`
	Size     int    `json:"size"`
}

// readContents reads every path at the base commit through the GitHub
// contents API (`gh api`, the same seam as the git reads): the
// fallback for a checkout that cannot supply the base (not a repository, or
// the base object missing). A 404 is an absent control; any other failure
// refuses. The per-file and total caps apply to the reported size, before
// any byte is decoded.
func readContents(ctx context.Context, cmd core.Commander, repo, base string, paths []string) (map[string][]byte, *core.RefusalError) {
	if !repoSlug.MatchString(repo) {
		return nil, refuse(CodeUnavailable, "the repository is not an owner/repo slug — cannot read controls through the contents API")
	}
	reader := contentsReader{contents: map[string][]byte{}, tree: treeBudget{root: PacksPrefix}}
	for _, path := range paths {
		var ref *core.RefusalError
		if path == PacksPrefix {
			ref = reader.readTree(ctx, cmd, repo, base, path, 0)
		} else {
			ref = reader.read(ctx, cmd, repo, base, path)
		}
		if ref != nil {
			return nil, ref
		}
	}
	return reader.contents, nil
}

// contentsEntry is one directory listing row the tree reader trusts.
type contentsEntry struct {
	Type string `json:"type"`
	Name string `json:"name"`
}

// readTree lists one pack directory recursively: directories descend, files
// join the ordinary capped read, and any other shape refuses. The shared tree
// budget spans the whole walk; depth starts at zero for the pack root.
func (r *contentsReader) readTree(
	ctx context.Context, cmd core.Commander, repo, base, path string, depth int,
) *core.RefusalError {
	if ref := r.tree.checkDepth(depth); ref != nil {
		return ref
	}
	entries, ref := listContents(ctx, cmd, repo, base, path)
	if ref != nil || entries == nil {
		return ref
	}
	if ref := r.tree.spend(len(entries)); ref != nil {
		return ref
	}
	for _, entry := range entries {
		if ref := r.readTreeEntry(ctx, cmd, repo, base, path, depth, entry); ref != nil {
			return ref
		}
	}
	return nil
}

// readTreeEntry dispatches one listed directory entry: descend, read, or
// refuse a shape that is neither.
func (r *contentsReader) readTreeEntry(
	ctx context.Context, cmd core.Commander, repo, base, dir string, depth int, entry contentsEntry,
) *core.RefusalError {
	full := dir + packSep + entry.Name
	switch entry.Type {
	case typeDir:
		return r.readTree(ctx, cmd, repo, base, full, depth+1)
	case typeFile:
		return r.read(ctx, cmd, repo, base, full)
	default:
		return refuse(CodeUnavailable, controlPrefix+full+" is not a regular file — replace it with one")
	}
}

// treeBudget bounds one recursive pack walk: every listing's entries spend one
// running total, and descent stops at the depth cap.
type treeBudget struct {
	root    string
	entries int
}

// checkDepth refuses a listing deeper than the walk may descend.
func (b *treeBudget) checkDepth(depth int) *core.RefusalError {
	if depth <= maxTreeDepth {
		return nil
	}
	return refuse(CodeTooLarge, fmt.Sprintf(
		"control %s nests deeper than %d directories — trim it at base", b.root, maxTreeDepth))
}

// spend charges one listing's entries against the shared total.
func (b *treeBudget) spend(listed int) *core.RefusalError {
	b.entries += listed
	if b.entries <= maxTreeEntries {
		return nil
	}
	return refuse(CodeTooLarge, fmt.Sprintf(
		"control %s lists more than %d entries across its tree — trim it at base", b.root, maxTreeEntries))
}

// escapePath percent-escapes each slash-separated segment of a repository
// path, so a name holding '#', '?', or '%' reaches the contents API as that
// name instead of being parsed as a fragment, a query, or already-escaped
// bytes. The caller keeps the stored key and every message on the raw path.
func escapePath(p string) string {
	segments := strings.Split(p, packSep)
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return strings.Join(segments, packSep)
}

// listContents asks the contents API for one directory: nil, nil is a 404.
func listContents(ctx context.Context, cmd core.Commander, repo, base, path string) ([]contentsEntry, *core.RefusalError) {
	endpoint := "repos/" + repo + "/contents/" + escapePath(path) + "?ref=" + base
	res, err := cmd.Run(ctx, &core.Command{Argv: []string{binGH, "api", endpoint}, OutputCap: contentsCap})
	if err == nil && res.ExitCode != 0 {
		err = res.ExitFailure(binGH)
	}
	if err != nil {
		if strings.Contains(strings.TrimSpace(res.Stderr), httpNotFound) {
			return nil, nil
		}
		return nil, refuse(CodeUnavailable, fmt.Sprintf("could not list %s at %s through the contents API",
			path, base[:shortLen]))
	}
	if res.StdoutTruncated {
		return nil, refuse(CodeTooLarge, fmt.Sprintf("control %s listing is over the %d-byte cap", path, contentsCap))
	}
	var entries []contentsEntry
	if json.Unmarshal([]byte(res.Stdout), &entries) != nil {
		return nil, refuse(CodeUnavailable, fmt.Sprintf("control %s is not a directory — replace it with one", path))
	}
	return entries, nil
}

// contentsReader accumulates the controls, their reported sizes, and the pack
// walk's shared budget.
type contentsReader struct {
	contents map[string][]byte
	tree     treeBudget
	total    int
}

// read records one inventory path: a 404 is absence, and the total cap
// applies before the bytes are kept.
func (r *contentsReader) read(ctx context.Context, cmd core.Commander, repo, base, path string) *core.RefusalError {
	raw, size, ref := contentAt(ctx, cmd, repo, base, path)
	if ref != nil {
		return ref
	}
	if raw == nil {
		return nil
	}
	r.total += size
	if r.total > MaxTotalBytes {
		return refuse(CodeTooLarge, fmt.Sprintf("controls exceed the %d-byte total cap — trim them at base", MaxTotalBytes))
	}
	r.contents[path] = raw
	return nil
}

// contentAt fetches and decodes one inventory path: nil bytes is a 404.
func contentAt(ctx context.Context, cmd core.Commander, repo, base, path string) ([]byte, int, *core.RefusalError) {
	doc, ref := fetchContent(ctx, cmd, repo, base, path)
	if ref != nil || doc == nil {
		return nil, 0, ref
	}
	raw, ref := decodeContent(path, doc)
	return raw, doc.Size, ref
}

// fetchContent asks the contents API for one path: nil, nil is a 404.
func fetchContent(ctx context.Context, cmd core.Commander, repo, base, path string) (*contentsDoc, *core.RefusalError) {
	endpoint := "repos/" + repo + "/contents/" + escapePath(path) + "?ref=" + base
	res, err := cmd.Run(ctx, &core.Command{Argv: []string{binGH, "api", endpoint}, OutputCap: contentsCap})
	if err == nil && res.ExitCode != 0 {
		err = res.ExitFailure(binGH)
	}
	if err != nil {
		return nil, contentsFailure(path, base, res, err)
	}
	if res.StdoutTruncated {
		return nil, refuse(CodeTooLarge, fmt.Sprintf("control %s is over the %d-byte cap", path, MaxFileBytes))
	}
	doc, ref := parseContents(path, res.Stdout)
	if ref != nil {
		return nil, ref
	}
	if doc.Size > MaxFileBytes {
		return nil, refuse(CodeTooLarge, fmt.Sprintf("control %s is over the %d-byte cap", path, MaxFileBytes))
	}
	return doc, nil
}

// contentsFailure maps one failed gh call: a 404 is absence, any other
// failure refuses.
func contentsFailure(path, base string, res core.Result, err error) *core.RefusalError {
	stderr := strings.TrimSpace(res.Stderr)
	if strings.Contains(stderr, httpNotFound) {
		return nil
	}
	return refuse(CodeUnavailable, fmt.Sprintf("could not read %s at %s through the contents API: %v",
		path, base[:shortLen], jq.Sealed(stderr[:min(len(stderr), stderrCap)], err)))
}

// parseContents decodes one API body and rejects non-file shapes.
func parseContents(path, stdout string) (*contentsDoc, *core.RefusalError) {
	var doc contentsDoc
	if json.Unmarshal([]byte(stdout), &doc) != nil || doc.Type != typeFile {
		// A directory answers with an array, a symlink or submodule with
		// another type: none is a regular file.
		return nil, refuse(CodeUnavailable, fmt.Sprintf("control %s is not a regular file — replace it with one", path))
	}
	return &doc, nil
}

// decodeContent decodes one response body and checks it against the
// reported size.
func decodeContent(path string, doc *contentsDoc) ([]byte, *core.RefusalError) {
	if doc.Encoding != encodingB64 {
		return nil, refuse(CodeUnavailable, fmt.Sprintf("control %s came back with encoding %q, not base64", path, doc.Encoding))
	}
	raw, err := base64.StdEncoding.DecodeString(strings.NewReplacer("\n", "", "\r", "").Replace(doc.Content))
	if err != nil || len(raw) != doc.Size {
		return nil, refuse(CodeUnavailable, fmt.Sprintf("control %s did not decode to its reported size", path))
	}
	return raw, nil
}
