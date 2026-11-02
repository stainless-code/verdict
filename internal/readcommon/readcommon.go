// Package readcommon carries the shared read-verb core for the pure-read
// port (report, next, show, ready, trend, calibrate, doctor, check-tree):
// bundle resolution, the findings-store load with its R1 entry guard, the
// gh subprocess runner with byte-exact argv, jq-faithful value helpers,
// finding ranking, and the cost rollup. Every helper preserves the frozen
// CLI bytes; divergences from old bash crash corners are
// documented at the call site.
package readcommon

import (
	"github.com/stainless-code/verdict/internal/cli"
)

// Request carries the resolved read-verb invocation: positionals, the
// checkout root for provenance reads, the process environment snapshot, and
// the global flag forces for render gating.
type Request struct {
	Env    map[string]string
	Forces *cli.GlobalForces
	Repo   string
	Pr     string
	Root   string
}

// ForcesOrEmpty returns the global forces, defaulting to an empty set so
// direct callers (next's sub-report, tests) need no main wiring.
func (q Request) ForcesOrEmpty() *cli.GlobalForces {
	if q.Forces == nil {
		return &cli.GlobalForces{}
	}
	return q.Forces
}

// ReviewModel reads the REVIEW_MODEL knob for attribution substitution.
func (q Request) ReviewModel() string {
	return q.Env["REVIEW_MODEL"]
}
