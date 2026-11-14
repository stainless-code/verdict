package trendcore

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// TestTrendWithoutIncidentsFlagKeepsEnvelopeBytes proves the opt-in flag is the
// only thing that can add the key: with an incident ledger present, the
// un-flagged envelope is byte-for-byte what the binary printed before the join
// existed (captured from pre-change code).
func TestTrendWithoutIncidentsFlagKeepsEnvelopeBytes(t *testing.T) {
	seedIncidentFixture(t, incidentLedger)
	read := core.ReadRequest{Runtime: &core.Runtime{Now: fixedTrendClock}, Repo: "acme/app"}
	resp, ref := Run(t.Context(), &Request{ReadRequest: read})
	if ref != nil {
		t.Fatalf("refusal %s: %s", ref.Code, ref.Message)
	}
	want := `{
  "schema_version": "1",
  "verb": "trend",
  "repo": "acme/app",
  "pr": null,
  "at": "2026-11-14T00:00:00Z",
  "next": "calibrate recent bundles for per-model tier precision",
  "warnings": [
    "trend skips .verdict/state/local-c/ (findings.json unreadable or not a findings array)"
  ],
  "data": {
    "generated_at": "2026-11-14T00:00:00Z",
    "bundles": 2,
    "reviews": 2,
    "reviews_with_findings": 1,
    "reviews_clean": 1,
    "findings_per_review": 0.5,
    "clean_rate": 0.5,
    "findings": 1,
    "open": 0,
    "addressed": 1,
    "closed": 0,
    "by_status": {
      "open": 0,
      "addressed": 1,
      "wont-fix": 0,
      "false-positive": 0,
      "deferred": 0
    },
    "by_tier": {
      "critical": 0,
      "important": 1,
      "informational": 0,
      "untagged": 0
    },
    "by_category": {
      "untagged": 1
    },
    "by_risk": {
      "low": {
        "bundles": 0,
        "findings": 0,
        "open": 0,
        "addressed": 0,
        "closed": 0
      },
      "medium": {
        "bundles": 2,
        "findings": 1,
        "open": 0,
        "addressed": 1,
        "closed": 0
      },
      "high": {
        "bundles": 0,
        "findings": 0,
        "open": 0,
        "addressed": 0,
        "closed": 0
      },
      "critical": {
        "bundles": 0,
        "findings": 0,
        "open": 0,
        "addressed": 0,
        "closed": 0
      },
      "unknown": {
        "bundles": 0,
        "findings": 0,
        "open": 0,
        "addressed": 0,
        "closed": 0
      }
    },
    "by_source": {
      "model": 1,
      "tool": 0
    },
    "withheld": {"agent":0,"below-severity":0,"exclusion":0,"validation":0},
    "provenance": {
      "tools": [
        null
      ],
      "models": [
        null
      ],
      "prompts_hashes": [
        null
      ]
    },
    "periods": [
      {
        "period": null,
        "bundles": 2,
        "findings": 1,
        "open": 0,
        "addressed": 1,
        "closed": 0,
        "by_tier": {
          "critical": 0,
          "important": 1,
          "informational": 0,
          "untagged": 0
        }
      }
    ],
    "per_bundle": [
      {
        "bundle": "local-a",
        "at": null,
        "models": [],
        "efforts": [],
        "provenance": {
          "tool_version": null,
          "prompts_hash": null,
          "model": null
        },
        "findings": 1,
        "clean": false,
        "open": 0,
        "addressed": 1,
        "closed": 0,
        "by_status": {
          "open": 0,
          "addressed": 1,
          "wont-fix": 0,
          "false-positive": 0,
          "deferred": 0
        },
        "by_tier": {
          "critical": 0,
          "important": 1,
          "informational": 0,
          "untagged": 0
        },
        "by_category": {
          "untagged": 1
        },
        "risk": "medium",
        "by_source": {
          "model": 1,
          "tool": 0
        },
        "withheld": {"agent":0,"below-severity":0,"exclusion":0,"validation":0}
      },
      {
        "bundle": "local-b",
        "at": null,
        "models": [],
        "efforts": [],
        "provenance": {
          "tool_version": null,
          "prompts_hash": null,
          "model": null
        },
        "findings": 0,
        "clean": true,
        "open": 0,
        "addressed": 0,
        "closed": 0,
        "by_status": {
          "open": 0,
          "addressed": 0,
          "wont-fix": 0,
          "false-positive": 0,
          "deferred": 0
        },
        "by_tier": {
          "critical": 0,
          "important": 0,
          "informational": 0,
          "untagged": 0
        },
        "by_category": {},
        "risk": "medium",
        "by_source": {
          "model": 0,
          "tool": 0
        },
        "withheld": {"agent":0,"below-severity":0,"exclusion":0,"validation":0}
      }
    ]
  },
  "error": null
}
`
	env := core.RenderEnvelope("trend", "acme/app", "", resp.At, resp.Next, resp.Warnings, resp.Data.Wire(), nil)
	if got := jq.PrettyOrdered(env); got != want {
		t.Fatalf("trend envelope moved:\n got %q\nwant %q", got, want)
	}
}

