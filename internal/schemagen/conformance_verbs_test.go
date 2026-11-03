package schemagen

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	adoptcore "github.com/stainless-code/verdict/internal/core/adopt"
	calibratecore "github.com/stainless-code/verdict/internal/core/calibrate"
	checktreecore "github.com/stainless-code/verdict/internal/core/checktree"
	doctorcore "github.com/stainless-code/verdict/internal/core/doctor"
	readycore "github.com/stainless-code/verdict/internal/core/ready"
	trendcore "github.com/stainless-code/verdict/internal/core/trend"
	triagecore "github.com/stainless-code/verdict/internal/core/triage"
	"github.com/stainless-code/verdict/internal/jq"
)

// fixedNow is the conformance clock: one deterministic, valid RFC 3339
// instant so the envelope's date-time format is exercised for real.
func fixedNow() time.Time { return time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC) }

// fixedRuntime builds the hermetic service runtime.
func fixedRuntime() *core.Runtime {
	return &core.Runtime{Now: fixedNow}
}

// TestRuntimeTriageEnvelopeValidates proves the triage response envelope
// validates against the generated envelope schema and carries the typed
// data profile byte-identically.
func TestRuntimeTriageEnvelopeValidates(t *testing.T) {
	doc := loadSchemaDocument(t, "envelope.schema.json")
	dir := t.TempDir()
	t.Chdir(dir)
	writeFixture(t, ".review/4251/findings.json", "["+fixtureFinding()+"]")
	req := &triagecore.Request{
		Sig:       "c111",
		Status:    "addressed",
		Rationale: "verified the note now carries the missing guard clause here",
	}
	req.ReadRequest = core.ReadRequest{Runtime: fixedRuntime(), Repo: "acme/app", Pr: "4251"}
	resp, ref := triagecore.Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("triage refusal: %v", ref)
	}
	data := resp.Data.Wire()
	assertEnvelope(t, doc, "triage", "4251", resp.At, resp.Next, resp.Warnings, data)
}

// TestRuntimeAdoptEnvelopeValidates proves the adopt response envelope
// validates against the generated envelope schema and reports the typed
// handoff counts.
func TestRuntimeAdoptEnvelopeValidates(t *testing.T) {
	doc := loadSchemaDocument(t, "envelope.schema.json")
	dir := t.TempDir()
	t.Chdir(dir)
	writeFixture(t, ".review/4311/PR.json",
		`{"state":"OPEN","headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",`+
			`"baseRefOid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`)
	writeFixture(t, ".review/4311/full.diff", fixtureDiff())
	writeFixture(t, ".review/local-adopt1/findings.json", "["+fixtureFinding()+"]")
	req := &adoptcore.Request{From: "adopt1"}
	req.ReadRequest = core.ReadRequest{Runtime: fixedRuntime(), Repo: "acme/app", Pr: "4311"}
	resp, ref := adoptcore.Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("adopt refusal: %v", ref)
	}
	data := resp.Data.Wire()
	assertEnvelope(t, doc, "adopt", "4311", resp.At, resp.Next, resp.Warnings, data)
}

// assertEnvelope validates one rendered envelope against the generated
// schema and round-trips it through the modeled shape.
func assertEnvelope(t *testing.T, doc map[string]any, verb, pr, at, next string, warnings []string, data any) {
	t.Helper()
	env := core.RenderEnvelope(verb, "acme/app", pr, at, next, warnings, data, nil)
	validateWire(t, doc, "$", doc, decodeWire(t, env))

	var modeled Envelope
	unmarshalWire(t, env, &modeled)
	if modeled.Verb != verb || modeled.Repo != "acme/app" {
		t.Errorf("envelope identity = %+v", modeled)
	}
	if modeled.Error != nil {
		t.Errorf("success envelope error = %+v", modeled.Error)
	}
	if modeled.Data == nil || !reflect.DeepEqual(*modeled.Data, wireMap(t, data)) {
		t.Errorf("envelope data = %v, want %v", modeled.Data, wireMap(t, data))
	}
}

