package fixcore

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/envsecret"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// The patch output's refusal text and refusal code.
const (
	// codePatchSecret refuses a patch that would write a credential value
	// into the bundle, where every later agent run reads it.
	codePatchSecret = "fix-patch-secret"
	// codeFixFailed refuses a bundle or worktree the pass cannot read or write.
	codeFixFailed = "fix-failed"
	// msgPatchSecret names where the patch would land and which shapes it
	// carries, never the matched bytes: the shape alone says what to rotate.
	msgPatchSecret = "fix.patch would carry a secret-shaped value (%s) — " +
		"remove it from the worktree (rotate it if it was real), then re-run; " +
		"the pass was not recorded and %s was not written"
	// msgPatchDiff refuses a worktree the diff command could not read.
	msgPatchDiff = "fix: could not diff the worktree against HEAD (%v) — " +
		"fix.patch is the change set a pass extends, so the pass needs it"
)

// writePatch writes fix.patch beside the ledger: the worktree's unified diff
// against HEAD, which is the change set the pass about to run will extend. It
// never applies anything and never commits — the operator applies the patch
// with `git apply`, and untracked files stay out until `git add -N` puts them
// in the diff. A patch carrying a credential shape refuses instead of landing
// on disk; an empty diff removes a stale patch and reports no path.
func (st *state) writePatch(ctx context.Context, dir string) (*string, *core.RefusalError) {
	path := dir + suffixFixPatch
	res, err := st.runGit(ctx, "git", "diff", "HEAD", "--no-color", "--no-ext-diff")
	if err != nil {
		return nil, st.refuse(codeFixFailed, fmt.Sprintf(msgPatchDiff, err))
	}
	if strings.TrimSpace(res.Stdout) == "" {
		if ref := removeStalePatch(path); ref != nil {
			return nil, ref
		}
		return nil, nil
	}
	if shapes := envsecret.SecretValueShapes(res.Stdout); len(shapes) > 0 {
		return nil, st.refuse(codePatchSecret,
			fmt.Sprintf(msgPatchSecret, strings.Join(shapes, ", "), path))
	}
	if err := atomic.WriteFile(path, []byte(res.Stdout)); err != nil {
		return nil, &core.RefusalError{
			Code:    codeFixFailed,
			Message: fmt.Sprintf("could not write %s: %v", path, err),
		}
	}
	st.humanf("» patch: %s (%d bytes, %d file(s))\n",
		path, len(res.Stdout), patchFiles(res.Stdout))
	return &path, nil
}

// removeStalePatch deletes a patch a previous pass left behind once the
// worktree is clean: a stale diff describes code that no longer differs, and
// an operator applying it would replay the wrong change.
func removeStalePatch(path string) *core.RefusalError {
	if !readcommon.IsFile(path) {
		return nil
	}
	if err := os.Remove(path); err != nil {
		return &core.RefusalError{
			Code:    codeFixFailed,
			Message: fmt.Sprintf("could not remove the stale %s: %v", path, err),
		}
	}
	return nil
}

// patchFiles counts the files a unified diff touches.
func patchFiles(diff string) int {
	if strings.TrimSpace(diff) == "" {
		return 0
	}
	return 1 + strings.Count(diff, "\ndiff --git ")
}