// TestIncidentMissRate pins the miss rate over the AC 5 fixture: three
// incidents, one caught, one missed by a clean bundle, one unmeasurable.
func TestIncidentMissRate(t *testing.T) {
	seedIncidentFixture(t, incidentLedger)
	resp, ref := runIncidents(t)
	if ref != nil {
		t.Fatalf("refusal %s: %s", ref.Code, ref.Message)
	}
	assertIncidentsData(t, resp, `{"bundles":3,"incidents":3,"measured":2,"unmeasured":1,`+
		`"hit":1,"missed":1,"miss_rate":0.5,"per_bundle":[`+
		`{"bundle":"local-a","incidents":1,"measured":1,"hit":1,"missed":0,"miss_rate":0},`+
		`{"bundle":"local-b","incidents":1,"measured":1,"hit":0,"missed":1,"miss_rate":1},`+
		`{"bundle":"local-c","incidents":1,"measured":0,"hit":0,"missed":0,"miss_rate":null}]}`)
	want := []string{
		"incidents: 3 attached, 2 measured, 1 unmeasured — missed 1 of 2 (50.0%)",
		"  local-a: 1 attached, 1 measured, 0 unmeasured — missed 0 of 1 (0.0%)",
		"  local-b: 1 attached, 1 measured, 0 unmeasured — missed 1 of 1 (100.0%)",
		"  local-c: 1 attached, 0 measured, 1 unmeasured — missed 0 of 0 (n/a)",
	}
	if got := lastHumanLines(resp, len(want)); !slices.Equal(got, want) {
		t.Fatalf("incident human lines = %q; want %q", got, want)
	}
}

// TestFalsePositiveIsNotACatch proves a wrong claim is not a catch: flipping
// the one finding to false-positive turns the caught incident into a miss.
func TestFalsePositiveIsNotACatch(t *testing.T) {
	seedIncidentFixture(t, incidentLedger)
	writeTrendStore(t, filepath.Join(readcommon.StateDir("."), "local-a"),
		`[`+strings.Replace(incidentCaughtFinding, `"status":"addressed"`, `"status":"false-positive"`, 1)+`]`)
	resp, ref := runIncidents(t)
	if ref != nil {
		t.Fatalf("refusal %s: %s", ref.Code, ref.Message)
	}
	assertIncidentsData(t, resp, `{"bundles":3,"incidents":3,"measured":2,"unmeasured":1,`+
		`"hit":0,"missed":2,"miss_rate":1,"per_bundle":[`+
		`{"bundle":"local-a","incidents":1,"measured":1,"hit":0,"missed":1,"miss_rate":1},`+
		`{"bundle":"local-b","incidents":1,"measured":1,"hit":0,"missed":1,"miss_rate":1},`+
		`{"bundle":"local-c","incidents":1,"measured":0,"hit":0,"missed":0,"miss_rate":null}]}`)
}

// TestMissRateIsNullWithoutMeasured proves a zero denominator reads null, not
// zero, for both an absent and an empty ledger.
func TestMissRateIsNullWithoutMeasured(t *testing.T) {
	t.Run("absent ledger", func(t *testing.T) { assertNoMeasurement(t, "") })
	t.Run("empty ledger", func(t *testing.T) { assertNoMeasurement(t, "\n") })
}

