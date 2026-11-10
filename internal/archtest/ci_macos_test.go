package archtest

// Repository guard for the macOS runner policy: GitHub caps concurrent macOS
// jobs per org, so the macOS legs of `go`, `evals` and `installers` run only on
// a main push, the nightly schedule and `workflow_dispatch`. A macOS leg that
// lost its event gate would put the queue back in front of every pull request,
// and one that vanished would drop macOS coverage in silence; the step-parity
// check keeps the copied macOS lane repeating the bar of the lane it twins. On
// a push or a manual dispatch those legs are advisory, because GitHub cancels
// legs it never assigned a runner; the nightly keeps them required.

import (
	"maps"
	"slices"
	"strings"
	"testing"
)

// macOSCondition is the one event gate a macOS lane may carry.
const macOSCondition = "    if: needs.changes.outputs.full == 'true' && github.event_name != 'pull_request'"

// macOSTwin maps each macOS lane to the lane whose bar it repeats.
var macOSTwin = map[string]string{
	"installers-macos": "installers",
	"go-macos":         "go",
	"evals-macos":      "evals",
}

// macOSStepOmissions are the base-lane steps a macOS twin cannot repeat, with
// why. Bubblewrap is Linux-only, so the verify sandbox install, namespace
// sysctl, namespace probe, and containment summary stay on the Linux eval
// lane; the same case still runs its refusal leg on macOS.
var macOSStepOmissions = map[string][]string{
	"evals": {
		"Install bubblewrap",
		"Allow unprivileged user namespaces (bubblewrap)",
		"Prove bubblewrap can create its namespaces",
		"Prove the verify sandbox containment case ran",
	},
}

// TestCIMacOSLanesSkipPullRequests fails when a macOS leg would run on a pull
// request, when one of the legs is gone, or when another lane takes a macOS
// runner.
func TestCIMacOSLanesSkipPullRequests(t *testing.T) {
	jobs := workflowJobs(t, "ci.yml", readCIWorkflow(t))
	checkMacOSLanes(t, jobs)
	checkNoOtherLaneTakesMacOS(t, jobs)
}

// checkMacOSLanes pins each leg's event gate and its runner.
func checkMacOSLanes(t *testing.T, jobs map[string][]string) {
	t.Helper()
	for _, lane := range slices.Sorted(maps.Keys(macOSTwin)) {
		block, ok := jobs[lane]
		if !ok {
			t.Errorf("ci.yml has no %s job: macOS coverage would vanish", lane)
			continue
		}
		joined := strings.Join(block, "\n")
		if !strings.Contains(joined, macOSCondition) {
			t.Errorf("%s lost %q: its runner would queue on every pull request",
				lane, strings.TrimSpace(macOSCondition))
		}
		if !strings.Contains(joined, "    runs-on: macos-latest") {
			t.Errorf("%s no longer runs on a macOS runner", lane)
		}
	}
}

// checkNoOtherLaneTakesMacOS fails when another lane picks a macOS runner, the
// shortest way to put the queue back in front of every pull request.
func checkNoOtherLaneTakesMacOS(t *testing.T, jobs map[string][]string) {
	t.Helper()
	for lane, block := range jobs {
		if _, ok := macOSTwin[lane]; ok {
			continue
		}
		if strings.Contains(strings.Join(block, "\n"), "macos-latest") {
			t.Errorf("%s takes a macOS runner: only %v do", lane, slices.Sorted(maps.Keys(macOSTwin)))
		}
	}
}

// TestCINightlySchedule pins the triggers that give the macOS lanes their run
// while pull requests stay off them.
func TestCINightlySchedule(t *testing.T) {
	workflow := readCIWorkflow(t)
	triggers := topLevelBlock(strings.Split(workflow, "\n"), "on")
	joined := strings.Join(triggers, "\n")
	for _, want := range []string{"  schedule:\n    - cron: \"17 3 * * *\"", "  workflow_dispatch:"} {
		if !strings.Contains(workflow, want) {
			t.Errorf("ci.yml triggers lost %q: the macOS legs would run on main pushes alone",
				strings.TrimSpace(want))
		}
	}
	if !strings.Contains(joined, "pull_request:") {
		t.Error("ci.yml no longer runs on pull_request: the merge bar lost its trigger")
	}
}

