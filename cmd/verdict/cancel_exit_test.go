package main

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/calibrate"
	"github.com/stainless-code/verdict/internal/checktree"
	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/doctor"
	"github.com/stainless-code/verdict/internal/readcommon"
	"github.com/stainless-code/verdict/internal/ready"
	"github.com/stainless-code/verdict/internal/report"
	"github.com/stainless-code/verdict/internal/threads"
	"github.com/stainless-code/verdict/internal/trend"
)

// cancelAfterGuard models the shutdown window GO-CLI-05 covers: the first Err
// read reports a live context (the adapter's cheap pre-check), every read after
// it reports canceled (the service's own entry check), which is where a SIGINT
// arriving between the two lands. Done stays open until that flip, so the
// double satisfies the context contract at every read. Field order keeps the GC
// scan short (govet fieldalignment), not reading order.
type cancelAfterGuard struct {
	done  chan struct{}
	reads int
	mu    sync.Mutex
}

func newCancelAfterGuard() *cancelAfterGuard {
	return &cancelAfterGuard{done: make(chan struct{})}
}

func (g *cancelAfterGuard) Deadline() (time.Time, bool) { return time.Time{}, false }

func (g *cancelAfterGuard) Done() <-chan struct{} { return g.done }

func (g *cancelAfterGuard) Value(_ any) any { return nil }

func (g *cancelAfterGuard) Err() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.reads++
	if g.reads == 1 {
		return nil
	}
	select {
	case <-g.done:
	default:
		close(g.done)
	}
	return context.Canceled
}

func (g *cancelAfterGuard) readCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.reads
}

const canceledCode = "canceled"

var noClock = func() time.Time { return time.Unix(0, 0).UTC() }

var cancellationVerbs = []struct {
	run  readHandler
	name string
}{
	{name: "ready", run: ready.Run},
	{name: "trend", run: trend.Run},
	{name: "threads", run: threads.Run},
	{name: "calibrate", run: calibrate.Run},
	{name: "check-tree", run: checktree.Run},
	{name: "doctor", run: doctor.Run},
	{name: "report", run: report.Run},
	{name: "next", run: report.RunNext},
}

// TestReadVerbCancelInsideServiceExitsBroke pins GO-CLI-05 for a cancel that
// lands after an adapter's pre-check, inside the service call: the verb exits
// 3 with the `canceled` refusal on its envelope, not 1.
func TestReadVerbCancelInsideServiceExitsBroke(t *testing.T) {
	t.Parallel()
	for _, tc := range cancellationVerbs {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkCanceledVerb(t, tc.name, tc.run)
		})
	}
}

func checkCanceledVerb(t *testing.T, verb string, run readHandler) {
	t.Helper()
	code, env, reads := runCanceledVerb(t, verb, run)
	if code != cli.ExitBroke {
		t.Errorf("exit = %d; want %d for a canceled verb", code, cli.ExitBroke)
	}
	if env.Error == nil || env.Error.Code != canceledCode {
		t.Fatalf("envelope error = %+v; want code %q", env.Error, canceledCode)
	}
	if reads < 2 {
		t.Errorf("context read %d time(s); the cancel never reached the service", reads)
	}
}

// TestNextHumanStoreErrorCancelExitsBroke pins the human half of next's
// store-error path: a cancel exits 3 there too, where no envelope carries the
// code.
func TestNextHumanStoreErrorCancelExitsBroke(t *testing.T) {
	t.Parallel()
	var out, diag bytes.Buffer
	r := &cli.Runner{Out: &out, Human: &out, Diag: &diag, Now: noClock, Cmd: "next"}
	guard := newCancelAfterGuard()
	code := report.RunNext(guard, r, nil, readcommon.Request{
		Runtime: &core.Runtime{},
		Repo:    "acme/app",
		Pr:      "7",
	})
	if code != cli.ExitBroke {
		t.Errorf("exit = %d; want %d for a canceled verb", code, cli.ExitBroke)
	}
	if out.Len() != 0 {
		t.Errorf("stdout = %q; a refusal stays off stdout", out.String())
	}
	if got := diag.String(); got != "next: cannot read store (run post or record first)\n" {
		t.Errorf("stderr = %q; want the frozen store-error line", diag.String())
	}
}

// envelopeRefusal is the envelope's error object.
type envelopeRefusal struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type refusedEnvelope struct {
	Error *envelopeRefusal `json:"error"`
}

func runCanceledVerb(t *testing.T, verb string, run readHandler) (int, refusedEnvelope, int) {
	t.Helper()
	var out, diag bytes.Buffer
	r := &cli.Runner{
		Out: &out, Human: &out, Diag: &diag, Now: noClock,
		Cmd: verb, JSONMode: true,
	}
	guard := newCancelAfterGuard()
	code := run(guard, r, nil, readcommon.Request{
		Runtime: &core.Runtime{},
		Repo:    "acme/app",
		Pr:      "7",
	})
	var env refusedEnvelope
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("stdout is not one envelope: %v\n%s", err, out.String())
	}
	return code, env, guard.readCount()
}
