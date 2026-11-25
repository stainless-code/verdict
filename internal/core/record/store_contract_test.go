package recordcore_test

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/testkit"
)

const (
	storeHead  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	storeBase  = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	storeShort = "aaaaaaa"
)

const storeDiff = "diff --git a/notes.txt b/notes.txt\n" +
	"index 1111111..2222222 100644\n--- a/notes.txt\n+++ b/notes.txt\n" +
	"@@ -1,3 +1,3 @@\n top\n-old line\n+new line\n bottom\n"

const storeDiffSHA = "318d35b3738f5e9aadfdaef4e9c0f2b1772e60605db9a0b29f7008fd67c6e268"

const storeBody = "> [!IMPORTANT] Store contract fixture with enough substance."

func writeStoreBundle(t *testing.T, slug, review string) {
	t.Helper()
	t.Chdir(t.TempDir())
	testkit.WriteFile(t, ".verdict/state/"+slug+"/source.json",
		`{"head_sha":"`+storeHead+`","base_sha":"`+storeBase+`","head_ref":"work"}`)
	testkit.WriteFile(t, ".verdict/state/"+slug+"/full.diff", storeDiff)
	testkit.WriteFile(t, ".verdict/state/"+slug+"/review.json", review)
}

// toolReceipt renders the verify-receipt.json `verdict verify` writes: the
// command it ran, the finding sig patch.json named ("" for none), and the
// clean head it ran on ("" when it could not bind one).
func toolReceipt(cmd, sig, head string, exit int, timedOut bool) string {
	doc := map[string]any{"schema_version": "1", "command": cmd, "at": "2026-01-01T00:00:00Z", "exit": exit, "timed_out": timedOut}
	if sig != "" {
		doc["sig"] = sig
	}
	if head != "" {
		doc["head"] = head
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

// writeVerifyReceipt writes the receipt a passing `verdict verify` run of
// `go test ./...` leaves at head: what a VERIFIED claim needs before it flips.
func writeVerifyReceipt(t *testing.T, dir, head string) {
	t.Helper()
	testkit.WriteFile(t, dir+"/verify-receipt.json", toolReceipt("go test ./...", "", head, 0, false))
}

func storeEntries(t *testing.T, slug string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(".verdict/state/" + slug + "/findings.json")
	if err != nil {
		t.Fatalf("read findings.json: %v", err)
	}
	var entries []map[string]any
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("findings.json is not a JSON list: %v\n%s", err, raw)
	}
	return entries
}

// bySig indexes decoded store entries by sig, failing on a duplicate sig
// (the store is a set keyed by sig — a twin is the dedup bug, not a detail).
func bySig(t *testing.T, entries []map[string]any) map[string]map[string]any {
	t.Helper()
	out := make(map[string]map[string]any, len(entries))
	for _, e := range entries {
		sig := entrySig(t, e)
		if _, dup := out[sig]; dup {
			t.Fatalf("duplicate sig %s in the store", sig)
		}
		out[sig] = e
	}
	return out
}

func entrySig(t *testing.T, e map[string]any) string {
	t.Helper()
	sig, ok := e["sig"].(string)
	if !ok || sig == "" {
		t.Fatalf("entry without a string sig: %v", e)
	}
	return sig
}

func historyOf(t *testing.T, entry map[string]any) []any {
	t.Helper()
	events, ok := entry["history"].([]any)
	if !ok {
		t.Fatalf("history = %T, want a list", entry["history"])
	}
	return events
}

func hasWarning(warns []string, want string) bool {
	for _, w := range warns {
		if strings.Contains(w, want) {
			return true
		}
	}
	return false
}

// TestRunRecordInputShapeRefusals is the fail-closed input gate the manual
// promises: every misshapen agent input refuses typed `bad-review-input`, a
// present-but-corrupt findings.json refuses `corrupt-store`, and nothing is
// written (no findings.json, no record.json). Each row names the exact
// refusal message, so a row cannot pass by tripping a different gate.
//
//nolint:revive // the table is the refusal matrix; each row names its gate
func TestRunRecordInputShapeRefusals(t *testing.T) {
	finding := func(extra string) string {
		return `{"path":"notes.txt","line":2,"severity":"important","claim":"Cited finding here with enough substance.","quote":"new line","confidence":"high"` + extra + `}`
	}
	valid := `{"schema_version":"1","summary":"A short summary of the clean review."`
	tests := []struct {
		name    string
		extra   string
		code    string
		wantErr string
	}{
		{"findings-not-array", `,"findings":{}`, "bad-review-input", "findings must be an array"},
		{"finding-not-object", `,"findings":[42]`, "bad-review-input", "finding must be an object"},
		{
			"finding-path-not-string", `,"findings":[` + finding("") + `,` + finding(`,"path":7`) + `]`,
			"bad-review-input", "path must be a string",
		},
		{"resolved-not-array", `,"resolved":{}`, "bad-review-input", "value must be an array of strings"},
		{"resolved-not-string", `,"resolved":[42]`, "bad-review-input", "array entry must be a string"},
		{"triage-not-array", `,"triage":"x"`, "bad-review-input", "triage must be an array"},
		{"triage-not-object", `,"triage":[42]`, "bad-review-input", "triage entry must be an object"},
		{"coverage-ranges-array", `,"coverage":[{"file":"notes.txt","ranges":["1-2"]}]`, "bad-review-input", "value must be a string"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			writeStoreBundle(t, "local-shape", valid+tc.extra+`}`)
			checkShapeRefusal(t, "local-shape", tc.code, tc.wantErr, false)
		})
	}

	fileTests := []struct {
		name    string
		path    string
		content string
		code    string
		wantErr string
	}{
		{"verification-not-list", "verification.json", `{"sig":"c111"}`, "bad-review-input", "malformed claims"},
		{"verification-claim-not-object", "verification.json", `[42]`, "bad-review-input", "non-object claim"},
		{"prior-not-list", "findings.json", `{"sig":"c111"}`, "corrupt-store", "corrupt or not a JSON list"},
		{"prior-non-object-element", "findings.json", `[42]`, "corrupt-store", "corrupt or not a JSON list"},
		{"prior-non-string-sig", "findings.json", `[{"sig":42,"body":"x"}]`, "corrupt-store", "corrupt or not a JSON list"},
		{"prior-container-severity", "findings.json", `[{"sig":"c111","severity":{"a":1}}]`, "corrupt-store", "corrupt or not a JSON list"},
	}
	for _, tc := range fileTests {
		t.Run(tc.name, func(t *testing.T) {
			writeStoreBundle(t, "local-shape", valid+`,"findings":[]}`)
			testkit.WriteFile(t, ".verdict/state/local-shape/"+tc.path, tc.content)
			checkShapeRefusal(t, "local-shape", tc.code, tc.wantErr, tc.path == "findings.json")
			kept, err := os.ReadFile(".verdict/state/local-shape/" + tc.path)
			if err != nil || string(kept) != tc.content {
				t.Errorf("%s = %q (err %v); refusal must not rewrite it", tc.path, kept, err)
			}
		})
	}
}

