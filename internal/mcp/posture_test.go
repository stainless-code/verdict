package mcp

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// constrainedWriters are the two tools the read-only contract keeps served in
// a narrowed form: record limited to dry_run, report without its file outputs.
var constrainedWriters = []string{"record", "report"}

// defaultWriters lists every tool the default surface exposes that writes
// local state or posts to a host.
func defaultWriters() []string {
	var out []string
	for _, tl := range allTools(Posture(false)) {
		if tl.mutates != noLocalWrites {
			out = append(out, tl.name)
		}
	}
	return out
}

// TestReadOnlyBlocksEveryWritePath proves --read-only is the one knob for
// local writes on this surface: every writer the default surface exposes is
// absent under --read-only, except the two constrained forms the contract
// names.
//
//nolint:revive // the writer matrix reads as one assertion block
func TestReadOnlyBlocksEveryWritePath(t *testing.T) {
	t.Parallel()
	served := map[string]*tool{}
	for _, tl := range allTools(Posture(true)) {
		served[tl.name] = tl
	}
	writers := defaultWriters()
	for _, name := range []string{"adopt", "fetch", "fix", "post", "threads", "triage", "verify"} {
		if !slices.Contains(writers, name) {
			t.Errorf("%s is not classed as a writer, so --read-only would not check it", name)
		}
	}
	for _, name := range writers {
		_, ok := served[name]
		if want := slices.Contains(constrainedWriters, name); ok != want {
			t.Errorf("%s served under --read-only = %v, want %v", name, ok, want)
		}
	}
	if !served["record"].requireDryRun {
		t.Error("read-only record does not require dry_run")
	}
	for _, arg := range []string{"output", "sarif"} {
		if !served["report"].refusedArgs[arg] {
			t.Errorf("read-only report does not refuse %q", arg)
		}
	}
}

// TestReadOnlyRefusesEveryWriterCall drives each hidden writer through a real
// tools/call and expects the typed read-only-tool refusal, never a protocol
// error and never a run.
//
//nolint:revive // the per-writer refusal reads as one assertion block
func TestReadOnlyRefusesEveryWriterCall(t *testing.T) {
	t.Parallel()
	cfg := testConfig()
	cfg.ReadOnly = true
	for _, name := range defaultWriters() {
		if slices.Contains(constrainedWriters, name) {
			continue
		}
		frames := serve(t, &cfg,
			`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"`+name+`","arguments":{"repo":"a/b","pr":"1"}}}`)
		if frames["1"].Error != nil {
			t.Errorf("%s under --read-only = protocol error %+v, want a typed refusal", name, frames["1"].Error)
			continue
		}
		var result callToolResult
		if err := json.Unmarshal(frames["1"].Result, &result); err != nil {
			t.Fatal(err)
		}
		if !result.IsError || !strings.Contains(string(result.StructuredContent), `"read-only-tool"`) {
			t.Errorf("%s under --read-only = %s, want a read-only-tool refusal", name, result.StructuredContent)
		}
	}
}
