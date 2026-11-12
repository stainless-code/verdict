package readcommon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"github.com/stainless-code/verdict/internal/core"
)

// CheckLocalFresh refuses dirty bundles whose tracked working tree
// has changed.
func CheckLocalFresh(ctx context.Context, runner core.Commander, root, dir string) *core.RefusalError {
	source := dir + "/source.json"
	if JSONStr(source, "kind") != "worktree" || JSONRaw(source, "dirty") != "true" {
		return nil
	}
	if localDiffMatches(ctx, runner, root, JSONStr(source, "worktree_diff_sha256")) {
		return nil
	}
	return &core.RefusalError{Code: "coverage-stale", Message: "dirty working tree no longer matches the fetched review; re-run fetch-local"}
}

func localDiffMatches(ctx context.Context, runner core.Commander, root, expected string) bool {
	if runner == nil || expected == "" {
		return false
	}
	result, err := runner.Run(ctx, &core.Command{Dir: root, Argv: []string{"git", "diff", "HEAD"}})
	if err != nil || result.ExitCode != 0 || result.StdoutTruncated {
		return false
	}
	sum := sha256.Sum256([]byte(result.Stdout))
	return hex.EncodeToString(sum[:]) == expected
}