// checkShapeRefusal runs one refusal case and asserts the typed code, the
// message naming the gate, and that no store artifact was written. A prior
// findings.json fixture is the one artifact that predates the run.
//
//nolint:revive // one refusal asserts the code, the gate message, and the unwritten artifacts inline
func checkShapeRefusal(t *testing.T, slug, code, wantErr string, priorStore bool) {
	t.Helper()
	c := &testkit.Collector{}
	resp, ref := recordcore.Run(context.Background(), recordRequest(t, c, slug))
	if ref == nil {
		t.Fatalf("Run(%s) succeeded (data %+v); want refusal %s", slug, resp, code)
	}
	if ref.Code != code {
		t.Errorf("code = %q, want %q (message %q)", ref.Code, code, ref.Message)
	}
	if !refusalNames(ref, wantErr) {
		t.Errorf("message = %q (problems %v), want it to name %q", ref.Message, ref.Problems, wantErr)
	}
	for _, artifact := range []string{"findings.json", "record.json"} {
		if priorStore && artifact == "findings.json" {
			continue
		}
		if _, err := os.Stat(".verdict/state/" + slug + "/" + artifact); !os.IsNotExist(err) {
			t.Errorf("refusal wrote %s", artifact)
		}
	}
}

// refusalNames reports whether the refusal, or one of its review-input
// problems, names want.
func refusalNames(ref *core.RefusalError, want string) bool {
	if strings.Contains(ref.Message, want) {
		return true
	}
	for _, problem := range ref.Problems {
		if strings.Contains(problem.Message, want) {
			return true
		}
	}
	return false
}

// TestRunRecordRefusesOversizeVerification proves the 32 MiB agent-input cap
// reaches verification.json too: a padded-but-valid claims file over the cap
// refuses bad-review-input instead of being parsed, and the run writes
// nothing.
func TestRunRecordRefusesOversizeVerification(t *testing.T) {
	writeStoreBundle(t, "local-cap",
		`{"schema_version":"1","body":"`+storeBody+`","approved":false,"comments":[]}`)
	padded := "[" + strings.Repeat(" ", 32<<20) + "]"
	testkit.WriteFile(t, ".verdict/state/local-cap/verification.json", padded)
	checkShapeRefusal(t, "local-cap", "bad-review-input", "32 MiB agent-input cap", false)
}

// TestRunRecordArtifactValues pins the record artifact the read verbs trust:
// every scalar comes from the bundle, never from an empty default. The oracle
// for diff_sha256 is the shasum literal above.
//
//nolint:revive // every record.json field asserts inline against its oracle
func TestRunRecordArtifactValues(t *testing.T) {
	review := `{"schema_version":"1","body":"` + storeBody + `","approved":false,` +
		`"confidence":3,"security_summary":"No new attack surface.",` +
		`"comments":[{"path":"notes.txt","line":2,"body":"Cited finding here with enough substance."}]}`
	writeStoreBundle(t, "local-av", review)
	testkit.WriteFile(t, ".verdict/state/local-av/cost.json", `[{"models":[{"name":"claude-sonnet-4"}]}]`)

	c := &testkit.Collector{}
	resp, ref := recordcore.Run(context.Background(), recordRequest(t, c, "local-av"))
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	rec := readRecord(t, "local-av")

	want := map[string]any{
		"schema_version":   "1",
		"status":           "local",
		"head":             storeHead,
		"diff_sha256":      storeDiffSHA,
		"recorded_at":      "2026-11-02T12:00:00Z",
		"findings":         float64(1),
		"open":             float64(1),
		"security_summary": "No new attack surface.",
		"model":            "claude-sonnet-4",
	}
	for key, wantVal := range want {
		if got := rec[key]; got != wantVal {
			t.Errorf("record.json %s = %#v, want %#v", key, got, wantVal)
		}
	}
	for _, key := range []string{"tool_version", "prompts_hash"} {
		v, okStr := rec[key].(string)
		if !okStr || v == "" {
			t.Errorf("record.json %s = %#v, want a stamped value", key, rec[key])
		}
	}

	if len(resp.Data.ItemsRaw) != 1 {
		t.Fatalf("data items = %d, want 1", len(resp.Data.ItemsRaw))
	}
	item, ok := resp.Data.ItemsRaw[0].(*jq.Obj)
	if !ok {
		t.Fatalf("item is %T, want an ordered object", resp.Data.ItemsRaw[0])
	}
	if loc, _ := item.Get("locator"); loc != "notes.txt:2" {
		t.Errorf("item locator = %v, want notes.txt:2", loc)
	}

	if hasWarning(c.Warnings, "no hunks indexed") {
		t.Errorf("warnings = %v; hunks were indexed", c.Warnings)
	}
}

