package schemagen

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	adoptcore "github.com/stainless-code/verdict/internal/core/adopt"
	calibratecore "github.com/stainless-code/verdict/internal/core/calibrate"
	checktreecore "github.com/stainless-code/verdict/internal/core/checktree"
	doctorcore "github.com/stainless-code/verdict/internal/core/doctor"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	indexcore "github.com/stainless-code/verdict/internal/core/index"
	packcore "github.com/stainless-code/verdict/internal/core/pack"
	"github.com/stainless-code/verdict/internal/core/query"
	readycore "github.com/stainless-code/verdict/internal/core/ready"
	trendcore "github.com/stainless-code/verdict/internal/core/trend"
	triagecore "github.com/stainless-code/verdict/internal/core/triage"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/packtest"
	"github.com/stainless-code/verdict/internal/readcommon"
	"github.com/stainless-code/verdict/internal/testenv"
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
	writeFixture(t, ".verdict/state/4251/findings.json", "["+fixtureFinding()+"]")
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
	assertWireFields(t, data, map[string]any{
		"sig":    "c111111111111111111111111111111111111111",
		"status": "addressed",
	})
	assertEnvelope(t, doc, "triage", "4251", resp.At, resp.Next, resp.Warnings, data)
}

// TestRuntimeAdoptEnvelopeValidates proves the adopt response envelope
// validates against the generated envelope schema and reports the typed
// handoff counts.
func TestRuntimeAdoptEnvelopeValidates(t *testing.T) {
	doc := loadSchemaDocument(t, "envelope.schema.json")
	dir := t.TempDir()
	t.Chdir(dir)
	writeFixture(t, ".verdict/state/4311/PR.json",
		`{"state":"OPEN","headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",`+
			`"baseRefOid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`)
	writeFixture(t, ".verdict/state/4311/full.diff", fixtureDiff())
	writeFixture(t, ".verdict/state/local-adopt1/findings.json", "["+fixtureFinding()+"]")
	req := &adoptcore.Request{From: "adopt1"}
	req.ReadRequest = core.ReadRequest{Runtime: fixedRuntime(), Repo: "acme/app", Pr: "4311"}
	resp, ref := adoptcore.Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("adopt refusal: %v", ref)
	}
	data := resp.Data.Wire()
	assertWireFields(t, data, map[string]any{
		"dir":     ".verdict/state/4311",
		"from":    "adopt1",
		"adopted": json.Number("1"),
		"open":    json.Number("1"),
	})
	assertEnvelope(t, doc, "adopt", "4311", resp.At, resp.Next, resp.Warnings, data)
}

// TestRuntimePackTestEnvelopeValidates validates the pack test data against
// the generated data and envelope schemas.
func TestRuntimePackTestEnvelopeValidates(t *testing.T) {
	doc := loadSchemaDocument(t, "envelope.schema.json")
	files := fstest.MapFS{
		"pack.json":                {Data: []byte(`{"schema_version":"1","name":"acme/test","version":"1.0.0","severity":{"s":"important"},"lenses":[{"id":"test"}],"rules":[{"id":"rule-a","severity":"s","globs":["src/*.go"]}]}`)},
		"lenses/test.md":           {Data: []byte("lens body\n")},
		"rules/rule-a.md":          {Data: []byte("rule body\n")},
		"evals/case/diff.patch":    {Data: []byte("diff --git a/src/a.go b/src/a.go\n--- a/src/a.go\n+++ b/src/a.go\n@@ -1 +1 @@\n-old\n+new\n")},
		"evals/case/findings.json": {Data: []byte(`[{"path":"src/a.go","line":1,"body":"This finding points to the changed line.","rigor":"walked","kb":["rule-a"]}]`)},
		"evals/case/expect.json":   {Data: []byte(`{"rules":["rule-a"]}`)},
	}
	p, ref := packcore.Load(files, ".")
	if ref != nil {
		t.Fatal(ref)
	}
	set, ref := packcore.Merge([]packcore.Origin{{Layer: packcore.LayerBuiltin, Source: "fixture", Pack: p}})
	if ref != nil {
		t.Fatal(ref)
	}
	resp, ref := packtest.Run(context.Background(), p, set, "2026-11-02T12:00:00Z")
	if ref != nil {
		t.Fatal(ref)
	}
	wire := core.Wire(resp.Data)
	defs, ok := doc["$defs"].(map[string]any)
	if !ok {
		t.Fatal("schema $defs is not an object")
	}
	validateWire(t, doc, "pack test data", defs["packTestData"], decodeWire(t, wire))
	envelope := core.RenderEnvelope("pack", "", "", resp.At, resp.Next, nil, wire, nil)
	validateWire(t, doc, "$", doc, decodeWire(t, envelope))
}