// assertNoMeasurement runs the join over one ledger spelling that attaches
// nothing and checks the empty rollup and its human line.
func assertNoMeasurement(t *testing.T, ledger string) {
	t.Helper()
	seedIncidentFixture(t, ledger)
	resp, ref := runIncidents(t)
	if ref != nil {
		t.Fatalf("refusal %s: %s", ref.Code, ref.Message)
	}
	assertIncidentsData(t, resp, `{"bundles":0,"incidents":0,"measured":0,"unmeasured":0,`+
		`"hit":0,"missed":0,"miss_rate":null,"per_bundle":[]}`)
	if got := len(resp.Data.Rows); got != 2 {
		t.Fatalf("rollup rows = %d; want 2", got)
	}
	want := "incidents: 0 attached, 0 measured, 0 unmeasured — missed 0 of 0 (n/a)"
	if got := lastHumanLines(resp, 1); !slices.Equal(got, []string{want}) {
		t.Fatalf("incident human line = %q; want %q", got, want)
	}
}

// TestCoveresAnyPathSpellsTheCoverRule pins the join's edge: a finding covers
// its own path or a path under the incident path taken as a directory, and
// nothing that only shares a string prefix.
func TestCoversAnyPathSpellsTheCoverRule(t *testing.T) {
	for _, tc := range []struct {
		incident string
		file     string
		want     bool
	}{
		{incident: "src/a.ts", file: "src/a.ts", want: true},
		{incident: "src", file: "src/a.ts", want: true},
		{incident: "src/a.ts", file: "src/a.tsx", want: false},
		{incident: "src/a.ts", file: "src/ab.ts", want: false},
		{incident: "src", file: "srcs/a.ts", want: false},
		{incident: "src/b", file: "src/a/b", want: false},
	} {
		if got := coversAnyPath(tc.file, []string{tc.incident}); got != tc.want {
			t.Errorf("coversAnyPath(%q, %q) = %v; want %v", tc.incident, tc.file, got, tc.want)
		}
	}
}

// TestUnmeasuredBundleWarnsAndSkips proves a bundle without a findings store
// is named once and stays out of the denominator instead of counting as a miss.
func TestUnmeasuredBundleWarnsAndSkips(t *testing.T) {
	seedIncidentFixture(t, incidentLedger)
	prog := &progressLog{}
	read := core.ReadRequest{Runtime: &core.Runtime{Now: fixedTrendClock, Progress: prog}, Repo: "acme/app"}
	resp, ref := Run(t.Context(), &Request{ReadRequest: read, Incidents: true})
	if ref != nil {
		t.Fatalf("refusal %s: %s", ref.Code, ref.Message)
	}
	var incidentWarns []string
	for _, warn := range prog.warns {
		if strings.HasPrefix(warn, "trend skips incident ") {
			incidentWarns = append(incidentWarns, warn)
		}
	}
	wantWarns := []string{"trend skips incident local-c: .verdict/state/local-c/ (findings.json unreadable or not a findings array)"}
	if !slices.Equal(incidentWarns, wantWarns) {
		t.Fatalf("incident warnings = %q; want %q", incidentWarns, wantWarns)
	}
	assertIncidentsData(t, resp, `{"bundles":3,"incidents":3,"measured":2,"unmeasured":1,`+
		`"hit":1,"missed":1,"miss_rate":0.5,"per_bundle":[`+
		`{"bundle":"local-a","incidents":1,"measured":1,"hit":1,"missed":0,"miss_rate":0},`+
		`{"bundle":"local-b","incidents":1,"measured":1,"hit":0,"missed":1,"miss_rate":1},`+
		`{"bundle":"local-c","incidents":1,"measured":0,"hit":0,"missed":0,"miss_rate":null}]}`)
}

