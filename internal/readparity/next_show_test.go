package readparity_test

import (
	"testing"
)

// showBundle writes a store with two sigs sharing an 8-char prefix (the
// ambiguous leg) plus a null-sig entry (the guard leg).
func showBundle(t *testing.T, dir, pr string) {
	t.Helper()
	mkPRBundle(t, dir, pr)
	writeFiles(t, dir, map[string]string{
		".review/" + pr + "/findings.json": findingsDoc(
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

// TestNextParity proves the next verb matches bash: the top-ranked
// finding, the empty store, the status filter, and the corrupt-store
// replay (a failed sub-report reads as unreadable, byte-exact).
func TestNextParity(t *testing.T) {
	t.Parallel()

	mixed := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPRBundle(t, dir, "5101")
		writeFiles(t, dir, map[string]string{
			".review/5101/findings.json": mixedFindings(),
		})
	}

	bash, goArgs := splitRepo("next", "5101")
	runReadBoth(t, "next/human", mixed, nil, nil, bash, goArgs)

	bash, goArgs = splitRepo("next", "5101", "--json")
	runReadBoth(t, "next/json", mixed, nil, nil, bash, goArgs)

	empty := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPRBundle(t, dir, "5101")
		writeFiles(t, dir, map[string]string{
			".review/5101/findings.json": `[]`,
		})
	}
	bash, goArgs = splitRepo("next", "5101", "--json")
	runReadBoth(t, "next/empty-json", empty, nil, nil, bash, goArgs)

	bash, goArgs = splitRepo("next", "5101")
	runReadBoth(t, "next/empty-human", empty, nil, nil, bash, goArgs)

	bash, goArgs = splitRepo("next", "5101", "--status", "addressed")
	runReadBoth(t, "next/status-addressed", mixed, nil, nil, bash, goArgs)

	corrupt := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPRBundle(t, dir, "5101")
		writeFiles(t, dir, map[string]string{
			".review/5101/findings.json": `[42]`,
		})
	}
	bash, goArgs = splitRepo("next", "5101", "--json")
	runReadBoth(t, "next/corrupt-json", corrupt, nil, nil, bash, goArgs)

	bash, goArgs = splitRepo("next", "5101")
	runReadBoth(t, "next/corrupt-human", corrupt, nil, nil, bash, goArgs)

	// An invalid status never reaches the store: bash's sub-report
	// validates --status and fails closed, so next reads the failed
	// replay as unreadable (exit 1), never as an empty ranking.
	bash, goArgs = splitRepo("next", "5101", "--status", "bogus")
	runReadBoth(t, "next/bad-status-human", mixed, nil, nil, bash, goArgs)

	bash, goArgs = splitRepo("next", "5101", "--status", "bogus", "--json")
	runReadBoth(t, "next/bad-status-json", mixed, nil, nil, bash, goArgs)
}

// TestShowParity proves the show verb matches bash: the full record, the
// unknown and ambiguous prefixes, the missing flag, the corrupt store,
// and the null-sig guard (a null sig never matches, never kills).
func TestShowParity(t *testing.T) {
	t.Parallel()

	show := func(t *testing.T, dir, _ string) {
		t.Helper()
		showBundle(t, dir, "5101")
	}

	bash, goArgs := splitRepo("show", "5101", "--sig", sigShowA[:8])
	runReadBoth(t, "show/human", show, nil, nil, bash, goArgs)

	bash, goArgs = splitRepo("show", "5101", "--sig", sigShowA[:8], "--json")
	runReadBoth(t, "show/json", show, nil, nil, bash, goArgs)

	bash, goArgs = splitRepo("show", "5101", "--sig", "deadbeef", "--json")
	runReadBoth(t, "show/unknown-sig", show, nil, nil, bash, goArgs)

	bash, goArgs = splitRepo("show", "5101", "--sig", "a1111111", "--json")
	runReadBoth(t, "show/ambiguous-sig", show, nil, nil, bash, goArgs)

	bash, goArgs = splitRepo("show", "5101", "--json")
	runReadBoth(t, "show/missing-sig", show, nil, nil, bash, goArgs)

	corrupt := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPRBundle(t, dir, "5101")
		writeFiles(t, dir, map[string]string{
			".review/5101/findings.json": `{"not":"a list"}`,
		})
	}
	bash, goArgs = splitRepo("show", "5101", "--sig", "a1111111", "--json")
	runReadBoth(t, "show/corrupt-store", corrupt, nil, nil, bash, goArgs)
}
