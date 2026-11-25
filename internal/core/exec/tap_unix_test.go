//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package exec

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
)

// lockedBuffer is a tap the test reads after the run.
type lockedBuffer struct {
	buf bytes.Buffer
	mu  sync.Mutex
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// TestWatchdogTapsStdoutAlone proves the tap reads the child's stdout whole —
// past the combined capture's cap — and never its stderr, while the capture
// keeps its own bounded, merged tail.
func TestWatchdogTapsStdoutAlone(t *testing.T) {
	tctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	tap := &lockedBuffer{}
	res, err := Watchdog{}.Run(tctx, &core.Command{
		Argv:      []string{"bash", "-c", "echo first; echo noise >&2; yes line | head -n 400000; echo last"},
		OutputCap: 4096,
		StdoutTap: tap,
	})
	if err != nil {
		t.Fatal(err)
	}
	got := tap.buf.String()
	if !res.StdoutTapped || !res.StdoutTruncated {
		t.Errorf("tapped=%v truncated=%v, want a tapped run past the capture cap", res.StdoutTapped, res.StdoutTruncated)
	}
	if !strings.HasPrefix(got, "first\n") || !strings.HasSuffix(got, "last\n") || strings.Count(got, "line\n") != 400000 {
		t.Errorf("tap holds %d bytes, want every stdout line from first to last", len(got))
	}
	if strings.Contains(got, "noise") {
		t.Error("stderr reached the stdout tap")
	}
}

// TestWatchdogStartFailureIsTyped proves a launch whose process never
// existed is the typed start error, the one failure that proves no spend.
func TestWatchdogStartFailureIsTyped(t *testing.T) {
	_, err := Watchdog{}.Run(t.Context(), &core.Command{Argv: []string{"/nonexistent/verdict-no-such-agent"}})
	if _, ok := errors.AsType[*core.StartError](err); !ok {
		t.Fatalf("err = %v, want a StartError", err)
	}
}