// readRecord decodes a bundle's record.json as the reader sees it: a map of
// wire keys, so the assertions cannot inherit the writer's Go types.
func readRecord(t *testing.T, slug string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(".verdict/state/" + slug + "/record.json")
	if err != nil {
		t.Fatalf("read record.json: %v", err)
	}
	var rec map[string]any
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("record.json is not JSON: %v\n%s", err, raw)
	}
	return rec
}

// TestRunRecordSigContract pins sig_version 1 as the manual defines it: the
// lowercase SHA-1 of `path + "\n" + heading + "\n" + body` with every
// whitespace run squashed to one space. The expected digests were taken with
// `tr -s '[:space:]' ' ' | sed … | shasum -a 1`, not from the code under test.
func TestRunRecordSigContract(t *testing.T) {
	const (
		// sha1("notes.txt Cited finding here with enough substance.")
		inlineSig = "101d0761051f365731b9b857810c9b3603c50208"
	)
	review := `{"schema_version":"1","body":"` + storeBody + `\n\n### Watch\n\nProse under the watch heading.",` +
		`"approved":false,"comments":[{"path":"notes.txt","line":2,` +
		`"body":"Cited finding here with enough substance."}]}`
	writeStoreBundle(t, "local-sig", review)
	c := &testkit.Collector{}
	if _, ref := recordcore.Run(context.Background(), recordRequest(t, c, "local-sig")); ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	entries := storeEntries(t, "local-sig")
	if len(entries) != 1 {
		t.Fatalf("findings = %d, want only the 1 inline finding (no scraped section)", len(entries))
	}
	got := bySig(t, entries)
	inline, ok := got[inlineSig]
	if !ok {
		t.Fatalf("no entry for sig %s; sigs = %v", inlineSig, sigsOf(got))
	}
	if inline["kind"] != "inline" || inline["path"] != "notes.txt" || inline["sig_version"] != "1" {
		t.Errorf("inline entry = %v", inline)
	}
}

func sigsOf(entries map[string]map[string]any) []string {
	out := make([]string, 0, len(entries))
	for sig := range entries {
		out = append(out, sig)
	}
	return out
}

// TestRunRecordPriorMerge proves the re-post merge contract on a store that
// already holds history: a re-reported finding keeps its sig (one entry, not
// a twin), keeps the operator's status and firstSeen, advances lastSeen, and
// keeps unknown keys; an unreported prior entry survives untouched; and a
// genuinely new finding lands after the prior block.
//
// The store is deliberately ordered so the expected output is not the prior
// order and not the review order: [prior inline, prior section, new inline].
//
//nolint:revive // the store assertions are the contract, kept in one legible block
func TestRunRecordPriorMerge(t *testing.T) {
	const (
		// sha1("notes.txt Cited finding here with enough substance.")
		priorSig = "101d0761051f365731b9b857810c9b3603c50208"
		// sha1("notes.txt Second inline finding body for the store.")
		newSig = "cec2c320fb7d7983e6271674e0f6af6718374758"
		// priorOnlySig is an entry the delta review never mentions again.
		priorOnlySig = "ffffffffffffffffffffffffffffffffffffffff"
	)
	prior := `[` +
		`{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,` +
		`"body":"Cited finding here with enough substance.","suggestion":null,"kb":[],` +
		`"inBounds":true,"rigor":"walked","severity":"important","sig":"` + priorSig + `",` +
		`"sig_version":"1","firstSeen":"ddddddd","lastSeen":"ddddddd","status":"addressed",` +
		`"rationale":"Fixed in the delta.","history":[],"future_key":{"kept":true}},` +
		`{"kind":"section","heading":"### Old","anchored":"body","body":"### Old",` +
		`"suggestion":null,"kb":[],"inBounds":null,"rigor":null,"severity":null,` +
		`"sig":"` + priorOnlySig + `","sig_version":"1","firstSeen":"eeeeeee",` +
		`"lastSeen":"eeeeeee","status":"deferred","rationale":"Waiting on the migration.","history":[]}` +
		`]`
	review := `{"schema_version":"1","body":"` + storeBody + `","approved":false,"comments":[` +
		`{"path":"notes.txt","line":2,"body":"Cited finding here with enough substance."},` +
		`{"path":"notes.txt","line":3,"body":"Second inline finding body for the store."}]}`
	writeStoreBundle(t, "local-pm", review)
	testkit.WriteFile(t, ".verdict/state/local-pm/findings.json", prior)

	c := &testkit.Collector{}
	if _, ref := recordcore.Run(context.Background(), recordRequest(t, c, "local-pm")); ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	entries := storeEntries(t, "local-pm")
	if len(entries) != 3 {
		t.Fatalf("findings = %d, want 3 (two priors + one new, re-report deduped)", len(entries))
	}
	var order []string
	for _, e := range entries {
		order = append(order, entrySig(t, e))
	}
	wantOrder := []string{priorSig, priorOnlySig, newSig}
	if strings.Join(order, ",") != strings.Join(wantOrder, ",") {
		t.Errorf("store order = %v, want prior block then the new finding %v", order, wantOrder)
	}

	got := bySig(t, entries)
	merged := got[priorSig]
	if merged["status"] != "addressed" {
		t.Errorf("status = %v, want addressed (a re-report never reopens)", merged["status"])
	}
	if merged["firstSeen"] != "ddddddd" {
		t.Errorf("firstSeen = %v, want the prior ddddddd", merged["firstSeen"])
	}
	if merged["lastSeen"] != storeShort {
		t.Errorf("lastSeen = %v, want the new head %s", merged["lastSeen"], storeShort)
	}
	if _, ok := merged["future_key"]; !ok {
		t.Error("unknown prior key future_key was dropped")
	}
	if priorEntry := got[priorOnlySig]; priorEntry["status"] != "deferred" {
		t.Errorf("prior-only entry status = %v, want deferred (untouched)", priorEntry["status"])
	}
	if fresh := got[newSig]; fresh["status"] != "open" || fresh["firstSeen"] != storeShort {
		t.Errorf("new entry = %v", fresh)
	}
}

