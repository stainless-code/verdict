package readcommon

import (
	"context"
	"time"
)

// RepoRootWithBudget runs the root probe under a caller-chosen budget. The
// shipped budget stays 5 s, which is shorter than the cancellation fixture's
// load-sensitive shell startup, so the fixture needs a budget that cannot
// decide its outcome.
func RepoRootWithBudget(ctx context.Context, budget time.Duration) (string, error) {
	return repoRoot(ctx, budget)
}
