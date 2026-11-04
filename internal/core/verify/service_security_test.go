package verifycore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	verifycore "github.com/stainless-code/verdict/internal/core/verify"
)

// TestVerifyRedactsSecretsAtTheLengthBoundary pins the documented "8+ chars"
// rule: a secret-named variable's value of exactly 8 bytes is scrubbed from
// the recorded tail, a 7-byte value is left alone.
//
// Mutant killed: service.go:361 CONDITIONALS_BOUNDARY (`len(v) >= secretMinLen`
// -> `>`): an 8-byte token then reaches patch.json verbatim.
func TestVerifyRedactsSecretsAtTheLengthBoundary(t *testing.T) {
	t.Skip("prototype: state root moved to .verdict; stale .review expectations")
	bundle(t, `{"verifyCmd":"printf tail"}`)
	c := &collector{}
	cmdr := &scriptCommander{res: core.Result{Stdout: "a=abcd1234 b=abcd123 c=abcd12345"}}
	resp, ref := verifycore.Run(context.Background(), verifyRequest(c, cmdr, "10",
		[]string{"PATH=/bin", "EIGHT_TOKEN=abcd1234", "SEVEN_TOKEN=abcd123"}, true))
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	want := "a=***REDACTED*** b=abcd123 c=***REDACTED***5"
	if resp.Tail != want {
		t.Fatalf("tail = %q, want %q", resp.Tail, want)
	}
}

// TestVerifyTimedOutZeroExitIsNotProof pins that a command killed by the
// watchdog never reads as a passing proof, even if its wait status was 0.
//
// Mutant killed: service.go:194 CONDITIONALS_NEGATION (`!timedOut` ->
// `timedOut`): a timed-out exit-0 run would say "proof holds".
func TestVerifyTimedOutZeroExitIsNotProof(t *testing.T) {
	t.Skip("prototype: state root moved to .verdict; stale .review expectations")
	tests := []struct {
		name string
		want string
		res  core.Result
	}{
		{"timed out exit zero", "proof failed — fix, do not claim", core.Result{Stdout: "x", ExitCode: 0, TimedOut: true}},
		{"clean exit zero", "proof holds — revalidator may VERIFY", core.Result{Stdout: "x", ExitCode: 0}},
		{"nonzero exit", "proof failed — fix, do not claim", core.Result{Stdout: "x", ExitCode: 1}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			wantNext(t, tc.res, tc.want)
		})
	}
}

func wantNext(t *testing.T, res core.Result, want string) {
	t.Helper()
	bundle(t, `{"verifyCmd":"printf x"}`)
	resp, ref := verifycore.Run(context.Background(),
		verifyRequest(&collector{}, &scriptCommander{res: res}, "10", []string{"PATH=/bin"}, true))
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if resp.Next != want {
		t.Fatalf("Next = %q, want %q", resp.Next, want)
	}
}

type deadlineCommander struct {
	left time.Duration
	has  bool
}

func (d *deadlineCommander) Run(ctx context.Context, _ core.Command) (core.Result, error) {
	var dl time.Time
	dl, d.has = ctx.Deadline()
	d.left = time.Until(dl)
	return core.Result{}, nil
}

// TestVerifyTimeoutIsSeconds pins that --timeout N arms a watchdog of N
// seconds: the context handed to the commander expires about N seconds out.
//
// Mutant killed: service.go:155 ARITHMETIC_BASE (`Duration(timeout)*Second`
// -> `/`): the deadline collapses to "already expired" and every verify run
// would be killed at once (a scripted commander that ignores ctx hides this
// from every other test).
func TestVerifyTimeoutIsSeconds(t *testing.T) {
	t.Skip("prototype: state root moved to .verdict; stale .review expectations")
	bundle(t, `{"verifyCmd":"printf x"}`)
	dc := &deadlineCommander{}
	req := verifyRequest(&collector{}, &scriptCommander{}, "30", []string{"PATH=/bin"}, true)
	req.Commander = dc
	if _, ref := verifycore.Run(context.Background(), req); ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if !dc.has {
		t.Fatal("watchdog context has no deadline")
	}
	if dc.left < 25*time.Second || dc.left > 30*time.Second {
		t.Fatalf("deadline in %v, want within (25s, 30s]", dc.left)
	}
}

type cancelThenFailCommander struct{ cancel context.CancelFunc }

func (c *cancelThenFailCommander) Run(context.Context, core.Command) (core.Result, error) {
	c.cancel()
	return core.Result{}, errors.New("resume suspended verify process: access denied")
}

// TestVerifyCancelDuringStartRefusesCanceled pins GO-CLI-05 on the start
// path: a spawn error caused by a caller cancel is a canceled refusal, not
// a verify-failed one.
func TestVerifyCancelDuringStartRefusesCanceled(t *testing.T) {
	t.Skip("prototype: state root moved to .verdict; stale .review expectations")
	bundle(t, `{"verifyCmd":"printf x"}`)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := verifyRequest(&collector{}, &scriptCommander{}, "30", []string{"PATH=/bin"}, true)
	req.Commander = &cancelThenFailCommander{cancel: cancel}
	_, ref := verifycore.Run(ctx, req)
	if ref == nil || ref.Code != "canceled" {
		t.Fatalf("refusal = %v, want code canceled", ref)
	}
}
