//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package exec

import (
	"context"
	"errors"
	"strings"
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

// TestOSCommanderCapsCombinedOutput proves a child that writes past the
// combined cap is truncated to the documented tail, with the marker.
func TestOSCommanderCapsCombinedOutput(t *testing.T) {
	tctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	const limit = 4096
	res, err := OSCommander{}.Run(tctx, core.Command{
		Argv:      []string{"bash", "-c", "yes | head -c 4194304"},
		OutputCap: limit,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.StdoutTruncated {
		t.Fatal("StdoutTruncated = false, want true")
	}
	if len(res.Stdout) > limit+len(truncationNotice(captureTail, limit)) {
		t.Errorf("Stdout = %d bytes, want <= cap plus the marker", len(res.Stdout))
	}
	if !strings.Contains(res.Stdout, "truncated") {
		t.Errorf("Stdout carries no truncation marker: %q", res.Stdout)
	}
}

// TestOSCommanderNoisyChildStaysBounded pins the OOM vector: `yes` as a
// verifyCmd runs until the watchdog kills it, and the capture holds at most
// the cap instead of growing with the child.
func TestOSCommanderNoisyChildStaysBounded(t *testing.T) {
	tctx, cancel := context.WithTimeout(t.Context(), 1500*time.Millisecond)
	defer cancel()
	const limit = 4096
	res, err := OSCommander{}.Run(tctx, core.Command{Argv: []string{"yes"}, OutputCap: limit})
	if err != nil {
		t.Fatal(err)
	}
	if !res.TimedOut {
		t.Error("TimedOut = false, want true (the watchdog budget fired)")
	}
	if !res.StdoutTruncated {
		t.Fatal("StdoutTruncated = false, want true")
	}
	if len(res.Stdout) > limit+len(truncationNotice(captureTail, limit)) {
		t.Errorf("Stdout = %d bytes, want <= cap plus the marker", len(res.Stdout))
	}
}

// TestSpawnCommanderCapsStdout proves an over-cap read refuses instead of
// returning a truncated document as success.
func TestSpawnCommanderCapsStdout(t *testing.T) {
	tctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	const limit = 32
	res, err := SpawnCommander{}.Run(tctx, core.Command{
		Argv:      []string{"bash", "-c", "head -c 4096 /dev/zero"},
		OutputCap: limit,
	})
	var capErr *core.OutputCappedError
	if !errors.As(err, &capErr) || capErr.Stream != "stdout" {
		t.Fatalf("err = %v, want OutputCappedError for stdout", err)
	}
	if !res.StdoutTruncated {
		t.Error("StdoutTruncated = false, want true")
	}
	if len(res.Stdout) > limit+len(truncationNotice(captureHead, limit)) {
		t.Errorf("Stdout = %d bytes, want <= cap plus the marker", len(res.Stdout))
	}
}

// TestSplitCommanderCapsStdout proves fetch's runner refuses an over-cap
// stdout rather than handing a truncated diff to the bundle writer.
func TestSplitCommanderCapsStdout(t *testing.T) {
	tctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	const limit = 32
	res, err := SplitCommander{}.Run(tctx, core.Command{
		Argv:      []string{"bash", "-c", "head -c 4096 /dev/zero"},
		OutputCap: limit,
	})
	var capErr *core.OutputCappedError
	if !errors.As(err, &capErr) || capErr.Stream != "stdout" {
		t.Fatalf("err = %v, want OutputCappedError for stdout", err)
	}
	if !res.StdoutTruncated {
		t.Error("StdoutTruncated = false, want true")
	}
	if len(res.Stdout) > limit+len(truncationNotice(captureHead, limit)) {
		t.Errorf("Stdout = %d bytes, want <= cap plus the marker", len(res.Stdout))
	}
}

// TestSplitCommanderKeepsTwentyMiBDiff proves the fetch default leaves the
// documented 20 MiB diff intact, so the existing cap/chunk decision still
// sees the whole payload.
func TestSplitCommanderKeepsTwentyMiBDiff(t *testing.T) {
	const diffBytes = 20 << 20
	tctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	res, err := SplitCommander{}.Run(tctx, core.Command{
		Argv: []string{"bash", "-c", "head -c 20971520 /dev/zero"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.StdoutTruncated {
		t.Fatal("StdoutTruncated = true, want false at 20 MiB")
	}
	if len(res.Stdout) != diffBytes {
		t.Errorf("Stdout = %d bytes, want %d", len(res.Stdout), diffBytes)
	}
}