// wireMap decodes an ordered wire value into a plain map with number
// fidelity.
func wireMap(t *testing.T, wire any) map[string]any {
	t.Helper()
	var out map[string]any
	unmarshalWire(t, wire, &out)
	return out
}

// fixtureDiff is the patch both adopt fixtures share.
func fixtureDiff() string {
	return "diff --git a/notes.txt b/notes.txt\n" +
		"index 1111111..2222222 100644\n" +
		"--- a/notes.txt\n" +
		"+++ b/notes.txt\n" +
		"@@ -1,3 +1,3 @@\n" +
		" top\n" +
		"-old line\n" +
		"+new line\n" +
		" bottom\n"
}

// TestCostRecordRoundTrips proves a bundle's cost.json round-trips through
// the core CostRecord shape field for field and validates against the
// generated cost schema.
//
//nolint:revive,cyclop // one assertion block per modeled field group
func TestCostRecordRoundTrips(t *testing.T) {
	doc := loadSchemaDocument(t, "cost.schema.json")
	raw := []byte(`{"schema_version":"1","effort":"standard","models":[` +
		`{"name":"m","input_tokens":100,"output_tokens":50,` +
		`"cache_read_input_tokens":10,"cache_creation_input_tokens":5}],` +
		`"est_usd":0.05,"wall_seconds":12.5,"diff_bytes":2048,"lenses":["correctness"]}`)
	ordered, err := jq.DecodeOrdered(raw)
	if err != nil {
		t.Fatalf("decode cost.json: %v", err)
	}
	validateWire(t, doc, "$defs.record", defOf(t, doc, "record"), decodeWire(t, ordered))

	var modeled CostRecord
	unmarshalWire(t, ordered, &modeled)
	if modeled.SchemaVersion != "1" || modeled.Effort != "standard" {
		t.Errorf("identity = %+v", modeled)
	}
	if len(modeled.Models) != 1 || modeled.Models[0].Name != "m" {
		t.Fatalf("models = %+v", modeled.Models)
	}
	if modeled.Models[0].InputTokens != 100 || modeled.Models[0].OutputTokens != 50 {
		t.Errorf("tokens = %+v", modeled.Models[0])
	}
	if got := modeled.Models[0].CacheReadInputTokens; got == nil || *got != 10 {
		t.Errorf("cache read = %v", got)
	}
	if modeled.EstUSD == nil || *modeled.EstUSD != 0.05 {
		t.Errorf("est_usd = %v", modeled.EstUSD)
	}
	if got, want := wireJSON(t, core.Wire(&modeled)), wireJSON(t, ordered); got != want {
		t.Errorf("cost round-trip differs:\n got %s\nwant %s", got, want)
	}
}

// scriptCommander is a canned core.Commander for the read-verb conformance
// runs: argv selects the canned result, so the live reads stay hermetic.
type scriptCommander struct {
	run func(cmd core.Command) (core.Result, error)
}

// Run implements core.Commander.
func (s scriptCommander) Run(_ context.Context, cmd core.Command) (core.Result, error) {
	return s.run(cmd)
}

// errUnexpectedCall refuses an argv the fixture did not script.
func errUnexpectedCall(argv []string) error {
	return &core.RefusalError{Code: "stub", Message: "unexpected call: " + strings.Join(argv, " ")}
}

// readyStub answers the three live reads deterministically: an open,
// mergeable PR with a passing check and no unresolved threads.
func readyStub(cmd core.Command) (core.Result, error) {
	argv := strings.Join(cmd.Argv, " ")
	switch {
	case strings.Contains(argv, "pr view"):
		return core.Result{Stdout: `{"isDraft":false,"mergeable":"MERGEABLE",` +
			`"mergeStateStatus":"CLEAN","body":"A description long enough to clear the thin gate with room to spare here.",` +
			`"state":"OPEN","headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`}, nil
	case strings.Contains(argv, "pr checks"):
		return core.Result{Stdout: `[{"bucket":"pass","name":"ci / test","state":"SUCCESS"}]`}, nil
	case strings.Contains(argv, "graphql"):
		return core.Result{Stdout: `{"data":{"repository":{"pullRequest":{` +
			`"reviewThreads":{"nodes":[{"isResolved":true}]}}}}}`}, nil
	}
	return core.Result{}, errUnexpectedCall(cmd.Argv)
}

