package adoptcore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// TestNumVal proves the anchor reader accepts decoded JSON numbers and
// strict digit strings, and refuses signs, fractions, trailing junk, and
// overflow instead of wrapping.
func TestNumVal(t *testing.T) {
	t.Parallel()
	maxInt := int(^uint(0) >> 1)
	for _, tc := range []struct { //nolint:govet // table layout prioritizes readability
		name string
		in   any
		want int
		ok   bool
	}{
		{"json number", json.Number("2"), 2, true},
		{"constructed number", jq.Number("3"), 3, true},
		{"digit string", "007", 7, true},
		{"empty", "", 0, false},
		{"negative", "-3", 0, false},
		{"plus sign", "+3", 0, false},
		{"fraction", "1.5", 0, false},
		{"exponent", "1e3", 0, false},
		{"trailing junk", "12abc", 0, false},
		{"max int", strconv.Itoa(maxInt), maxInt, true},
		{"max int64 plus one", "9223372036854775808", 0, false},
		{"20 digits", "99999999999999999999", 0, false},
		{"number overflow", jq.Number("99999999999999999999"), 0, false},
		{"bool", true, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := numVal(tc.in)
			if got != tc.want || ok != tc.ok {
				t.Errorf("numVal(%v) = %d, %v; want %d, %v", tc.in, got, ok, tc.want, tc.ok)
			}
		})
	}
}

// TestParseHunkRowOverflow proves a corrupt hunks.tsv row with an
// unparseable number is skipped, never wrapped into a bogus range.
func TestParseHunkRowOverflow(t *testing.T) {
	t.Parallel()
	if h, ok := parseHunkRow("notes.txt\tNEW\t002\t3"); !ok || h.Start != 2 || h.End != 3 {
		t.Errorf("leading zeros row = %+v, %v; want start 2 end 3", h, ok)
	}
	for _, row := range []string{
		"notes.txt\tNEW\t99999999999999999999\t3",
		"notes.txt\tNEW\t1\t9223372036854775808",
	} {
		if h, ok := parseHunkRow(row); ok {
			t.Errorf("overflow row %q = %+v, true; want skipped", row, h)
		}
	}
}

// TestAdoptEntriesBackfillsRigorSource proves handed-off findings carry
// rigor_source without waiting for a later post merge: a missing source is
// backfilled from the recorded rung, and an explicit source is never
// clobbered.
func TestAdoptEntriesBackfillsRigorSource(t *testing.T) {
	t.Parallel()
	entries := []any{
		jq.NewObj("rigor", "pointed", "path", "notes.txt"),
		jq.NewObj("rigor", "walked", "rigor_source", "model", "path", "notes.txt"),
	}
	stamped := adoptEntries(entries, "aaaaaaa", "2026-11-02T12:00:00Z", "adopt1", nil)
	m0, ok := stamped[0].(*jq.Obj)
	if !ok {
		t.Fatalf("adopted entry 0 is not an object: %T", stamped[0])
	}
	got0, _ := m0.Get("rigor_source")
	if got0 != "default" {
		t.Errorf("pointed entry rigor_source = %v, want default", got0)
	}
	m1, ok := stamped[1].(*jq.Obj)
	if !ok {
		t.Fatalf("adopted entry 1 is not an object: %T", stamped[1])
	}
	got1, _ := m1.Get("rigor_source")
	if got1 != "model" {
		t.Errorf("model entry rigor_source = %v, want model", got1)
	}
}

// testAdoptDiff is the minimal patch checkAdoptTarget needs beside the store.
const testAdoptDiff = "diff --git a/notes.txt b/notes.txt\n" +
	"index 1111111..2222222 100644\n" +
	"--- a/notes.txt\n+++ b/notes.txt\n" +
	"@@ -1,3 +1,3 @@\n top\n-old line\n+new line\n bottom\n"