// TestRunRecordDeltaReopen proves the delta-run rule: an addressed finding on
// a path the incremental diff touches goes back to open, with the reopen on
// the record. The prior entry is the only entry, so a run that drops the
// prior store cannot pass.
//
//nolint:revive // the reopen assertions walk the history inline
func TestRunRecordDeltaReopen(t *testing.T) {
	const addressedSig = "cccccccccccccccccccccccccccccccccccccccc"
	prior := `[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,` +
		`"body":"Cited finding here with enough substance.","suggestion":null,"kb":[],` +
		`"inBounds":true,"rigor":"walked","severity":"important","sig":"` + addressedSig + `",` +
		`"sig_version":"1","firstSeen":"ddddddd","lastSeen":"ddddddd","status":"addressed",` +
		`"rationale":"Fixed.","history":[]}]`
	writeStoreBundle(t, "local-ro",
		`{"schema_version":"1","body":"`+storeBody+`","approved":false,"comments":[]}`)
	testkit.WriteFile(t, ".verdict/state/local-ro/findings.json", prior)
	testkit.WriteFile(t, ".verdict/state/local-ro/incremental.diff",
		"diff --git a/notes.txt b/notes.txt\n--- a/notes.txt\n+++ b/notes.txt\n@@ -1,1 +1,1 @@\n-a\n+b\n")

	c := &testkit.Collector{}
	if _, ref := recordcore.Run(context.Background(), recordRequest(t, c, "local-ro")); ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	entries := storeEntries(t, "local-ro")
	if len(entries) != 1 {
		t.Fatalf("findings = %d, want the one prior entry", len(entries))
	}
	entry := entries[0]
	if entry["status"] != "open" {
		t.Errorf("status = %v, want open (the delta touched notes.txt)", entry["status"])
	}
	events := historyOf(t, entry)
	found := false
	for _, ev := range events {
		m, ok := ev.(map[string]any)
		if !ok {
			continue
		}
		if m["event"] == "reopened" && m["note"] == "delta touched path" && m["sha"] == storeShort {
			found = true
		}
	}
	if !found {
		t.Errorf("history = %v, want a reopened delta event", events)
	}
}

// TestRunRecordFenceDemotion proves the suggestion rule: a suggestion that
// carries a fence cannot render as a fenced block, so the comment is demoted
// to the body (anchored false) with its suggestion text intact; a plain
// suggestion stays inline and anchored, and never warns.
//
//nolint:gocognit,revive // the two legs assert anchor, suggestion, and warn inline
func TestRunRecordFenceDemotion(t *testing.T) {
	tests := []struct {
		name       string
		suggestion string
		wantWarn   string
		wantAnchor bool
	}{
		{"fenced", "```go\nold line\n```", "suggestion contains a fence", false},
		{"plain", "new line", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			slug := "local-fence-" + tc.name
			review := `{"schema_version":"1","body":"` + storeBody + `","approved":false,"comments":[` +
				`{"path":"notes.txt","line":2,"body":"Cited finding here with enough substance.",` +
				`"suggestion":` + mustJSON(t, tc.suggestion) + `}]}`
			writeStoreBundle(t, slug, review)
			c := &testkit.Collector{}
			if _, ref := recordcore.Run(context.Background(), recordRequest(t, c, slug)); ref != nil {
				t.Fatalf("Run refusal: %v", ref)
			}
			var inline map[string]any
			for _, e := range storeEntries(t, slug) {
				if e["kind"] == "inline" {
					inline = e
				}
			}
			if inline == nil {
				t.Fatal("no inline entry in the store")
			}
			if inline["anchored"] != tc.wantAnchor {
				t.Errorf("anchored = %v, want %v", inline["anchored"], tc.wantAnchor)
			}
			if inline["suggestion"] != tc.suggestion {
				t.Errorf("suggestion = %v, want the agent's text %q", inline["suggestion"], tc.suggestion)
			}
			if tc.wantWarn == "" {
				if hasWarning(c.Warnings, "demoting") {
					t.Errorf("warnings = %v; a plain suggestion demotes nothing", c.Warnings)
				}
				return
			}
			if !hasWarning(c.Warnings, tc.wantWarn) {
				t.Errorf("warnings = %v, want %q", c.Warnings, tc.wantWarn)
			}
		})
	}
}