// TestCorruptBundleStoreKeepsTheReaderReason proves a bundle whose store will
// not read warns with the reader's reason, so a corrupt store never reads as an
// absent one.
func TestCorruptBundleStoreKeepsTheReaderReason(t *testing.T) {
	seedIncidentFixture(t, incidentLedger)
	writeTrendStore(t, filepath.Join(readcommon.StateDir("."), "local-a"), "not-json")
	prog := &progressLog{}
	read := core.ReadRequest{Runtime: &core.Runtime{Now: fixedTrendClock, Progress: prog}, Repo: "acme/app"}
	resp, ref := Run(t.Context(), &Request{ReadRequest: read, Incidents: true})
	if ref != nil {
		t.Fatalf("refusal %s: %s", ref.Code, ref.Message)
	}
	var incidentWarns []string
	for _, warn := range prog.warns {
		if strings.HasPrefix(warn, "trend skips incident ") {
			incidentWarns = append(incidentWarns, warn)
		}
	}
	want := []string{
		"trend skips incident local-a: .verdict/state/local-a/ (findings.json unreadable or not a findings array)",
		"trend skips incident local-c: .verdict/state/local-c/ (findings.json unreadable or not a findings array)",
	}
	if !slices.Equal(incidentWarns, want) {
		t.Fatalf("incident warnings = %q; want %q", incidentWarns, want)
	}
	assertIncidentsData(t, resp, `{"bundles":3,"incidents":3,"measured":1,"unmeasured":2,`+
		`"hit":0,"missed":1,"miss_rate":1,"per_bundle":[`+
		`{"bundle":"local-a","incidents":1,"measured":0,"hit":0,"missed":0,"miss_rate":null},`+
		`{"bundle":"local-b","incidents":1,"measured":1,"hit":0,"missed":1,"miss_rate":1},`+
		`{"bundle":"local-c","incidents":1,"measured":0,"hit":0,"missed":0,"miss_rate":null}]}`)
}

// TestIncidentSummaryCountsMisses proves the summary line counts misses: the
// fixture reads three catches against one miss, so a hit/missed swap in the
// rendered line cannot pass.
func TestIncidentSummaryCountsMisses(t *testing.T) {
	seedIncidentFixture(t, incidentLedgerManyHits)
	resp, ref := runIncidents(t)
	if ref != nil {
		t.Fatalf("refusal %s: %s", ref.Code, ref.Message)
	}
	assertIncidentsData(t, resp, `{"bundles":2,"incidents":4,"measured":4,"unmeasured":0,`+
		`"hit":3,"missed":1,"miss_rate":0.25,"per_bundle":[`+
		`{"bundle":"local-a","incidents":3,"measured":3,"hit":3,"missed":0,"miss_rate":0},`+
		`{"bundle":"local-b","incidents":1,"measured":1,"hit":0,"missed":1,"miss_rate":1}]}`)
	want := []string{
		"incidents: 4 attached, 4 measured, 0 unmeasured — missed 1 of 4 (25.0%)",
		"  local-a: 3 attached, 3 measured, 0 unmeasured — missed 0 of 3 (0.0%)",
		"  local-b: 1 attached, 1 measured, 0 unmeasured — missed 1 of 1 (100.0%)",
	}
	if got := lastHumanLines(resp, len(want)); !slices.Equal(got, want) {
		t.Fatalf("incident human lines = %q; want %q", got, want)
	}
}

// TestIncidentLedgerRefusesCorrupt proves a ledger this build cannot read
// refuses typed instead of reading as zeros.
func TestIncidentLedgerRefusesCorrupt(t *testing.T) {
	seedIncidentFixture(t, `{"schema_version":"9","bundle":"local-a","sha":"`+strings.Repeat("a", 40)+`",`+
		`"paths":["src/a.ts"],"note":null,"url":null,"detected_at":"2026-11-10T00:00:00Z"}`+"\n")
	if _, ref := runIncidents(t); ref == nil || ref.Code != "corrupt-store" {
		t.Fatalf("refusal = %v; want corrupt-store", ref)
	}
}

// runIncidents runs the join over the seeded working directory.
func runIncidents(t *testing.T) (*Response, *core.RefusalError) {
	t.Helper()
	read := core.ReadRequest{Runtime: &core.Runtime{Now: fixedTrendClock, Progress: &progressLog{}}, Repo: "acme/app"}
	return Run(t.Context(), &Request{ReadRequest: read, Incidents: true})
}

