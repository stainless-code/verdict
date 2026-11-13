package readgolden_test

import (
	"testing"
)

// showBundle writes a store with two sigs sharing an 8-char prefix (the
// ambiguous leg) plus a null-sig entry (the guard leg).
func showBundle(t *testing.T, dir, pr string) {
	t.Helper()
	mkPRBundle(t, dir, pr)
	writeFiles(t, dir, map[string]string{
		".verdict/state/" + pr + "/findings.json": findingsDoc(
			`{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,`+
				`"anchored":true,"body":"Usable body here with enough substance.",`+
				`"suggestion":null,"inBounds":true,"rigor":"walked","severity":"critical",`+
				`"sig":null,"sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa",`+
				`"status":"open","rationale":"","history":[]}`,
			finding(sigShowA, "open", "critical", "walked", "inline"),
			finding(sigShowB, "addressed", "important", "pointed", "inline"),
		),
	})
}

// TestNextGolden pins the next verb: the top-ranked
// finding, the empty store, the status filter, and the corrupt-store
// replay (a failed sub-report reads as unreadable, byte-exact).
func TestNextGolden(t *testing.T) {
	t.Parallel()

	mixed := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPRBundle(t, dir, "5101")
		writeFiles(t, dir, map[string]string{
			".verdict/state/5101/findings.json": mixedFindings(),
		})
	}

	goArgs := splitRepo("next", "5101")
	goldenRead(t, "next/human", mixed, nil, nil, goArgs...)

	goArgs = splitRepo("next", "5101", "--json")
	goldenRead(t, "next/json", mixed, nil, nil, goArgs...)

	empty := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPRBundle(t, dir, "5101")
		writeFiles(t, dir, map[string]string{
			".verdict/state/5101/findings.json": `[]`,
		})
	}
	goArgs = splitRepo("next", "5101", "--json")
	goldenRead(t, "next/empty-json", empty, nil, nil, goArgs...)

	goArgs = splitRepo("next", "5101")
	goldenRead(t, "next/empty-human", empty, nil, nil, goArgs...)

	goArgs = splitRepo("next", "5101", "--status", "addressed")
	goldenRead(t, "next/status-addressed", mixed, nil, nil, goArgs...)

	corrupt := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPRBundle(t, dir, "5101")
		writeFiles(t, dir, map[string]string{
			".verdict/state/5101/findings.json": `[42]`,
		})
	}
	goArgs = splitRepo("next", "5101", "--json")
	goldenRead(t, "next/corrupt-json", corrupt, nil, nil, goArgs...)

	goArgs = splitRepo("next", "5101")
	goldenRead(t, "next/corrupt-human", corrupt, nil, nil, goArgs...)

	// An invalid status never reaches the store: bash's sub-report
	// validates --status and fails closed, so next reads the failed
	// replay as unreadable (exit 1), never as an empty ranking.
	goArgs = splitRepo("next", "5101", "--status", "bogus")
	goldenRead(t, "next/bad-status-human", mixed, nil, nil, goArgs...)

	goArgs = splitRepo("next", "5101", "--status", "bogus", "--json")
	goldenRead(t, "next/bad-status-json", mixed, nil, nil, goArgs...)

	// A truncated store restates its fetch receipt over the envelope:
	// the found ranking counts itself, and an empty ranking is a success
	// envelope with truncated:true returned:0 — not a plain miss.
	truncated := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPRBundle(t, dir, "5101")
		writeFiles(t, dir, map[string]string{
			".verdict/state/5101/findings.json":   mixedFindings(),
			".verdict/state/5101/truncation.json": truncationReceipt,
		})
	}
	goArgs = splitRepo("next", "5101", "--json")
	goldenRead(t, "next/truncated-json", truncated, nil, nil, goArgs...)

	empty = func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPRBundle(t, dir, "5101")
		writeFiles(t, dir, map[string]string{
			".verdict/state/5101/findings.json":   `[]`,
			".verdict/state/5101/truncation.json": truncationReceipt,
		})
	}
	goArgs = splitRepo("next", "5101", "--json")
	goldenRead(t, "next/truncated-empty-json", empty, nil, nil, goArgs...)
}

// truncationReceipt is the exact bytes fetch writes when a source scan stops
// at its page cap: 100 files listed, 12 expected, no continuation.
const truncationReceipt = "{\n  \"truncated\": true,\n  \"truncation_reason\": \"scan_cap\",\n" +
	"  \"returned\": 100,\n  \"total\": 12,\n  \"next_offset\": null\n}\n"

// TestShowGolden pins the show verb: the full record, the
// unknown and ambiguous prefixes, the missing flag, the corrupt store,
// and the null-sig guard (a null sig never matches, never kills).
func TestShowGolden(t *testing.T) {
	t.Parallel()

	show := func(t *testing.T, dir, _ string) {
		t.Helper()
		showBundle(t, dir, "5101")
	}

	goArgs := splitRepo("show", "5101", "--sig", sigShowA[:8])
	goldenRead(t, "show/human", show, nil, nil, goArgs...)

	goArgs = splitRepo("show", "5101", "--sig", sigShowA[:8], "--json")
	goldenRead(t, "show/json", show, nil, nil, goArgs...)

	goArgs = splitRepo("show", "5101", "--sig", "deadbeef", "--json")
	goldenRead(t, "show/unknown-sig", show, nil, nil, goArgs...)

	goArgs = splitRepo("show", "5101", "--sig", "a1111111", "--json")
	goldenRead(t, "show/ambiguous-sig", show, nil, nil, goArgs...)

	goArgs = splitRepo("show", "5101", "--json")
	goldenRead(t, "show/missing-sig", show, nil, nil, goArgs...)

	// A truncated store answers a miss with the receipt instead of the
	// unknown-sig refusal: the sig may exist past the scan cap, so the
	// envelope says truncated:true returned:0 with item and fix null.
	truncatedShow := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPRBundle(t, dir, "5101")
		writeFiles(t, dir, map[string]string{
			".verdict/state/5101/findings.json":   findingsDoc(finding(sigShowA, "open", "critical", "walked", "inline")),
			".verdict/state/5101/truncation.json": truncationReceipt,
		})
	}
	goArgs = splitRepo("show", "5101", "--sig", "deadbeef", "--json")
	goldenRead(t, "show/truncated-miss-json", truncatedShow, nil, nil, goArgs...)

	goArgs = splitRepo("show", "5101", "--sig", sigShowA[:8], "--json")
	goldenRead(t, "show/truncated-hit-json", truncatedShow, nil, nil, goArgs...)

	corrupt := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPRBundle(t, dir, "5101")
		writeFiles(t, dir, map[string]string{
			".verdict/state/5101/findings.json": `{"not":"a list"}`,
		})
	}
	goArgs = splitRepo("show", "5101", "--sig", "a1111111", "--json")
	goldenRead(t, "show/corrupt-store", corrupt, nil, nil, goArgs...)
}