func mustJSON(t *testing.T, s string) string {
	t.Helper()
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal %q: %v", s, err)
	}
	return string(raw)
}

// TestRunRecordKeepsFetchSnapshot proves the fetch-time artifact survives the
// run untouched: hunks.tsv is what post/record validate anchors against, so a
// merge that deletes or regenerates it destroys the pinned snapshot the
// operator fetched. The fixture pre-seeds a snapshot whose row differs from
// anything full.diff would derive (NEW 1-99, not 1-3), so a rebuild from the
// diff is as visible as a deletion.
func TestRunRecordKeepsFetchSnapshot(t *testing.T) {
	const pinned = "notes.txt\tNEW\t1\t99\n"
	writeStoreBundle(t, "local-snap",
		`{"schema_version":"1","body":"`+storeBody+`","approved":false,"comments":[]}`)
	testkit.WriteFile(t, ".verdict/state/local-snap/hunks.tsv", pinned)
	c := &testkit.Collector{}
	if _, ref := recordcore.Run(context.Background(), recordRequest(t, c, "local-snap")); ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	raw, err := os.ReadFile(".verdict/state/local-snap/hunks.tsv")
	if err != nil {
		t.Fatalf("hunks.tsv after the run: %v", err)
	}
	if string(raw) != pinned {
		t.Errorf("hunks.tsv = %q, want the pinned fetch-time snapshot %q untouched", raw, pinned)
	}
	if hasWarning(c.Warnings, "no fetch-time hunk snapshot") {
		t.Errorf("warnings = %v; the pinned snapshot exists, nothing to rebuild", c.Warnings)
	}
}

// TestRunRecordAuthorityClaims pins the manual's authority rule —
// "the script disposes, the agent proposes, the human vetoes": a bare
// resolved[] sig records a history claim and never flips status; addressed
// needs a VERIFIED claim with a verify command and a resultHead matching
// HEAD; NOT VERIFIED reopens; anything else only logs a revalidation event.
//
//nolint:gocognit,revive // the authority matrix asserts status and history inline
func TestRunRecordAuthorityClaims(t *testing.T) {
	const (
		// sha1("notes.txt Cited finding here with enough substance.")
		sig = "101d0761051f365731b9b857810c9b3603c50208"
		// comment re-reports the finding so the run has an entry to flip.
		comment = `{"path":"notes.txt","line":2,"body":"Cited finding here with enough substance."}`
	)
	// addressedPrior is a bundle that already carries the finding as
	// addressed, for the re-open legs.
	addressedPrior := `[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,` +
		`"body":"Cited finding here with enough substance.","suggestion":null,"kb":[],` +
		`"inBounds":true,"rigor":"walked","severity":"important","sig":"` + sig + `",` +
		`"sig_version":"1","firstSeen":"ddddddd","lastSeen":"ddddddd","status":"addressed",` +
		`"rationale":"Fixed.","history":[]}]`
	tests := []struct {
		name       string
		prior      string
		resolved   string
		claims     string
		wantStatus string
		wantEvent  string
		wantNote   string
		receipt    bool
	}{
		{
			name: "bare-resolved-records-a-claim-only", resolved: `,"resolved":["` + sig + `"]`,
			wantStatus: "open", wantEvent: "claim", wantNote: "claimed in review",
		},
		{
			name:       "verified-with-matching-head-addresses",
			claims:     `[{"sig":"` + sig + `","verdict":"VERIFIED","verifyCmd":"go test ./...","resultHead":"aaaaaaa"}]`,
			receipt:    true,
			wantStatus: "addressed", wantEvent: "addressed", wantNote: "go test ./...",
		},
		{
			// A hand-written claim with no verify receipt is attestation
			// nobody observed: it only logs, however well-formed it is.
			name:       "verified-without-receipt-only-logs",
			claims:     `[{"sig":"` + sig + `","verdict":"VERIFIED","verifyCmd":"go test ./...","resultHead":"aaaaaaa"}]`,
			wantStatus: "open", wantEvent: "revalidation", wantNote: "VERIFIED: go test ./...",
		},
		{
			// The head must match: a claim about another HEAD is evidence
			// about another tree, so it only logs.
			name:       "verified-wrong-head-only-logs",
			claims:     `[{"sig":"` + sig + `","verdict":"VERIFIED","verifyCmd":"go test ./...","resultHead":"ccccccc"}]`,
			receipt:    true,
			wantStatus: "open", wantEvent: "revalidation", wantNote: "VERIFIED: go test ./...",
		},
		{
			// The manual's evidence tuple is {sig, verdict, verifyCmd,
			// resultHead}: a VERIFIED claim naming no command proves nothing
			// reproducible, so it only logs even when the head matches.
			name:       "verified-without-verify-cmd-only-logs",
			claims:     `[{"sig":"` + sig + `","verdict":"VERIFIED","resultHead":"aaaaaaa"}]`,
			receipt:    true,
			wantStatus: "open", wantEvent: "revalidation", wantNote: "VERIFIED",
		},
		{
			name: "not-verified-reopens", prior: addressedPrior,
			claims:     `[{"sig":"` + sig + `","verdict":"NOT VERIFIED","verifyCmd":"go test ./..."}]`,
			wantStatus: "open", wantEvent: "reopened", wantNote: "go test ./...",
		},
		{
			name: "inconclusive-only-logs", prior: addressedPrior,
			claims:     `[{"sig":"` + sig + `","verdict":"INCONCLUSIVE","verifyCmd":"go test ./..."}]`,
			wantStatus: "addressed", wantEvent: "revalidation", wantNote: "INCONCLUSIVE: go test ./...",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			slug := "local-cl"
			writeStoreBundle(t, slug, `{"schema_version":"1","body":"`+storeBody+`","approved":false,`+
				`"comments":[`+comment+`]`+tc.resolved+`}`)
			if tc.prior != "" {
				testkit.WriteFile(t, ".verdict/state/"+slug+"/findings.json", tc.prior)
			}
			if tc.claims != "" {
				testkit.WriteFile(t, ".verdict/state/"+slug+"/verification.json", tc.claims)
			}
			if tc.receipt {
				writeVerifyReceipt(t, ".verdict/state/"+slug, storeHead)
			}
			c := &testkit.Collector{}
			if _, ref := recordcore.Run(context.Background(), recordRequest(t, c, slug)); ref != nil {
				t.Fatalf("Run refusal: %v", ref)
			}
			entry := bySig(t, storeEntries(t, slug))[sig]
			if entry == nil {
				t.Fatalf("no entry for sig %s", sig)
			}
			if entry["status"] != tc.wantStatus {
				t.Errorf("status = %v, want %v", entry["status"], tc.wantStatus)
			}
			event, note := lastHistory(t, historyOf(t, entry))
			if event != tc.wantEvent || note != tc.wantNote {
				t.Errorf("last history event = %q/%q, want %q/%q", event, note, tc.wantEvent, tc.wantNote)
			}
		})
	}
}

