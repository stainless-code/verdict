package fetchcore

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
	threadscore "github.com/stainless-code/verdict/internal/core/threads"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

const suffixSelected = "/selected.json"

// writeSelected records the reaction-scoped fix selection beside the bundle:
// the finding sigs whose posted root comment carries a human 👍. A failed
// reaction read keeps whatever selection the previous fetch wrote — a partial
// read must never narrow the scope — and no 👍 at all leaves no file, because
// an empty selection would claim a scope nobody asked for.
func writeSelected(ctx context.Context, r *session, dir, repo, pr, inlineBody string) *core.RefusalError {
	comments, ok := decodeComments(inlineBody)
	if !ok {
		return nil
	}
	sigs, ok := threadscore.SelectedSigs(ctx, r.reactionRead, repo, comments)
	if !ok {
		r.Warn("github reaction read failed — selected.json kept as it was; re-fetch after confirming gh auth")
		return nil
	}
	if len(sigs) == 0 {
		return discardSelected(dir)
	}
	file := &core.SelectedFile{
		SchemaVersion: threadscore.SchemaVersion,
		Repo:          repo,
		Pr:            pr,
		HeadSHA:       readcommon.PrField(dir, keyHeadSHA),
		SelectedAt:    r.nowUTC(),
		Sigs:          sigs,
	}
	path := dir + suffixSelected
	if werr := atomic.WriteFile(path, []byte(jq.PrettyOrdered(core.Wire(file)))); werr != nil {
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf(
			"could not write %s: %v", path, werr)}
	}
	fmt.Fprintf(r.Human, "» selected: %d reacted finding(s) -> %s\n", len(sigs), r.display(path))
	return nil
}

// discardSelected removes a stale selection so a bundle never keeps claiming
// a scope the current reactions no longer name.
func discardSelected(dir string) *core.RefusalError {
	path := dir + suffixSelected
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf(
			"could not remove %s: %v", path, err)}
	}
	return nil
}

// decodeComments reads the review-comment array fetch already wrote to
// inline.json; an unreadable body reports false so the selection is untouched.
func decodeComments(body string) ([]any, bool) {
	doc, err := jq.Decode([]byte(body))
	if err != nil {
		return nil, false
	}
	comments, ok := doc.([]any)
	return comments, ok
}

// reactionRead reads one paginated GitHub endpoint through the run's bounded
// host client, flattening every page.
func (s *session) reactionRead(ctx context.Context, endpoint string) ([]any, bool) {
	res, err := s.ghFetch(ctx, ghCmdAPI, endpoint, flagPaginate, "--slurp")
	if err != nil {
		return nil, false
	}
	doc, derr := jq.Decode([]byte(res.Stdout))
	if derr != nil {
		return nil, false
	}
	return threadscore.ReactionPages(doc), true
}
