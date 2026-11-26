package run

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// phaseBarrierBound is how long one half of a pair waits for the other to enter
// before the commander reports that the coordinator serialized them. It only
// elapses on a serialized coordinator, and failing there beats hanging the
// suite's own timeout.
const phaseBarrierBound = 10 * time.Second

// barrierCommander makes each phase pair overlap on purpose and lets the test
// choose which half finishes first. Each half announces that it is inside the
// launch, waits for its partner to be inside too, and the delayed half also
// waits for the partner's launch to return, so the two workers' writes and
// binds land in either order.
type barrierCommander struct {
	*phaseCommander
	delay    map[string]string
	entered  map[string]chan struct{}
	finished map[string]chan struct{}
	mu       sync.Mutex
}

func (b *barrierCommander) Run(ctx context.Context, cmd *core.Command) (core.Result, error) {
	phase, ok := phaseOf(cmd)
	if !ok {
		return b.phaseCommander.Run(ctx, cmd)
	}
	partner := phasePartner(phase)
	if partner != "" {
		b.signal(b.entered, phase)
		b.await(b.entered, partner, phase)
	}
	if waitFor := b.delay[phase]; waitFor != "" {
		b.await(b.finished, waitFor, phase)
	}
	result, err := b.phaseCommander.Run(ctx, cmd)
	b.signal(b.finished, phase)
	return result, err
}

// signal closes one phase's gate exactly once, whoever gets there first.
func (b *barrierCommander) signal(gates map[string]chan struct{}, phase string) {
	b.mu.Lock()
	gate, ok := gates[phase]
	if !ok {
		gate = make(chan struct{})
		gates[phase] = gate
	}
	b.mu.Unlock()
	select {
	case <-gate:
	default:
		close(gate)
	}
}

// await blocks until the named gate is closed, failing the test instead of
// hanging when the coordinator never runs the two phases at once.
func (b *barrierCommander) await(gates map[string]chan struct{}, name, phase string) {
	b.mu.Lock()
	gate, ok := gates[name]
	if !ok {
		gate = make(chan struct{})
		gates[name] = gate
	}
	b.mu.Unlock()
	select {
	case <-gate:
	case <-time.After(phaseBarrierBound):
		b.t.Errorf("phase %s waited %v for %s: the coordinator serialized the pair", phase, phaseBarrierBound, name)
	}
}

// phasePartner names the phase a launch must overlap with: the other finder,
// or the other cross check.
func phasePartner(phase string) string {
	switch phase {
	case "x-finder-1":
		return "x-finder-2"
	case "x-finder-2":
		return "x-finder-1"
	case "x-cross-1":
		return "x-cross-2"
	case "x-cross-2":
		return "x-cross-1"
	}
	return ""
}

// concurrentArtifacts is what one concurrent execution must produce, reduced to
// the documents whose order a fan-in decides.
type concurrentArtifacts struct {
	findings  string
	checks    string
	results   string
	failures  string
	artifacts []string
}

// boundArtifacts is every artifact both finders and both cross checks bind into
// the launcher record. The digests move with the bundle's absolute paths and
// launch timings; the set is the part a fan-in order must never change.
var boundArtifacts = []string{
	"PR.json", "context.txt", "controls.json", "full.diff", "hunks.tsv", "procedure.md", "review.json",
	"roles.txt", "run.json", "source.json", "toc.txt", "verification/plan.json",
	"x-cross-1/.run.out", "x-cross-1/.run.rounds/01.check.json", "x-cross-1/.run.rounds/01.review.json",
	"x-cross-1/launcher.json", "x-cross-1/request.json", "x-cross-1/verdicts.json",
	"x-cross-2/.run.out", "x-cross-2/.run.rounds/01.check.json", "x-cross-2/.run.rounds/01.review.json",
	"x-cross-2/launcher.json", "x-cross-2/request.json", "x-cross-2/verdicts.json",
	"x-finder-1/launcher.json", "x-finder-1/procedure.md", "x-finder-1/review.json",
	"x-finder-2/launcher.json", "x-finder-2/procedure.md", "x-finder-2/review.json",
}

func TestCoordinatorRunsFindersAndCrossConcurrently(t *testing.T) {
	t.Parallel()
	var first concurrentArtifacts
	for i, order := range []struct {
		delay map[string]string
		name  string
	}{
		{name: "finder-2 and cross-2 finish first", delay: map[string]string{
			"x-finder-1": "x-finder-2", "x-cross-1": "x-cross-2",
		}},
		{name: "finder-1 and cross-1 finish first", delay: map[string]string{
			"x-finder-2": "x-finder-1", "x-cross-2": "x-cross-1",
		}},
	} {
		t.Run(order.name, func(t *testing.T) {
			assertConcurrentOrder(t, i, order.delay, &first)
		})
	}
}