func lastHistory(t *testing.T, events []any) (string, string) {
	t.Helper()
	if len(events) == 0 {
		t.Fatal("entry carries no history")
	}
	last, ok := events[len(events)-1].(map[string]any)
	if !ok {
		t.Fatalf("history entry is %T, want an object", events[len(events)-1])
	}
	event, okEvent := last["event"].(string)
	note, okNote := last["note"].(string)
	if !okEvent || !okNote {
		t.Fatalf("history entry = %v, want string event and note", last)
	}
	return event, note
}

// TestRunRecordClaimsAllApplyInOneRun proves the claim kinds compose: two
// verification claims and a triage claim in the same run each land on their
// own finding. A run that stops after the first successful claim silently
// drops the rest of the operator's claims, leaving a finding at its prior
// status while the envelope reads as if every claim applied.
func TestRunRecordClaimsAllApplyInOneRun(t *testing.T) {
	const (
		// sha1("notes.txt Cited finding here with enough substance.")
		verifiedSig = "101d0761051f365731b9b857810c9b3603c50208"
		// sha1("notes.txt Second inline finding body for the store.")
		triagedSig = "cec2c320fb7d7983e6271674e0f6af6718374758"
		// sha1("notes.txt Third inline finding body for the store.")
		secondVerifiedSig = "8767c19ed9e1d2f5139277832aa7e7bb4507d448"
	)
	review := `{"schema_version":"1","body":"` + storeBody + `","approved":false,"comments":[` +
		`{"path":"notes.txt","line":2,"body":"Cited finding here with enough substance."},` +
		`{"path":"notes.txt","line":3,"body":"Second inline finding body for the store."},` +
		`{"path":"notes.txt","line":4,"body":"Third inline finding body for the store."}],` +
		`"triage":[{"sig":"` + triagedSig + `","status":"wont-fix",` +
		`"rationale":"By design: the gate owns that decision and tests cover it."}]}`
	writeStoreBundle(t, "local-cc", review)
	testkit.WriteFile(t, ".verdict/state/local-cc/verification.json",
		`[{"sig":"`+verifiedSig+`","verdict":"VERIFIED","verifyCmd":"go test ./...","resultHead":"aaaaaaa"},`+
			`{"sig":"`+secondVerifiedSig+`","verdict":"VERIFIED","verifyCmd":"go test ./...","resultHead":"aaaaaaa"}]`)
	writeVerifyReceipt(t, ".verdict/state/local-cc", storeHead)

	c := &testkit.Collector{}
	if _, ref := recordcore.Run(context.Background(), recordRequest(t, c, "local-cc")); ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	entries := bySig(t, storeEntries(t, "local-cc"))
	for _, sig := range []string{verifiedSig, secondVerifiedSig} {
		if got := entries[sig]["status"]; got != "addressed" {
			t.Errorf("finding %s status = %v, want addressed (every verification claim applies)", sig, got)
		}
		if event, _ := lastHistory(t, historyOf(t, entries[sig])); event != "addressed" {
			t.Errorf("finding %s last history event = %q, want addressed", sig, event)
		}
	}
	if got := entries[triagedSig]["status"]; got != "wont-fix" {
		t.Errorf("triaged finding status = %v, want wont-fix (a claim never blocks the next)", got)
	}
}

