package mcp

import (
	"bytes"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// TestConcurrentWarningsShareTheDiagWriter drives concurrent tool calls whose
// service warnings all land on the session's single stderr destination. Under
// -race it fails unless diagnostic writes are serialized at the transport
// boundary, and the byte assertions prove the same lock keeps lines whole: the
// concurrent stream is compared, line for line, against the same calls served
// one at a time, so no torn, interleaved, duplicated or lost warning passes.
func TestConcurrentWarningsShareTheDiagWriter(t *testing.T) {
	fixture(t)
	const calls = 24
	lines := make([]string, 0, calls)
	for id := range calls {
		lines = append(lines, concurrentReport(id))
	}
	want := sequentialWarningLines(t, lines)
	stderr := &bytes.Buffer{}
	cfg := testConfig()
	cfg.Stderr = stderr
	frames := serve(t, &cfg, lines...)
	if len(frames) != calls {
		t.Fatalf("got %d response frames, want %d", len(frames), calls)
	}
	for id := range calls {
		if _, ok := frames[strconv.Itoa(id)]; !ok {
			t.Fatalf("missing response frame for id %d", id)
		}
	}
	got := warningLines(t, stderr.String())
	if !slices.Equal(got, want) {
		t.Fatalf("concurrent stderr = %q, want the same whole lines as the sequential run %q", got, want)
	}
}

// sequentialWarningLines serves each call on its own server and returns every
// diagnostic line it wrote, sorted: a race-free reference for the concurrent
// run, with no expectation derived from the run under test.
func sequentialWarningLines(t *testing.T, calls []string) []string {
	t.Helper()
	var all []string
	for _, call := range calls {
		stderr := &bytes.Buffer{}
		cfg := testConfig()
		cfg.Stderr = stderr
		serve(t, &cfg, call)
		all = append(all, warningLines(t, stderr.String())...)
	}
	if len(all) == 0 {
		t.Fatal("no call wrote a warning; the test would not exercise the shared writer")
	}
	slices.Sort(all)
	return all
}

// warningLines splits one diagnostic stream into its non-empty lines, sorted.
func warningLines(t *testing.T, raw string) []string {
	t.Helper()
	var out []string
	for line := range strings.SplitSeq(raw, "\n") {
		if line != "" {
			out = append(out, line)
		}
	}
	slices.Sort(out)
	return out
}
