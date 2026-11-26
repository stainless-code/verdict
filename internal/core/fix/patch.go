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

const (
	codePatchSecret = "fix-patch-secret"
	codeFixFailed   = "fix-failed"
	msgPatchSecret  = "fix.patch would carry a secret-shaped value (%s) — " +
		"remove it from the worktree (rotate it if it was real), then re-run; " +
		"the pass was not recorded and %s was not written"
	msgPatchDiff = "fix: could not diff the worktree against HEAD (%v) — " +
		"fix.patch is the change set a pass extends, so the pass needs it"
)

func (st *state) writePatch(ctx context.Context, dir string) (*string, *core.RefusalError) {
	path := dir + suffixFixPatch
	res, err := st.runGit(ctx, "diff", "HEAD", "--no-color")
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
			Message: core.Msgf("could not write %s: %v", path, err),
		}
	}
	st.progressf("» patch: %s (%d bytes, %d file(s))\n",
		path, len(res.Stdout), patchFiles(res.Stdout))
	return &path, nil
}

func removeStalePatch(path string) *core.RefusalError {
	if !readcommon.IsFile(path) {
		return nil
	}
	if err := os.Remove(path); err != nil {
		return &core.RefusalError{
			Code:    codeFixFailed,
			Message: core.Msgf("could not remove the stale %s: %v", path, err),
		}
	}
	return nil
}

func patchFiles(diff string) int {
	if strings.TrimSpace(diff) == "" {
		return 0
	}
	return 1 + strings.Count(diff, "\ndiff --git ")
}