// assertServiceEnvelope validates one rendered envelope against the
// generated schema and round-trips it through the modeled shape, with an
// explicit repo for the repo-less verbs (doctor, check-tree).
func assertServiceEnvelope(t *testing.T, doc map[string]any, verb, repo, pr, at, next string,
	warnings []string, data any,
) {
	t.Helper()
	env := core.RenderEnvelope(verb, repo, pr, at, next, warnings, data, nil)
	validateWire(t, doc, "$", doc, decodeWire(t, env))

	var modeled Envelope
	unmarshalWire(t, env, &modeled)
	if modeled.Verb != verb || modeled.Repo != repo {
		t.Errorf("envelope identity = %+v", modeled)
	}
	if modeled.Error != nil {
		t.Errorf("success envelope error = %+v", modeled.Error)
	}
	if modeled.Data == nil || !reflect.DeepEqual(*modeled.Data, wireMap(t, data)) {
		t.Errorf("envelope data = %v, want %v", modeled.Data, wireMap(t, data))
	}
}

// TestRuntimeReadyEnvelopeValidates proves the ready response envelope
// validates against the generated envelope schema and reports the typed
// advisory: reviewable with no blockers.
func TestRuntimeReadyEnvelopeValidates(t *testing.T) {
	doc := loadSchemaDocument(t, "envelope.schema.json")
	dir := t.TempDir()
	t.Chdir(dir)
	writeFixture(t, ".review/5101/PR.json",
		`{"state":"OPEN","headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",`+
			`"baseRefOid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`)
	writeFixture(t, ".review/5101/findings.json", "["+fixtureFinding()+"]")
	req := &readycore.Request{Check: true}
	req.ReadRequest = core.ReadRequest{
		Runtime: &core.Runtime{Now: fixedNow, Commander: scriptCommander{run: readyStub}},
		Repo:    "acme/app",
		Pr:      "5101",
	}
	resp, ref := readycore.Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("ready refusal: %v", ref)
	}
	if !resp.Ready || resp.GateFailed {
		t.Errorf("ready = %+v, gate failed", resp)
	}
	if len(resp.Data.Blockers) != 0 || resp.Data.Recommendation != "review" {
		t.Errorf("advisory = %+v", resp.Data)
	}
	data := resp.Data.Wire()
	assertServiceEnvelope(t, doc, "ready", "acme/app", "5101", resp.At, resp.Next, resp.Warnings, data)
}

// TestRuntimeTrendEnvelopeValidates proves the trend response envelope
// validates against the generated envelope schema and reports the typed
// single-bundle rollup.
func TestRuntimeTrendEnvelopeValidates(t *testing.T) {
	doc := loadSchemaDocument(t, "envelope.schema.json")
	dir := t.TempDir()
	t.Chdir(dir)
	writeFixture(t, ".review/5101/PR.json",
		`{"state":"OPEN","headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",`+
			`"baseRefOid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`)
	writeFixture(t, ".review/5101/findings.json", "["+fixtureFinding()+"]")
	writeFixture(t, ".review/5101/post.json",
		`{"schema_version":"1","review":{"id":99},`+
			`"head":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","posted_at":"2026-01-01T00:00:00Z",`+
			`"confidence":null,"security_summary":null,`+
			`"tool_version":"v1","prompts_hash":"h1","model":"m"}`)
	req := &trendcore.Request{}
	req.ReadRequest = core.ReadRequest{Runtime: fixedRuntime(), Repo: "acme/app"}
	resp, ref := trendcore.Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("trend refusal: %v", ref)
	}
	data := resp.Data.Wire()
	assertServiceEnvelope(t, doc, "trend", "acme/app", "", resp.At, resp.Next, resp.Warnings, data)
	var modeled Envelope
	unmarshalWire(t, core.RenderEnvelope("trend", "acme/app", "", resp.At, resp.Next,
		resp.Warnings, data, nil), &modeled)
	if got := (*modeled.Data)["bundles"]; got != json.Number("1") {
		t.Errorf("bundles = %v, want 1", got)
	}
	if got := (*modeled.Data)["generated_at"]; got != resp.At {
		t.Errorf("generated_at = %v, want %v", got, resp.At)
	}
}