// TestCIMacOSAdvisoryEvents pins which events tolerate a non-success macOS
// result. A main push or a manual dispatch does, because GitHub cancels a leg
// it never assigned a runner and that must not turn main red; every other
// event, `schedule` above all, must keep the lane a merge blocker so a real
// macOS break still fails the nightly.
func TestCIMacOSAdvisoryEvents(t *testing.T) {
	workflow := readCIWorkflow(t)
	gate := parseCIGate(t, workflow)
	if slices.Contains(gate.advisoryEvents, "schedule") {
		t.Fatal("MACOS_ADVISORY_EVENTS lists schedule: a real macOS break would no longer fail the nightly")
	}
	want := []string{"push", "workflow_dispatch"}
	if !slices.Equal(gate.advisoryEvents, want) {
		t.Fatalf("MACOS_ADVISORY_EVENTS = %v; want %v (macOS required everywhere else)",
			gate.advisoryEvents, want)
	}
	for _, lane := range gate.macOS {
		if !slices.Contains(gate.required, lane) {
			t.Errorf("%s dropped out of REQUIRED_JOBS: the nightly would stop gating it", lane)
		}
	}
	const advisoryArm = `elif ($m | index($k)) and ($me | index($ev)) and $res != "" then empty`
	if !strings.Contains(workflow, advisoryArm) {
		t.Errorf("ci-complete lost %q: the advisory events would stop being read", advisoryArm)
	}
}

// TestCIMacOSLaneStepsMatch pins each macOS lane to the step names of the lane
// it repeats, so a gate added on one side cannot leave the other behind.
func TestCIMacOSLaneStepsMatch(t *testing.T) {
	workflow := readCIWorkflow(t)
	jobs := workflowJobs(t, "ci.yml", workflow)
	for _, macLane := range slices.Sorted(maps.Keys(macOSTwin)) {
		checkMacOSLaneSteps(t, macLane, macOSTwin[macLane], jobs)
	}
}

// checkMacOSLaneSteps compares one twin's step names with its base lane,
// after dropping the steps bubblewrap makes Linux-only.
func checkMacOSLaneSteps(t *testing.T, macLane, baseLane string, jobs map[string][]string) {
	t.Helper()
	base, got := stepNames(jobs[baseLane]), stepNames(jobs[macLane])
	if len(base) == 0 {
		t.Errorf("%s declares no named step", baseLane)
		return
	}
	omit := macOSStepOmissions[baseLane]
	checkOmittedSteps(t, macLane, base, omit)
	want := withoutSteps(base, omit)
	if !slices.Equal(want, got) {
		t.Errorf("%s steps drifted from %s:\n %s: %v\n %s: %v",
			macLane, baseLane, baseLane, want, macLane, got)
	}
}

// checkOmittedSteps fails an omission the base lane no longer declares.
func checkOmittedSteps(t *testing.T, macLane string, base, omit []string) {
	t.Helper()
	for _, name := range omit {
		if !slices.Contains(base, name) {
			t.Errorf("%s omits stale step %q: the base lane no longer carries it", macLane, name)
		}
	}
}

// withoutSteps drops the lane-specific step names from a base lane.
func withoutSteps(have, drop []string) []string {
	kept := make([]string, 0, len(have))
	for _, name := range have {
		if !slices.Contains(drop, name) {
			kept = append(kept, name)
		}
	}
	return kept
}

// stepNames lists a job's step names in order.
func stepNames(block []string) []string {
	var out []string
	for _, line := range block {
		if name, ok := strings.CutPrefix(line, "      - name: "); ok {
			out = append(out, name)
		}
	}
	return out
}
