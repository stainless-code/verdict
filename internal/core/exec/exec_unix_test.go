//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package exec

import (
	"context"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
)

// TestOSCommanderRun proves the production commander contract: argv runs,
// combined streams come back in Stdout, the wait code maps, and a clean run
// is not a timeout.
func TestOSCommanderRun(t *testing.T) {
	tctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	res, err := OSCommander{}.Run(tctx, core.Command{
		Argv: []string{"bash", "-c", `printf out; printf err >&2`},
		Env:  []string{"PATH=/bin:/usr/bin"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Stdout != "outerr" || res.Stderr != "" {
		t.Errorf("result = %+v, want combined outerr", res)
	}
	if res.ExitCode != 0 || res.TimedOut || res.Decoy {
		t.Errorf("result = %+v, want clean exit 0", res)
	}
}

// TestOSCommanderExitCode proves nonzero exits come back as results, not
// errors.
func TestOSCommanderExitCode(t *testing.T) {
	tctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	res, err := OSCommander{}.Run(tctx, core.Command{Argv: []string{"bash", "-c", `exit 3`}})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 3 || res.TimedOut {
		t.Errorf("result = %+v, want exit 3", res)
	}
}