// calibrateLiveStub answers the live join deterministically: resolved
// threads matching the posted body and a current live head (no compare).
func calibrateLiveStub(cmd core.Command) (core.Result, error) {
	argv := strings.Join(cmd.Argv, " ")
	switch {
	case strings.Contains(argv, "graphql"):
		return core.Result{Stdout: `{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":` +
			`[{"isResolved":true,"comments":{"nodes":[` +
			`{"body":"Fixed body here with enough substance.","path":"notes.txt"}]}}]}}}}}`}, nil
	case strings.Contains(argv, "pr view"):
		return core.Result{Stdout: `{"headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",` +
			`"state":"OPEN","mergedAt":null}`}, nil
	}
	return core.Result{}, errUnexpectedCall(cmd.Argv)
}

// TestRuntimeCalibrateLiveEnvelopeValidates proves the calibrate live-join
// response envelope validates against the generated envelope schema and
// reports the typed resolved-unfixed outcome.
func TestRuntimeCalibrateLiveEnvelopeValidates(t *testing.T) {
	doc := loadSchemaDocument(t, "envelope.schema.json")
	dir := t.TempDir()
	t.Chdir(dir)
	writeFixture(t, ".review/5101/PR.json",
		`{"state":"OPEN","headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",`+
			`"baseRefOid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`)
	writeFixture(t, ".review/5101/findings.json", "["+calibrateLiveFinding()+"]")
	writeFixture(t, ".review/5101/post.json",
		`{"schema_version":"1","review":{"id":424242},`+
			`"head":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","posted_at":"2026-01-01T00:00:00Z",`+
			`"confidence":null,"security_summary":null,`+
			`"tool_version":"t","prompts_hash":"h","model":"m"}`)
	req := &calibratecore.Request{}
	req.ReadRequest = core.ReadRequest{
		Runtime: &core.Runtime{Now: fixedNow, Commander: scriptCommander{run: calibrateLiveStub}},
		Repo:    "acme/app",
		Pr:      "5101",
	}
	resp, ref := calibratecore.Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("calibrate refusal: %v", ref)
	}
	if resp.Next != "calibration recorded" {
		t.Errorf("next = %q", resp.Next)
	}
	data := resp.Data.Wire()
	assertServiceEnvelope(t, doc, "calibrate", "acme/app", "5101", resp.At, resp.Next, resp.Warnings, data)
}

// calibrateLiveFinding renders the settled join fixture: an open inline
// finding whose posted body matches the resolved live thread.
func calibrateLiveFinding() string {
	return `{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,` +
		`"anchored":true,"body":"Fixed body here with enough substance.","suggestion":null,` +
		`"inBounds":true,"rigor":"walked","severity":"critical",` +
		`"sig":"c111111111111111111111111111111111111111","sig_version":"1",` +
		`"firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"",` +
		`"history":[],"kb":["r-a1"]}`
}