// mkAdoptTarget writes one PR target bundle with the given PR record.
func mkAdoptTarget(t *testing.T, record string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "PR.json"), []byte(record), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "full.diff"), []byte(testAdoptDiff), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestAdoptTargetHostGate proves adopt hands findings to every host verdict can
// post to and refuses the rest with the host named, so the gate follows the
// capability table instead of one hard-coded host.
//
//nolint:revive // the provider matrix reads as one block
func TestAdoptTargetHostGate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		provider string
		adopts   bool
	}{
		{"github", true},
		{"gitlab", true},
		{"gitcafe", false},
		{"forgejo", false},
		{"bitbucket", false},
		{"azure", false},
	}
	for _, tc := range cases {
		t.Run(tc.provider, func(t *testing.T) {
			t.Parallel()
			dir := mkAdoptTarget(t, `{"state":"OPEN","provider":"`+tc.provider+`"}`)
			ref := (&state{}).checkAdoptTarget(dir)
			if tc.adopts {
				if ref != nil {
					t.Fatalf("%s target refused: %+v", tc.provider, ref)
				}
				return
			}
			assertHostRefusal(t, tc.provider, ref)
		})
	}
}

// assertHostRefusal proves one host's refusal is the typed provider gate and
// names that host, so the message cannot degrade to a generic sentence.
func assertHostRefusal(t *testing.T, provider string, ref *core.RefusalError) {
	t.Helper()
	if ref == nil {
		t.Fatalf("%s target was accepted, want a typed refusal", provider)
	}
	if ref.Code != "provider-unimplemented" {
		t.Errorf("%s refusal code = %q, want provider-unimplemented", provider, ref.Code)
	}
	if !strings.Contains(ref.Message, `"`+provider+`"`) {
		t.Errorf("%s refusal message does not name the host: %q", provider, ref.Message)
	}
}

// TestAdoptRefusalListsPostableHosts proves the refusal names the
// capability-derived postable set, so a hard-coded host list cannot pass.
func TestAdoptRefusalListsPostableHosts(t *testing.T) {
	t.Parallel()
	var want []string
	for _, p := range []string{"azure", "bitbucket", "forgejo", "gitcafe", "github", "gitlab"} {
		if core.CapabilitiesFor(p).Post {
			want = append(want, p)
		}
	}
	dir := mkAdoptTarget(t, `{"state":"OPEN","provider":"gitcafe"}`)
	ref := (&state{}).checkAdoptTarget(dir)
	if ref == nil {
		t.Fatal("gitcafe target was accepted, want a typed refusal")
	}
	if list := "(" + strings.Join(want, ", ") + ")"; !strings.Contains(ref.Message, list) {
		t.Errorf("refusal message %q does not list the postable hosts %q", ref.Message, list)
	}
}

// TestAdoptRefusalWritesNothing proves a refused adopt leaves the target
// bundle byte-identical: the provider gate runs before the store write.
func TestAdoptRefusalWritesNothing(t *testing.T) {
	t.Parallel()
	dir := mkAdoptTarget(t, `{"state":"OPEN","provider":"gitcafe"}`)
	before := dirSnapshot(t, dir)
	ref := (&state{}).checkAdoptTarget(dir)
	if ref == nil || ref.Code != "provider-unimplemented" {
		t.Fatalf("gitcafe target refusal = %+v, want provider-unimplemented", ref)
	}
	after := dirSnapshot(t, dir)
	if !reflect.DeepEqual(before, after) {
		t.Errorf("refused adopt mutated the target: before %q, after %q", before, after)
	}
}

// dirSnapshot maps each file under dir to its bytes.
func dirSnapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		out[rel] = string(raw)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestAdoptTargetWithoutProviderMarkerIsGitHub proves a bundle that predates
// the provider marker still adopts: the missing marker reads as GitHub.
func TestAdoptTargetWithoutProviderMarkerIsGitHub(t *testing.T) {
	t.Parallel()
	dir := mkAdoptTarget(t, `{"state":"OPEN"}`)
	if ref := (&state{}).checkAdoptTarget(dir); ref != nil {
		t.Fatalf("marker-less target refused: %+v", ref)
	}
}
