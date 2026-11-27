package core

import (
	"context"
	"testing"
)

// plainWrapper forwards to another commander and says nothing about
// simulation, as a production wrapper written tomorrow would.
type plainWrapper struct{ inner Commander }

func (w plainWrapper) Run(ctx context.Context, cmd *Command) (Result, error) {
	return w.inner.Run(ctx, cmd)
}

// double is a test double that says it simulates processes, or not.
type double struct{ simulated bool }

func (double) Run(context.Context, *Command) (Result, error) { return Result{}, nil }

func (d double) SimulatesProcesses() bool { return d.simulated }

// TestStartsHostProcessesFailsClosed proves the sandbox decision fails
// closed: a commander that says nothing — a wrapper, even around a test
// double — starts host processes; only a double answering true is
// simulated; and nil starts nothing.
func TestStartsHostProcessesFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		cmdr Commander
		name string
		want bool
	}{
		{name: "silent wrapper around a double", cmdr: plainWrapper{inner: double{simulated: true}}, want: true},
		{name: "double answering false", cmdr: double{}, want: true},
		{name: "simulated double", cmdr: double{simulated: true}, want: false},
		{name: "nil", cmdr: nil, want: false},
	} {
		if got := StartsHostProcesses(tc.cmdr); got != tc.want {
			t.Errorf("%s: StartsHostProcesses = %v, want %v", tc.name, got, tc.want)
		}
	}
}
