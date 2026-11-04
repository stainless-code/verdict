package controls

import (
	"context"
	"errors"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// stderrCap keeps a git failure message short enough to read inline.
const stderrCap = 300

// runGit runs one git subcommand through the seam. A failure carries the
// first 300 bytes of stderr, never a bare exit status.
func runGit(ctx context.Context, cmd core.Commander, outputCap int, args ...string) (string, error) {
	res, err := cmd.Run(ctx, core.Command{Argv: append([]string{"git"}, args...), OutputCap: outputCap})
	if err == nil && res.ExitCode != 0 {
		err = res.ExitFailure("git")
	}
	if err == nil && res.StdoutTruncated {
		err = errors.New("output over its cap")
	}
	if err != nil {
		stderr := strings.TrimSpace(res.Stderr)
		return "", jq.Sealed(stderr[:min(len(stderr), stderrCap)], err)
	}
	return res.Stdout, nil
}
