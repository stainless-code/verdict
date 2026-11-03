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

// readContents reads every inventory path at the base commit through the
// GitHub contents API (`gh api`, the same seam as the git reads): the
// fallback for a checkout that cannot supply the base (not a repository, or
// the base object missing). A 404 is an absent control; any other failure
// refuses. The per-file and total caps apply to the reported size, before
// any byte is decoded.
func readContents(ctx context.Context, cmd core.Commander, repo, base string) (map[string][]byte, *core.RefusalError) {
	if !repoSlug.MatchString(repo) {
		return nil, refuse(CodeUnavailable, "the repository is not an owner/repo slug — cannot read controls through the contents API")
	}
	contents, total := map[string][]byte{}, 0
	for _, path := range Inventory {
		doc, ref := fetchContent(ctx, cmd, repo, base, path)
		if ref != nil {
			return nil, ref
		}
		if doc == nil {
			continue // absent at base: no control
		}
		if total += doc.Size; total > MaxTotalBytes {
			return nil, refuse(CodeTooLarge, fmt.Sprintf("controls exceed the %d-byte total cap — trim them at base", MaxTotalBytes))
		}
		raw, ref := decodeContent(path, doc)
		if ref != nil {
			return nil, ref
		}
		contents[path] = raw
	}
	return contents, nil
}

// fetchContent asks the contents API for one path: nil, nil is a 404.
func fetchContent(ctx context.Context, cmd core.Commander, repo, base, path string) (*contentsDoc, *core.RefusalError) {
	endpoint := "repos/" + repo + "/contents/" + path + "?ref=" + base
	res, err := cmd.Run(ctx, core.Command{Argv: []string{"gh", "api", endpoint}, OutputCap: contentsCap})
	if err == nil && res.StdoutTruncated {
		return nil, refuse(CodeTooLarge, fmt.Sprintf("control %s is over the %d-byte cap", path, MaxFileBytes))
	}
	if err != nil {
		stderr := strings.TrimSpace(res.Stderr)
		if strings.Contains(stderr, httpNotFound) {
			return nil, nil
		}
		return nil, refuse(CodeUnavailable, fmt.Sprintf("could not read %s at %s through the contents API: %v",
			path, base[:shortLen], jq.Sealed(stderr[:min(len(stderr), stderrCap)], err)))
	}
	var doc contentsDoc
	if json.Unmarshal([]byte(res.Stdout), &doc) != nil || doc.Type != typeFile {
		// A directory answers with an array, a symlink or submodule with
		// another type: none is a regular file.
		return nil, refuse(CodeUnavailable, fmt.Sprintf("control %s is not a regular file — replace it with one", path))
	}
	if doc.Size > MaxFileBytes {
		return nil, refuse(CodeTooLarge, fmt.Sprintf("control %s is over the %d-byte cap", path, MaxFileBytes))
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