// assertEnvelope validates one rendered envelope against the generated
// schema and round-trips it through the modeled shape.
func assertEnvelope(t *testing.T, doc map[string]any, verb, pr, at, next string, warnings []string, data any) {
	t.Helper()
	env := core.RenderEnvelope(verb, "acme/app", pr, at, next, warnings, data, nil)
	validateWire(t, doc, "$", doc, decodeWire(t, env))
	validateDataProfile(t, doc, verb, data)

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

// validateDataProfile validates one verb's data block against its pinned
// profile when the generated schema publishes one, so the runtime verbs pin
// their published shape instead of only the envelope around it.
func validateDataProfile(t *testing.T, doc map[string]any, verb string, data any) {
	t.Helper()
	defs, ok := doc["$defs"].(map[string]any)
	if !ok || data == nil {
		return
	}
	def, ok := defs[dataDefName(verb)]
	if !ok {
		return
	}
	validateWire(t, doc, verb+" data", def, decodeWire(t, data))
}

// wireMap decodes an ordered wire value into a plain map with number
// fidelity.
func wireMap(t *testing.T, wire any) map[string]any {
	t.Helper()
	var out map[string]any
	unmarshalWire(t, wire, &out)
	return out
}

// assertWireFields asserts top-level wire data fields against literals.
func assertWireFields(t *testing.T, data any, want map[string]any) {
	t.Helper()
	got := wireMap(t, data)
	for key, wantVal := range want {
		if !reflect.DeepEqual(got[key], wantVal) {
			t.Errorf("data.%s = %#v, want %#v", key, got[key], wantVal)
		}
	}
}

// wireSubMap returns one nested object from the wire data.
func wireSubMap(t *testing.T, data any, key string) map[string]any {
	t.Helper()
	sub, ok := wireMap(t, data)[key].(map[string]any)
	if !ok {
		t.Fatalf("data.%s is not an object", key)
	}
	return sub
}

// wireList returns one array from the wire data.
func wireList(t *testing.T, data any, key string) []any {
	t.Helper()
	list, ok := wireMap(t, data)[key].([]any)
	if !ok {
		t.Fatalf("data.%s is not an array", key)
	}
	return list
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

// TestRuntimeIndexEnvelopeValidates proves the index response envelope
// validates against the generated schema for every query kind and that each
// kind labels itself: a status profile never masquerades as build.
func TestRuntimeIndexEnvelopeValidates(t *testing.T) {
	doc := loadSchemaDocument(t, "envelope.schema.json")
	dir, env := indexConformanceRepo(t)
	cases := []struct {
		want   map[string]any
		query  string
		symbol string
		path   string
		paths  []string
	}{
		{query: indexcore.QueryBuild, want: map[string]any{
			"kind": "build", "files_indexed": json.Number("2"), "parsed": json.Number("2"),
		}},
		{query: indexcore.QueryStatus, want: map[string]any{
			"kind": "status", "built": true, "stale": false,
		}},
		{query: indexcore.QueryCallers, symbol: "a.Target", want: map[string]any{
			"kind": "callers", "counts": map[string]any{"callers": json.Number("2"), "unresolved": json.Number("0")},
		}},
		{query: indexcore.QueryPrune, want: map[string]any{
			"kind": "prune", "removed": map[string]any{"trees": json.Number("0"), "shards": json.Number("0")},
		}},
		{query: indexcore.QueryTestsFor, path: "a/a.go", want: map[string]any{
			"kind": "tests-for", "counts": map[string]any{"tests": json.Number("0")},
		}},
		{query: indexcore.QueryCoChanged, path: "a/a.go", want: map[string]any{
			"kind": "co-changed", "counts": map[string]any{
				"partners": json.Number("0"), "target_commits": json.Number("1"),
			},
		}},
		{query: indexcore.QueryOwners, path: "a/a.go", want: map[string]any{
			"kind": "owners", "target": map[string]any{
				"path": "a/a.go", "owners": nil, "owners_source": nil,
			},
		}},
		{query: indexcore.QueryBlastRadius, paths: []string{"a/a.go"}, want: map[string]any{
			"kind": "blast-radius",
			"symbols": map[string]any{
				"count": json.Number("2"), "truncated": json.Number("0"),
				"rows": []any{
					map[string]any{"path": "a/a.go", "name": "Target", "kind": "func", "line": json.Number("3")},
					map[string]any{"path": "a/a.go", "name": "Caller", "kind": "func", "line": json.Number("5")},
				},
			},
			"callers": map[string]any{
				"count": json.Number("2"), "unresolved": json.Number("0"), "truncated": json.Number("0"),
				"rows": []any{
					map[string]any{
						"symbol": "example.com/m/a.Target", "path": "a/a.go", "kind": "call",
						"resolution": "exact", "line": json.Number("6"), "depth": json.Number("1"),
					},
					map[string]any{
						"symbol": "example.com/m/a.Target", "path": "b/b.go", "kind": "call",
						"resolution": "lexical", "line": json.Number("6"), "depth": json.Number("1"),
					},
				},
			},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			req := &indexcore.Request{Query: tc.query, Symbol: tc.symbol, Path: tc.path, Paths: tc.paths}
			req.ReadRequest = core.ReadRequest{
				Runtime:  &core.Runtime{Now: fixedNow, Commander: coreexec.Runner{}, Env: env},
				RepoRoot: dir,
			}
			resp, ref := indexcore.Run(context.Background(), req)
			if ref != nil {
				t.Fatalf("index %s refusal: %v", tc.query, ref)
			}
			data := resp.Data.Wire()
			assertWireFields(t, data, tc.want)
			assertServiceEnvelope(t, doc, "index", "", "", resp.At, resp.Next, resp.Warnings, data)
		})
	}
}

// indexConformanceRepo writes the two-package index fixture as one commit and
// returns the checkout plus the hermetic child environment.
func indexConformanceRepo(t *testing.T) (string, map[string]string) {
	t.Helper()
	dir := t.TempDir()
	writeFixture(t, filepath.Join(dir, "go.mod"), "module example.com/m\n\ngo 1.27\n")
	writeFixture(t, filepath.Join(dir, "a", "a.go"),
		"package a\n\nfunc Target() {}\n\nfunc Caller() {\n\tTarget()\n}\n")
	writeFixture(t, filepath.Join(dir, "b", "b.go"),
		"package b\n\nimport \"example.com/m/a\"\n\nfunc Uses() {\n\ta.Target()\n}\n")
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"add", "-A"},
		{"commit", "-qm", "index conformance"},
	} {
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir
		cmd.Env = testenv.Base(nil)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	env := map[string]string{}
	for _, kv := range testenv.Base(nil) {
		if name, value, ok := strings.Cut(kv, "="); ok {
			env[name] = value
		}
	}
	return dir, env
}

// TestCostRecordNamesAreAWriterGateReadersTolerate pins both halves of the
// cost-record contract in one place, so neither side can drift alone: the
// schema requires a name on every models entry (the writer's gate), and the
// reader still prices a record that omits one, reporting the model as
// unknown rather than the spend as zero and never failing the run (the
// reader's tolerance). It also pins the one accepted models shape: an object
// is not a models list.
func TestCostRecordNamesAreAWriterGateReadersTolerate(t *testing.T) {
	doc := loadSchemaDocument(t, "cost.schema.json")
	spend := defOf(t, doc, "modelSpend")

	nameless := map[string]any{"input_tokens": 100.0, "output_tokens": 50.0}
	if err := validationError(doc, spend, nameless); err == nil {
		t.Error("a models entry without name validated against modelSpend; want the required name to refuse it")
	}
	named := map[string]any{"name": "m", "input_tokens": 100.0, "output_tokens": 50.0}
	if err := validationError(doc, spend, named); err != nil {
		t.Errorf("a named models entry was refused: %v", err)
	}

	record := defOf(t, doc, "record")
	if err := validationError(doc, record, map[string]any{"models": []any{named}}); err != nil {
		t.Errorf("the array form was refused: %v", err)
	}
	objectModels := map[string]any{"models": map[string]any{"m": map[string]any{"input_tokens": 1.0}}}
	if err := validationError(doc, record, objectModels); err == nil {
		t.Error("object-shaped models validated against the record; want the array shape enforced")
	}

	dir := t.TempDir()
	raw := []byte(`{"schema_version":"2","models":[{"input_tokens":100,"output_tokens":50}]}`)
	if err := os.WriteFile(filepath.Join(dir, "cost.json"), raw, 0o600); err != nil {
		t.Fatalf("write cost.json: %v", err)
	}
	res := readcommon.CostRollup(dir)
	if got, _ := res.Cost.Get("total_tokens"); got != 150.0 {
		t.Errorf("total_tokens = %v; want 150 for a nameless entry the reader still prices", got)
	}
	if len(res.Warns) != 0 {
		t.Errorf("warns = %v; want none — the reader tolerates a nameless entry", res.Warns)
	}
}

// TestCostRecordRoundTrips proves a bundle's cost.json round-trips through
// the core CostRecord shape field for field and validates against the
// generated cost schema.
//
//nolint:revive,cyclop // one assertion block per modeled field group
func TestCostRecordRoundTrips(t *testing.T) {
	doc := loadSchemaDocument(t, "cost.schema.json")
	raw := []byte(`{"schema_version":"2","effort":"standard","models":[` +
		`{"name":"m","input_tokens":100,"output_tokens":50,` +
		`"cache_read_input_tokens":10,"cache_creation_input_tokens":5}],` +
		`"est_usd":0.05,"wall_seconds":12.5,"diff_bytes":2048,` +
		`"lenses":[{"group":"group-a","labels":["correctness"]}]}`)
	ordered, err := jq.DecodeOrdered(raw)
	if err != nil {
		t.Fatalf("decode cost.json: %v", err)
	}
	validateWire(t, doc, "$defs.record", defOf(t, doc, "record"), decodeWire(t, ordered))

	var modeled CostRecord
	unmarshalWire(t, ordered, &modeled)
	if modeled.SchemaVersion != "2" || modeled.Effort != "standard" {
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
	run func(cmd *core.Command) (core.Result, error)
}

// Run implements core.Commander.
func (s scriptCommander) Run(_ context.Context, cmd *core.Command) (core.Result, error) {
	return s.run(cmd)
}

// errUnexpectedCall refuses an argv the fixture did not script.
func errUnexpectedCall(argv []string) error {
	return &core.RefusalError{Code: "stub", Message: "unexpected call: " + strings.Join(argv, " ")}
}

// readyStub answers the three live reads deterministically: an open,
// mergeable PR with a passing check and no unresolved threads.
func readyStub(cmd *core.Command) (core.Result, error) {
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
	validateDataProfile(t, doc, verb, data)

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
	writeFixture(t, ".verdict/state/5101/PR.json",
		`{"state":"OPEN","headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",`+
			`"baseRefOid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`)
	writeFixture(t, ".verdict/state/5101/findings.json", "["+fixtureFinding()+"]")
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
	assertWireFields(t, data, map[string]any{
		"ready":            true,
		"recommendation":   "review",
		"state":            "OPEN",
		"draft":            false,
		"mergeable":        "MERGEABLE",
		"mergeStateStatus": "CLEAN",
		"threads_open":     json.Number("0"),
		// 73-char description plus the newline bodyLength appends.
		"description_chars": json.Number("74"),
	})
	ci := wireSubMap(t, data, "ci")
	if ci["failing"] != json.Number("0") || ci["total"] != json.Number("1") {
		t.Errorf("ready ci = %v, want 0 failing of 1", ci)
	}
}

// TestRuntimeTrendEnvelopeValidates proves the trend response envelope
// validates against the generated envelope schema and reports the typed
// single-bundle rollup.
func TestRuntimeTrendEnvelopeValidates(t *testing.T) {
	doc := loadSchemaDocument(t, "envelope.schema.json")
	dir := t.TempDir()
	t.Chdir(dir)
	writeFixture(t, ".verdict/state/5101/PR.json",
		`{"state":"OPEN","headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",`+
			`"baseRefOid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`)
	writeFixture(t, ".verdict/state/5101/findings.json", "["+fixtureFinding()+"]")
	writeFixture(t, ".verdict/state/5101/post.json",
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
	assertWireFields(t, data, map[string]any{
		"bundles":  json.Number("1"),
		"findings": json.Number("1"),
		"open":     json.Number("1"),
	})
	if rows := wireList(t, data, "per_bundle"); len(rows) != 1 {
		t.Errorf("trend per_bundle rows = %d, want 1", len(rows))
	}
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
func calibrateLiveStub(cmd *core.Command) (core.Result, error) {
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
	writeFixture(t, ".verdict/state/5101/PR.json",
		`{"state":"OPEN","headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",`+
			`"baseRefOid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`)
	writeFixture(t, ".verdict/state/5101/findings.json", "["+calibrateLiveFinding()+"]")
	writeFixture(t, ".verdict/state/5101/post.json",
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
	assertWireFields(t, data, map[string]any{"review_id": json.Number("424242")})
	precision := wireSubMap(t, data, "precision")
	if precision["resolved_unfixed"] != json.Number("1") {
		t.Errorf("calibrate precision = %v, want resolved_unfixed=1", precision)
	}
	if outcomes := wireList(t, data, "outcomes"); len(outcomes) != 1 {
		t.Errorf("calibrate outcomes = %d, want 1", len(outcomes))
	}
	head := wireSubMap(t, data, "head")
	if head["reviewed"] != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Errorf("calibrate head = %v", head)
	}
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
	writeFixture(t, ".verdict/state/5101/PR.json",
		`{"state":"OPEN","headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",`+
			`"baseRefOid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`)
	writeFixture(t, ".verdict/state/5101/findings.json", "["+
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
	counts := wireSubMap(t, data, "counts")
	assertWireFields(t, counts, map[string]any{
		"findings": json.Number("3"),
		"real":     json.Number("1"),
		"false":    json.Number("1"),
		"unknown":  json.Number("1"),
	})
	if grades := wireList(t, data, "grades"); len(grades) != 3 {
		t.Errorf("calibrate grades = %d, want 3", len(grades))
	}
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
		Commander: scriptCommander{run: func(cmd *core.Command) (core.Result, error) {
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

// cleanTreeStub answers the tracked-plus-untracked status probe the clean gate
// runs with a clean tree.
func cleanTreeStub(cmd *core.Command) (core.Result, error) {
	if isStatusProbe(cmd.Argv) {
		return core.Result{Stdout: ""}, nil
	}
	return core.Result{}, errUnexpectedCall(cmd.Argv)
}

// isStatusProbe reports the exact tracked-plus-untracked status argv the clean
// gate runs: git [-C root] status --porcelain --untracked-files=all.
func isStatusProbe(argv []string) bool {
	if len(argv) < 5 || argv[0] != "git" {
		return false
	}
	rest := argv[1:]
	if len(rest) >= 2 && rest[0] == "-C" {
		rest = rest[2:]
	}
	return len(rest) == 3 && rest[0] == "status" && rest[1] == "--porcelain" && rest[2] == "--untracked-files=all"
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
	assertWireFields(t, data, map[string]any{
		"clean":                true,
		"ignored_review_lines": json.Number("0"),
	})
}

// TestRuntimeConfigEnvelopeValidates proves the config explain data
// validates against the pinned configData profile: the stored bundle
// from a hand-authored policy renders the wire the schema pins.
func TestRuntimeConfigEnvelopeValidates(t *testing.T) {
	doc := loadSchemaDocument(t, "envelope.schema.json")
	dir := t.TempDir()
	t.Chdir(dir)
	guard := 9.5
	rs := &config.RunState{
		BaseSHA:    "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Trusted:    true,
		CapturedAt: "2026-11-02T12:00:00Z",
		Evidence: config.Evidence{
			SchemaVersion: "1",
			BaseSHA:       "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			Plan:          config.Plan{Status: config.PendingStatus},
			Values: []config.EvidenceValue{
				{Key: "budget_usd", Value: 9.5, Origin: config.FieldOrigin{Layer: config.LayerEnv, Source: "REVIEW_BUDGET_USD"}},
			},
		},
		Resolved: &config.Resolved{
			Plan: config.Plan{Status: config.PendingStatus},
			Defaults: config.ResolvedDefaults{
				Values: config.EffectiveDefaults{BudgetUSD: &guard},
			},
		},
	}
	if serr := config.WriteRunState(filepath.Join(".", ".verdict", "state", "7"), rs); serr != nil {
		t.Fatalf("WriteRunState: %v", serr)
	}
	seedControls(t, filepath.Join(".", ".verdict", "state", "7"))
	data, _, ref := config.Explain(".", "7")
	if ref != nil {
		t.Fatalf("Explain: %v", ref)
	}
	assertWireFields(t, data.Wire(), map[string]any{
		"mode":    "explain",
		"trusted": true,
	})
	assertEnvelope(t, doc, "config", "7", "2026-11-02T12:00:00Z", "post, report, and verify enforce this policy; re-fetch to move it", nil, data.Wire())
}

// TestRuntimeServeEnvelopeValidates proves every serve view validates against
// the generated serveData profile and that the show route keeps show's data
// profile byte-for-byte.
func TestRuntimeServeEnvelopeValidates(t *testing.T) {
	doc := loadSchemaDocument(t, "envelope.schema.json")
	defs, ok := doc["$defs"].(map[string]any)
	if !ok {
		t.Fatal("envelope schema has no $defs")
	}
	if _, ok := defs["serveData"]; !ok {
		t.Fatal("envelope schema lacks $defs/serveData")
	}
	dir := t.TempDir()
	bundle := filepath.Join(dir, ".verdict", "state", "4232")
	writeFixture(t, filepath.Join(bundle, "PR.json"),
		`{"state":"OPEN","headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",`+
			`"baseRefOid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`)
	writeFixture(t, filepath.Join(bundle, "post.json"),
		`{"schema_version":"1","review":{"id":9},"head":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",`+
			`"model":"m1","posted_at":"2026-01-01T00:00:00Z"}`)
	writeFixture(t, filepath.Join(bundle, "findings.json"), "["+fixtureFinding()+"]")
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	layer := query.New(query.Snapshot{
		Version: "1.2.3",
		Roots:   []query.Root{{Path: abs}},
		Runtime: fixedRuntime(),
	}, query.Options{Check: "critical", MaxBundles: 10})

	health := layer.Health(t.Context())
	assertEnvelope(t, doc, "serve", "", health.At, health.Next, health.Warnings, health.Data)
	roots := layer.Roots(t.Context())
	assertEnvelope(t, doc, "serve", "", roots.At, roots.Next, roots.Warnings, roots.Data)
	inbox, err := layer.Bundles(t.Context(), "")
	if err != nil {
		t.Fatalf("Bundles: %v", err)
	}
	assertEnvelope(t, doc, "serve", "", inbox.At, inbox.Next, inbox.Warnings, inbox.Data)
	detail, err := layer.Detail(t.Context(), query.RootID(abs), "4232")
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	assertEnvelope(t, doc, "serve", "4232", detail.At, detail.Next, detail.Warnings, detail.Data)
	show, err := layer.Show(t.Context(), query.RootID(abs), "4232", "c111")
	if err != nil {
		t.Fatalf("Show: %v", err)
	}
	assertEnvelope(t, doc, "show", "4232", show.At, show.Next, show.Warnings, show.Data)
}