// assertIncidentsData compares data.incidents with a literal document.
func assertIncidentsData(t *testing.T, resp *Response, want string) {
	t.Helper()
	got, ok := resp.Data.Wire().Get("incidents")
	if !ok {
		t.Fatal("data carries no incidents key")
	}
	obj, ok := got.(*jq.Obj)
	if !ok {
		t.Fatalf("data.incidents = %T; want an object", got)
	}
	if compact := obj.MarshalCompact(); compact != want {
		t.Fatalf("data.incidents = %s; want %s", compact, want)
	}
}

// lastHumanLines returns the trailing human lines of one run.
func lastHumanLines(resp *Response, n int) []string {
	if len(resp.Human) < n {
		return resp.Human
	}
	return resp.Human[len(resp.Human)-n:]
}

func fixedTrendClock() time.Time {
	return time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
}

// seedIncidentFixture lands the AC 5 fixture in the working directory: three
// bundles (caught, clean, without a findings store) and the given ledger.
func seedIncidentFixture(t *testing.T, ledger string) {
	t.Helper()
	t.Chdir(t.TempDir())
	state := readcommon.StateDir(".")
	writeTrendStore(t, filepath.Join(state, "local-a"), `[`+incidentCaughtFinding+`]`)
	writeTrendStore(t, filepath.Join(state, "local-b"), `[]`)
	if err := os.MkdirAll(filepath.Join(state, "local-c"), 0o755); err != nil {
		t.Fatal(err)
	}
	if ledger != "" {
		writeIncidentLedger(t, ledger)
	}
}

// incidentCaughtFinding is the one finding local-a stores: it sits on the
// incident path local-a is attached to.
const incidentCaughtFinding = `{"kind":"inline","path":"src/a.ts","side":"RIGHT","line":2,` +
	`"anchored":true,"body":"Unhandled await error path here.","suggestion":null,` +
	`"inBounds":true,"rigor":"walked","severity":"important",` +
	`"sig":"a111111111111111111111111111111111111111","sig_version":"1",` +
	`"firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"addressed",` +
	`"rationale":"","history":[]}`

// incidentLedger attaches one incident per fixture bundle: a caught one on the
// caught bundle, a missed one on the clean bundle, and one no store can
// measure.
const incidentLedger = `{"schema_version":"1","bundle":"local-a","sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","paths":["src/a.ts"],"note":null,"url":null,"detected_at":"2026-11-10T00:00:00Z"}
{"schema_version":"1","bundle":"local-b","sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","paths":["src/b.ts"],"note":null,"url":null,"detected_at":"2026-11-11T00:00:00Z"}
{"schema_version":"1","bundle":"local-c","sha":"cccccccccccccccccccccccccccccccccccccccc","paths":["src/c.ts"],"note":null,"url":null,"detected_at":"2026-11-12T00:00:00Z"}
`

// incidentLedgerManyHits attaches three caught incidents to local-a and one
// missed one to local-b, so the summary line's catch and miss counts differ.
const incidentLedgerManyHits = `{"schema_version":"1","bundle":"local-a","sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa1","paths":["src/a.ts"],"note":null,"url":null,"detected_at":"2026-11-10T00:00:00Z"}
{"schema_version":"1","bundle":"local-a","sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa2","paths":["src/a.ts"],"note":null,"url":null,"detected_at":"2026-11-10T00:01:00Z"}
{"schema_version":"1","bundle":"local-a","sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa3","paths":["src/a.ts"],"note":null,"url":null,"detected_at":"2026-11-10T00:02:00Z"}
{"schema_version":"1","bundle":"local-b","sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","paths":["src/b.ts"],"note":null,"url":null,"detected_at":"2026-11-11T00:00:00Z"}
`

func writeIncidentLedger(t *testing.T, content string) {
	t.Helper()
	path := filepath.FromSlash(".verdict/incidents.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeTrendStore(t *testing.T, dir, findings string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "findings.json"), []byte(findings), 0o600); err != nil {
		t.Fatal(err)
	}
}