// TestRuntimeCalibrateProbeEnvelopeValidates proves the calibrate probe
// response envelope validates against the generated envelope schema and
// reports the typed grade counts.
func TestRuntimeCalibrateProbeEnvelopeValidates(t *testing.T) {
	doc := loadSchemaDocument(t, "envelope.schema.json")
	dir := t.TempDir()
	t.Chdir(dir)
	writeFixture(t, ".review/5101/PR.json",
		`{"state":"OPEN","headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",`+
			`"baseRefOid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`)
	writeFixture(t, ".review/5101/findings.json", "["+
		calibrateProbeFinding("addressed", "real")+", "+
		calibrateProbeFinding("false-positive", "false")+", "+
		calibrateProbeFinding("open", "unknown")+"]")
	req := &calibratecore.Request{Probe: true}
	req.ReadRequest = core.ReadRequest{Runtime: fixedRuntime(), Repo: "acme/app", Pr: "5101"}
	resp, ref := calibratecore.Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("calibrate refusal: %v", ref)
	}
	if resp.Next != "adopted-pair signal present — feed X3 into the H2 evidence gate" {
		t.Errorf("next = %q", resp.Next)
	}
	data := resp.Data.Wire()
	assertServiceEnvelope(t, doc, "calibrate", "acme/app", "5101", resp.At, resp.Next, resp.Warnings, data)
}

// calibrateProbeFinding renders one probe finding with the triage baseline
// status and a kb cite.
func calibrateProbeFinding(status, _ string) string {
	return `{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,` +
		`"anchored":true,"body":"Usable body here with enough substance.","suggestion":null,` +
		`"inBounds":true,"rigor":"walked","severity":"critical",` +
		`"sig":"c111111111111111111111111111111111111111","sig_version":"1",` +
		`"firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"` + status + `","rationale":"",` +
		`"history":[],"kb":["r-a1"]}`
}

// TestRuntimeDoctorEnvelopeValidates proves the doctor response envelope
// validates against the generated envelope schema. Row values stay
// environment-shaped (PATH lookups), so only the envelope identity and
// the data shape pin: the readparity goldens pin the rows byte-exactly.
func TestRuntimeDoctorEnvelopeValidates(t *testing.T) {
	doc := loadSchemaDocument(t, "envelope.schema.json")
	dir := t.TempDir()
	t.Chdir(dir)
	req := &doctorcore.Request{}
	req.ReadRequest = core.ReadRequest{Runtime: &core.Runtime{
		Now: fixedNow,
		Commander: scriptCommander{run: func(cmd core.Command) (core.Result, error) {
			return core.Result{}, errUnexpectedCall(cmd.Argv)
		}},
	}}
	resp, ref := doctorcore.Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("doctor refusal: %v", ref)
	}
	data := resp.Data.Wire()
	assertServiceEnvelope(t, doc, "doctor", "", "", resp.At, resp.Next, resp.Warnings, data)
}

// cleanTreeStub answers `git status --porcelain` with a clean tree.
func cleanTreeStub(cmd core.Command) (core.Result, error) {
	if len(cmd.Argv) == 3 && cmd.Argv[0] == "git" {
		return core.Result{Stdout: ""}, nil
	}
	return core.Result{}, errUnexpectedCall(cmd.Argv)
}

// TestRuntimeCheckTreeEnvelopeValidates proves the check-tree response
// envelope validates against the generated envelope schema and reports
// the typed clean gate.
func TestRuntimeCheckTreeEnvelopeValidates(t *testing.T) {
	doc := loadSchemaDocument(t, "envelope.schema.json")
	dir := t.TempDir()
	t.Chdir(dir)
	req := &checktreecore.Request{}
	req.ReadRequest = core.ReadRequest{
		Runtime: &core.Runtime{Now: fixedNow, Commander: scriptCommander{run: cleanTreeStub}},
	}
	resp, ref := checktreecore.Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("check-tree refusal: %v", ref)
	}
	if resp.Exit != 0 || resp.Next != "check-tree: clean" {
		t.Errorf("gate = %+v", resp)
	}
	data := resp.Data.Wire()
	assertServiceEnvelope(t, doc, "check-tree", "", "", resp.At, resp.Next, resp.Warnings, data)
}