// TestRunRecordTriageRationaleFloor pins the triage claim gates the manual
// states: a rationale at the floor flips the status, one rune short keeps the
// prior status with a warn, and an unknown sig is ignored with a warn. A run
// that flips on a short rationale writes a status the operator never
// justified.
//
//nolint:revive // the three gates assert inline
func TestRunRecordTriageRationaleFloor(t *testing.T) {
	const (
		// sha1("notes.txt Cited finding here with enough substance.")
		sig = "101d0761051f365731b9b857810c9b3603c50208"
		// rationaleFloor is exactly minRationaleRunes (30) runes;
		// rationaleShort is one short of it.
		rationaleFloor = "By design: the gate is the rul"
		rationaleShort = "By design: the gate is the ru"
	)
	tests := []struct {
		name       string
		claimSig   string
		rationale  string
		wantStatus string
		wantWarn   string
	}{
		{"at-floor-flips", sig, rationaleFloor, "wont-fix", ""},
		{"one-rune-short-keeps", sig, rationaleShort, "open", "triage claim without rationale, kept prior status"},
		{"unknown-sig-ignored", "deadbeefdeadbeef", rationaleFloor, "open", "triage claim deadbeef ignored (not in store)"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			slug := "local-tr"
			review := `{"schema_version":"1","body":"` + storeBody + `","approved":false,` +
				`"comments":[{"path":"notes.txt","line":2,"body":"Cited finding here with enough substance."}],` +
				`"triage":[{"sig":"` + tc.claimSig + `","status":"wont-fix","rationale":"` + tc.rationale + `"}]}`
			writeStoreBundle(t, slug, review)
			c := &testkit.Collector{}
			if _, ref := recordcore.Run(context.Background(), recordRequest(t, c, slug)); ref != nil {
				t.Fatalf("Run refusal: %v", ref)
			}
			got := bySig(t, storeEntries(t, slug))[sig]["status"]
			if got != tc.wantStatus {
				t.Errorf("status = %v, want %v", got, tc.wantStatus)
			}
			if tc.wantWarn == "" {
				return
			}
			if !hasWarning(c.Warnings, tc.wantWarn) {
				t.Errorf("warnings = %v, want %q", c.Warnings, tc.wantWarn)
			}
		})
	}
}

// TestRunRecordTriageCannotAddress pins that a rationale cannot buy
// addressed: a triage[] claim for it, however long its rationale, leaves the
// finding open and warns, because addressed comes only from a VERIFIED claim
// backed by a verify receipt. The other triage states still flip.
func TestRunRecordTriageCannotAddress(t *testing.T) {
	const (
		// sha1("notes.txt Cited finding here with enough substance.")
		sig       = "101d0761051f365731b9b857810c9b3603c50208"
		rationale = "Fixed in the follow-up commit; the boundary check now covers it."
	)
	writeStoreBundle(t, "local-ta", `{"schema_version":"1","body":"`+storeBody+`","approved":false,`+
		`"comments":[{"path":"notes.txt","line":2,"body":"Cited finding here with enough substance."}],`+
		`"triage":[{"sig":"`+sig+`","status":"addressed","rationale":"`+rationale+`"}]}`)
	c := &testkit.Collector{}
	if _, ref := recordcore.Run(context.Background(), recordRequest(t, c, "local-ta")); ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if got := bySig(t, storeEntries(t, "local-ta"))[sig]["status"]; got != "open" {
		t.Errorf("status = %v, want open (a triage rationale never addresses)", got)
	}
	want := "triage claim 101d0761 ignored: addressed needs a VERIFIED claim in verification.json " +
		"backed by a verdict verify receipt, never a triage rationale"
	if !slices.Contains(c.Warnings, want) {
		t.Errorf("warnings = %q, want %q", c.Warnings, want)
	}
}

// TestRunRecordTriageClaimsCompose proves every triage claim in one run lands:
// two claims on two findings both flip. A run that stops at the first success
// silently drops the rest of the operator's triage.
func TestRunRecordTriageClaimsCompose(t *testing.T) {
	const (
		// sha1("notes.txt Cited finding here with enough substance.")
		firstSig = "101d0761051f365731b9b857810c9b3603c50208"
		// sha1("notes.txt Second inline finding body for the store.")
		secondSig = "cec2c320fb7d7983e6271674e0f6af6718374758"
		rationale = "By design: the gate is the rul"
	)
	review := `{"schema_version":"1","body":"` + storeBody + `","approved":false,"comments":[` +
		`{"path":"notes.txt","line":2,"body":"Cited finding here with enough substance."},` +
		`{"path":"notes.txt","line":3,"body":"Second inline finding body for the store."}],` +
		`"triage":[` +
		`{"sig":"` + firstSig + `","status":"wont-fix","rationale":"` + rationale + `"},` +
		`{"sig":"` + secondSig + `","status":"false-positive","rationale":"` + rationale + `"}]}`
	writeStoreBundle(t, "local-tc", review)
	c := &testkit.Collector{}
	if _, ref := recordcore.Run(context.Background(), recordRequest(t, c, "local-tc")); ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	entries := bySig(t, storeEntries(t, "local-tc"))
	if got := entries[firstSig]["status"]; got != "wont-fix" {
		t.Errorf("first claim status = %v, want wont-fix", got)
	}
	if got := entries[secondSig]["status"]; got != "false-positive" {
		t.Errorf("second claim status = %v, want false-positive (claims compose)", got)
	}
}

