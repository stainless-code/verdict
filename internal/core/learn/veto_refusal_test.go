package learncore

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/kb"
)

// TestRefusedVetoWritesNothing proves a veto that refuses leaves the tree
// exactly as found: no `.verdict/.gitignore`, no lock file, and the store bytes
// untouched. Every refusal is decided before the store lock is asked for, so a
// rejected human veto is not a write at all.
func TestRefusedVetoWritesNothing(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	store := filepath.Join(root, kb.File)
	if err := os.MkdirAll(filepath.Dir(store), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store, []byte(vetoSeed), 0o600); err != nil {
		t.Fatal(err)
	}
	before := dirNames(t, filepath.Dir(store))
	for _, tc := range refusedVetoes() {
		assertVetoWroteNothing(t, store, before, tc)
	}
}

// refusedVetoCase is one veto that must refuse before it writes anything.
type refusedVetoCase struct {
	req  *Request
	want string
	why  string
}

// refusedVetoes builds one refused veto per guard: the reason length, the row's
// absence, the row's state, and the id shape.
func refusedVetoes() []refusedVetoCase {
	return []refusedVetoCase{
		{want: codeBadReason, why: "short retire reason", req: &Request{Retire: "active-one", Reason: "too short"}},
		{want: codeUnknownRule, why: "unknown retire target", req: &Request{Retire: "no-such-rule", Reason: vetoReason}},
		{want: codeAlreadyRetired, why: "already retired", req: &Request{Retire: "retired-one", Reason: vetoReason}},
		{want: codeNotRetired, why: "already active", req: &Request{Restore: "active-one", Reason: vetoReason}},
		{want: codeUnknownRule, why: "unknown restore target", req: &Request{Restore: "no-such-rule", Reason: vetoReason}},
		{want: codeBadID, why: "malformed roster id", req: &Request{Retire: "Not A Slug", Reason: vetoReason}},
	}
}

// assertVetoWroteNothing proves one refused veto changed no file and no byte.
func assertVetoWroteNothing(t *testing.T, store string, before []string, tc refusedVetoCase) {
	t.Helper()
	req := tc.req
	req.Runtime = &core.Runtime{}
	_, ref := Run(t.Context(), req)
	if ref == nil || ref.Code != tc.want {
		t.Fatalf("%s = %v; want %s", tc.why, ref, tc.want)
	}
	if got := dirNames(t, filepath.Dir(store)); !slices.Equal(got, before) {
		t.Fatalf("%s wrote %v; want only %v", tc.why, got, before)
	}
	if got := storeBytes(t, store); got != vetoSeed {
		t.Fatalf("%s moved the store: %q", tc.why, got)
	}
}
