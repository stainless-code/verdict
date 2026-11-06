package controls

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
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
	encodingB64  = "base64"
	httpNotFound = "HTTP 404"
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
	reader := contentsReader{contents: map[string][]byte{}}
	for _, path := range paths {
		if ref := reader.read(ctx, cmd, repo, base, path); ref != nil {
			return nil, ref
		}
	}
	return reader.contents, nil
}

// contentsReader accumulates the controls and their reported sizes.
type contentsReader struct {
	contents map[string][]byte
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
	endpoint := "repos/" + repo + "/contents/" + path + "?ref=" + base
	res, err := cmd.Run(ctx, core.Command{Argv: []string{"gh", "api", endpoint}, OutputCap: contentsCap})
	if err == nil && res.ExitCode != 0 {
		err = res.ExitFailure("gh")
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