// assertConcurrentOrder runs one finish order and compares it with the first
// order's documents: the second execution must produce the very same ones.
func assertConcurrentOrder(t *testing.T, index int, delay map[string]string, first *concurrentArtifacts) {
	t.Helper()
	got, warnings := runConcurrentExecution(t, delay)
	if index == 0 {
		*first = got
	} else if !sameConcurrent(first, &got) {
		t.Fatalf("artifacts depend on the finish order:\n first=%+v\nsecond=%+v", *first, got)
	}
	if !slices.Equal(got.artifacts, boundArtifacts) {
		t.Fatalf("bound artifacts = %q, want %q", got.artifacts, boundArtifacts)
	}
	assertReplayedWarnings(t, warnings)
}

// runConcurrentExecution runs one two-finder execution whose phases overlap by
// construction, and returns the documents the fan-in decides plus the warnings
// the run reported.
func runConcurrentExecution(t *testing.T, delay map[string]string) (concurrentArtifacts, []string) {
	t.Helper()
	req, base := coordinatorFixture(t)
	base.distinct = true
	base.outputDir = true
	commander := &barrierCommander{
		phaseCommander: base, delay: delay,
		entered: map[string]chan struct{}{}, finished: map[string]chan struct{}{},
	}
	req.Commander = commander
	response, ref := TwoFinders(t.Context(), req)
	if ref != nil {
		t.Fatal(ref)
	}
	return captureConcurrentArtifacts(t, base.dir), response.Warnings
}

// captureConcurrentArtifacts reduces one execution to the documents a fan-in
// order would move: the aggregate review, the plan's checks, the cross
// verdicts, and the manifest's artifact digests and failures.
func captureConcurrentArtifacts(t *testing.T, dir string) concurrentArtifacts {
	t.Helper()
	review := readPhaseObject(t, filepath.Join(dir, "review.json"))
	plan := readPhaseObject(t, filepath.Join(dir, "verification", "plan.json"))
	verdicts := readPhaseObject(t, filepath.Join(dir, "verification", "verdicts.json"))
	manifest := readPhaseObject(t, filepath.Join(dir, "launcher.json"))
	return concurrentArtifacts{
		findings:  compactRows(jq.Elements(readcommon.FieldOf(review, "findings"))),
		checks:    compactRows(jq.Elements(readcommon.FieldOf(plan, "checks"))),
		results:   compactRows(jq.Elements(readcommon.FieldOf(verdicts, "results"))),
		failures:  jq.Compact(readcommon.FieldOf(manifest, "failures")),
		artifacts: artifactKeys(t, manifest),
	}
}

// sameConcurrent reports whether two executions produced the same documents.
func sameConcurrent(a, b *concurrentArtifacts) bool {
	return a.findings == b.findings && a.checks == b.checks && a.results == b.results &&
		a.failures == b.failures && slices.Equal(a.artifacts, b.artifacts)
}

// artifactKeys lists the artifact paths the launcher record binds, sorted so
// the comparison never depends on the map's iteration order. The record keys
// them with the platform separator, so the collected paths are normalized to
// slashes before they meet the literals below.
func artifactKeys(t *testing.T, manifest *jq.Obj) []string {
	t.Helper()
	artifacts, ok := readcommon.FieldOf(manifest, "artifacts").(*jq.Obj)
	if !ok {
		t.Fatal("manifest artifacts is not an object")
	}
	keys := make([]string, 0, len(artifacts.Fields))
	for key := range artifacts.Fields {
		keys = append(keys, filepath.ToSlash(key))
	}
	slices.Sort(keys)
	return keys
}

// assertReplayedWarnings proves the two phases' buffered warnings came back in
// index order, whatever order their launches finished in: each phase's own
// `.run.out` is a directory, so each reports itself.
func assertReplayedWarnings(t *testing.T, warnings []string) {
	t.Helper()
	if len(warnings) != 2 {
		t.Fatalf("warnings = %q, want one per finder", warnings)
	}
	if !strings.Contains(warnings[0], filepath.Join("x-finder-1", runOutputFile)) {
		t.Fatalf("warnings[0] = %q, want the first finder's", warnings[0])
	}
	if !strings.Contains(warnings[1], filepath.Join("x-finder-2", runOutputFile)) {
		t.Fatalf("warnings[1] = %q, want the second finder's", warnings[1])
	}
}