// TestRunRecordVerifiedNeedsReceipt pins the receipt rule: a VERIFIED claim
// with a verify command and a matching resultHead flips only when patch.json
// carries the receipt `verdict verify` writes — exit 0, not timed out, and a
// verifiedHead equal to the gated head. Every other receipt keeps the finding
// open, logs the claim, and warns with the reason and the head to verify.
func TestRunRecordVerifiedNeedsReceipt(t *testing.T) {
	const (
		// sha1("notes.txt Cited finding here with enough substance.")
		sig    = "101d0761051f365731b9b857810c9b3603c50208"
		claims = `[{"sig":"` + sig + `","verdict":"VERIFIED","verifyCmd":"go test ./...","resultHead":"aaaaaaa"}]`
		cmd    = "go test ./..."
		head   = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		tail   = " — run verdict verify on HEAD aaaaaaa, then land the claim again"
		// forged is the outcome an agent can write into patch.json, the file
		// it is told to write, without running anything.
		forged = `{"verifyCmd":"go test ./...","verifyExit":0,"timedOut":false,"verifiedHead":"` + head + `"}`
	)
	tests := []struct {
		name, receipt, patch, wantStatus, wantWarn string // "" receipt/patch: none written; "" warn: none wanted
	}{
		{name: "receipt-at-head-flips", receipt: toolReceipt(cmd, "", head, 0, false), wantStatus: "addressed"},
		{name: "receipt-for-this-sig-flips", receipt: toolReceipt(cmd, sig[:8], head, 0, false), wantStatus: "addressed"},
		{
			name: "hand-written-only", wantStatus: "open",
			wantWarn: "VERIFIED claim for 101d0761 not applied (no verify receipt: verify-receipt.json is missing)" + tail,
		},
		{
			name: "forged-patch-outcome", patch: forged, wantStatus: "open",
			wantWarn: "VERIFIED claim for 101d0761 not applied (no verify receipt: verify-receipt.json is missing)" + tail,
		},
		{
			name: "nonzero-exit", receipt: toolReceipt(cmd, "", head, 1, false), wantStatus: "open",
			wantWarn: "VERIFIED claim for 101d0761 not applied (the verify receipt failed with exit=1)" + tail,
		},
		{
			name: "timed-out", receipt: toolReceipt(cmd, "", head, 0, true), wantStatus: "open",
			wantWarn: "VERIFIED claim for 101d0761 not applied (the verify receipt timed out)" + tail,
		},
		{
			name: "other-command", receipt: toolReceipt("true", "", head, 0, false), wantStatus: "open",
			wantWarn: `VERIFIED claim for 101d0761 not applied (the verify receipt ran "true", not the claim's "go test ./...")` + tail,
		},
		{
			name: "other-finding", receipt: toolReceipt(cmd, "cec2c320fb7d", head, 0, false), wantStatus: "open",
			wantWarn: "VERIFIED claim for 101d0761 not applied (the verify receipt is for finding cec2c320)" + tail,
		},
		{
			name: "stale-head", receipt: toolReceipt(cmd, "", "cccccccccccccccccccccccccccccccccccccccc", 0, false), wantStatus: "open",
			wantWarn: "VERIFIED claim for 101d0761 not applied (the verify receipt covers HEAD ccccccc)" + tail,
		},
		{
			name: "no-clean-head", receipt: toolReceipt(cmd, "", "", 0, false), wantStatus: "open",
			wantWarn: "VERIFIED claim for 101d0761 not applied (the verify receipt records no clean HEAD)" + tail,
		},
		{
			name: "unreadable-receipt", receipt: `{"schema_version":"1","command":"go test ./...","exit":0,"extra":true}`, wantStatus: "open",
			wantWarn: `VERIFIED claim for 101d0761 not applied (the verify receipt is unreadable: json: unknown field "extra")` + tail,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			checkReceiptLeg(t, sig, claims, tc.receipt, tc.patch, tc.wantStatus, tc.wantWarn)
		})
	}
}

// checkReceiptLeg records one VERIFIED claim beside the given tool receipt
// and patch.json ("" writes neither) and checks the status, the warning, and
// the logged claim.
func checkReceiptLeg(t *testing.T, sig, claims, receipt, patch, wantStatus, wantWarn string) {
	t.Helper()
	slug := "local-vc"
	writeStoreBundle(t, slug, `{"schema_version":"1","body":"`+storeBody+`","approved":false,`+
		`"comments":[{"path":"notes.txt","line":2,"body":"Cited finding here with enough substance."}]}`)
	testkit.WriteFile(t, ".verdict/state/"+slug+"/verification.json", claims)
	for name, body := range map[string]string{"verify-receipt.json": receipt, "patch.json": patch} {
		if body != "" {
			testkit.WriteFile(t, ".verdict/state/"+slug+"/"+name, body)
		}
	}
	c := &testkit.Collector{}
	if _, ref := recordcore.Run(context.Background(), recordRequest(t, c, slug)); ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	entry := bySig(t, storeEntries(t, slug))[sig]
	if got := entry["status"]; got != wantStatus {
		t.Errorf("status = %v, want %v", got, wantStatus)
	}
	checkReceiptWarn(t, c.Warnings, entry, wantWarn)
}

// checkReceiptWarn proves an unbacked claim warns with its reason and lands
// in history as a revalidation, and a backed one does not warn.
func checkReceiptWarn(t *testing.T, warnings []string, entry map[string]any, wantWarn string) {
	t.Helper()
	if wantWarn == "" {
		if hasWarning(warnings, "not applied") {
			t.Errorf("warnings = %v, want no unbacked-claim warning", warnings)
		}
		return
	}
	if !slices.Contains(warnings, wantWarn) {
		t.Errorf("warnings = %q, want %q", warnings, wantWarn)
	}
	if event, note := lastHistory(t, historyOf(t, entry)); event != "revalidation" || note != "VERIFIED: go test ./..." {
		t.Errorf("last history = %q/%q, want the claim logged as a revalidation", event, note)
	}
}

// TestRunRecordAnchorNormalization pins the anchor range normalizer on the
// three shapes the manual calls out: a non-numeric anchor stays inline with a
// warning, a reversed span is normalized before the hunk check (so a span
// that is outside moves to the body), and a line 0 is outside like any other
// uncontained anchor.
//
// The review lists the moved comments first, so the expected store order
// (kept comments, then the moved ones) is not the input order: a merge that
// keeps input order fails below.
//
