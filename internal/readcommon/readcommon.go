// Package readcommon carries the shared read-verb core (report, next, show,
// ready, trend, calibrate, doctor, check-tree): bundle resolution, the
// findings-store load with its entry-shape guard, the gh subprocess runner
// with byte-exact argv, jq-faithful value helpers, finding ranking, and the
// cost rollup. Every helper preserves the frozen CLI bytes.
package readcommon

import (
	"github.com/stainless-code/verdict/internal/core"
)

// Request is the typed read-verb request owned by core: positionals, the
// injected runtime (env/clock/progress), and the global flag forces.
type Request = core.ReadRequest
