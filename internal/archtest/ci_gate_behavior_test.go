package archtest

// Behavioural guard for the ci-complete gate: the string pins next door prove
// the declaration is wired, this table proves what the shipped program does
// with it. The cases that matter are the non-success results — a cancelled
// macOS leg on a main push, a broken macOS leg on the nightly, a skipped leg
// on a pull request — because each one is a policy someone will be tempted to
// widen.

import (
	"encoding/json"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

const ciGateStep = "Required lanes succeeded, advisory lanes reported"

// gateCase is one synthetic run: a needs map, the two event inputs, and the
// verdict the gate must return for them.
type gateCase struct {
	name     string
	event    string
	full     string
	base     string            // result for every declared lane; "" means success
	results  map[string]string // per-lane result, overriding base
	silent   []string          // lanes whose needs entry carries no result at all
	wantPass bool
	wantLog  []string
}

// TestCICompleteGateBehavior fails when the gate's verdict on a non-success
// result changes: a macOS leg cancelled on a push passes, the same leg failing
// on the nightly fails, and no other lane inherits that tolerance.
func TestCICompleteGateBehavior(t *testing.T) {
	workflow := readCIWorkflow(t)
	gate := parseCIGate(t, workflow)
	script := gateStepScript(t, workflow)
	sh := posixShell(t)
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not installed: the ci-complete gate is a jq program")
	}
	for _, tc := range gateCases() {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.CommandContext(t.Context(), sh, "-c", script)
			cmd.Env = append(os.Environ(), gateEnv(t, gate, tc)...)
			out, err := cmd.CombinedOutput()
			if passed := err == nil; passed != tc.wantPass {
				t.Fatalf("gate passed = %v, want %v\n%s", passed, tc.wantPass, out)
			}
			for _, want := range tc.wantLog {
				if !strings.Contains(string(out), want) {
					t.Errorf("gate output lost %q:\n%s", want, out)
				}
			}
		})
	}
}

// gateCases is the policy table: every row is an event, a needs map, and the
// verdict that event must produce.
func gateCases() []gateCase {
	cancelled := map[string]string{
		"installers-macos": "cancelled",
		"go-macos":         "cancelled",
		"evals-macos":      "cancelled",
	}
	skipped := map[string]string{
		"installers-macos": "skipped",
		"go-macos":         "skipped",
		"evals-macos":      "skipped",
	}
	return []gateCase{
		{
			name: "push with every lane green", event: "push", full: "true",
			wantPass: true,
			wantLog: []string{
				"required go-macos: success (macOS leg, a non-success result does not block on push)",
			},
		},
		{
			name: "push with the macOS legs cancelled", event: "push", full: "true",
			results: cancelled, wantPass: true,
			wantLog: []string{"required go-macos: cancelled (macOS leg"},
		},
		{
			name: "push with a macOS leg failed", event: "push", full: "true",
			results: map[string]string{"go-macos": "failure"}, wantPass: true,
			wantLog: []string{"required go-macos: failure (macOS leg"},
		},
		{
			name:  "workflow_dispatch with a macOS leg cancelled",
			event: "workflow_dispatch", full: "true",
			results: cancelled, wantPass: true,
		},
		{
			name: "push with a macOS leg reporting nothing", event: "push", full: "true",
			silent:   []string{"go-macos"},
			wantPass: false,
			wantLog:  []string{"required but nothing: go-macos"},
		},
		{
			name: "push with a Linux lane cancelled", event: "push", full: "true",
			results:  map[string]string{"go": "cancelled"},
			wantPass: false,
			wantLog:  []string{"required but cancelled: go"},
		},
		{
			name: "push with the advisory lane silent", event: "push", full: "true",
			silent:   []string{"fuzz"},
			wantPass: false,
			wantLog:  []string{"advisory but silent: fuzz"},
		},
		{
			name: "nightly with a macOS leg failed", event: "schedule", full: "true",
			results:  map[string]string{"evals-macos": "failure"},
			wantPass: false,
			wantLog:  []string{"required but failure: evals-macos"},
		},
		{
			name: "nightly with a macOS leg cancelled", event: "schedule", full: "true",
			results: cancelled, wantPass: false,
			wantLog: []string{"required but cancelled: "},
		},
		{
			name: "nightly with every lane green", event: "schedule", full: "true",
			wantPass: true,
			wantLog:  []string{"required go-macos: success"},
		},
		{
			name:  "pull request with the macOS legs skipped",
			event: "pull_request", full: "true",
			results: skipped, wantPass: true,
			wantLog: []string{
				"required go-macos: skipped (macOS leg, not scheduled on a pull request)",
			},
		},
		{
			name:  "pull request with a macOS leg failed",
			event: "pull_request", full: "true",
			results:  map[string]string{"go-macos": "failure"},
			wantPass: false,
			wantLog:  []string{"required but failure: go-macos"},
		},
		{
			name:  "pull request on the docs-only fast path",
			event: "pull_request", full: "false", base: "skipped",
			results: map[string]string{
				"changes": "success", "format": "success", "docs": "success", "audit": "success",
			},
			wantPass: true,
		},
	}
}

// gateStepScript returns the gate step's run block, dedented, so the test runs
// the program the workflow ships instead of a copy of it.
func gateStepScript(t *testing.T, workflow string) string {
	t.Helper()
	lines := strings.Split(workflow, "\n")
	start := slices.Index(lines, "      - name: "+ciGateStep)
	if start < 0 {
		t.Fatalf("ci-complete has no %q step", ciGateStep)
	}
	run := slices.Index(lines[start:], "        run: |")
	if run < 0 {
		t.Fatalf("the %q step has no run block", ciGateStep)
	}
	var body []string
	for _, line := range lines[start+run+1:] {
		if line != "" && !strings.HasPrefix(line, "          ") {
			break
		}
		body = append(body, strings.TrimPrefix(line, "          "))
	}
	if strings.TrimSpace(strings.Join(body, "\n")) == "" {
		t.Fatalf("the %q step has an empty run block", ciGateStep)
	}
	return strings.Join(body, "\n")
}

// gateEnv renders the step's environment: the declaration read from the
// workflow, the event inputs from the case, and a needs map covering every
// declared lane.
func gateEnv(t *testing.T, gate ciGate, tc gateCase) []string {
	t.Helper()
	base := tc.base
	if base == "" {
		base = "success"
	}
	needs := make(map[string]map[string]string, len(gate.needs))
	for _, lane := range gate.needs {
		result, ok := tc.results[lane]
		if !ok {
			result = base
		}
		if slices.Contains(tc.silent, lane) {
			needs[lane] = map[string]string{}
			continue
		}
		needs[lane] = map[string]string{"result": result}
	}
	raw, err := json.Marshal(needs)
	if err != nil {
		t.Fatalf("render needs: %v", err)
	}
	return []string{
		"NEEDS=" + string(raw),
		"FULL=" + tc.full,
		"EVENT=" + tc.event,
		"REQUIRED_JOBS=" + strings.Join(gate.required, " "),
		"MACOS_JOBS=" + strings.Join(gate.macOS, " "),
		"MACOS_ADVISORY_EVENTS=" + strings.Join(gate.advisoryEvents, " "),
		"ADVISORY_JOBS=" + strings.Join(gate.advisory, " "),
	}
}
