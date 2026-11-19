package readgolden_test

import (
	"os"
	"path/filepath"
	"testing"

	journalcore "github.com/stainless-code/verdict/internal/core/journal"
	"github.com/stainless-code/verdict/internal/golden"
)

func seedExternalJournal(t *testing.T, dir, stateDir, bundleName, content string) {
	t.Helper()
	jpath, _, ok := journalcore.Path(dir, bundleName, map[string]string{"XDG_STATE_HOME": stateDir})
	if !ok {
		t.Fatalf("journal.Path(%q, %q) returned ok=false", dir, bundleName)
	}
	if err := os.MkdirAll(filepath.Dir(jpath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(jpath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func journalPopulated(t *testing.T, dir, stateDir string) {
	t.Helper()
	mkLocalBundle(t, dir, "local-x")
	seedExternalJournal(t, dir, stateDir, "local-x",
		`{"schema_version":"1","phase":"start","repo":"/repo/app","step":"2026-11-19T10:03:12.000000Z","verb":"fetch-local","channel":"cli","version":"0.14.2","started_at":"2026-11-19T10:03:12.000000Z","argv":["fetch-local","acme/app","local-x"],"pid":51001}`+"\n"+
			`{"schema_version":"1","phase":"end","repo":"/repo/app","step":"2026-11-19T10:03:12.000000Z","verb":"fetch-local","channel":"cli","started_at":"2026-11-19T10:03:12.000000Z","ended_at":"2026-11-19T10:03:12.250000Z","refusal":null,"next":"run the review","bundle":{"before":null,"after":"1111111111111111111111111111111111111111111111111111111111111111"},"wrote":[{"path":"full.diff","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","bytes":42}],"removed":[],"pid":51001,"exit":0,"warnings":0}`+"\n"+
			`{"schema_version":"1","phase":"start","repo":"/repo/app","step":"2026-11-19T10:05:44.000000Z","verb":"record","channel":"cli","version":"0.14.2","started_at":"2026-11-19T10:05:44.000000Z","argv":["record","acme/app","local-x","--dry-run"],"pid":51002}`+"\n"+
			`{"schema_version":"1","phase":"end","repo":"/repo/app","step":"2026-11-19T10:05:44.000000Z","verb":"record","channel":"cli","started_at":"2026-11-19T10:05:44.000000Z","ended_at":"2026-11-19T10:05:44.500000Z","refusal":"review-empty","next":"","bundle":{"before":"1111111111111111111111111111111111111111111111111111111111111111","after":"1111111111111111111111111111111111111111111111111111111111111111"},"wrote":[],"removed":[],"pid":51002,"exit":1,"warnings":2}`+"\n")
}

func journalEmpty(t *testing.T, dir, _ string) {
	t.Helper()
	mkLocalBundle(t, dir, "local-x")
}

func journalCrashAndBreak(t *testing.T, dir, stateDir string) {
	t.Helper()
	mkLocalBundle(t, dir, "local-x")
	seedExternalJournal(t, dir, stateDir, "local-x",
		`{"schema_version":"1","phase":"start","repo":"/repo/app","step":"2026-11-19T10:03:12.000000Z","verb":"fetch-local","channel":"cli","version":"0.14.2","started_at":"2026-11-19T10:03:12.000000Z","argv":["fetch-local","acme/app","local-x"],"pid":51001}`+"\n"+
			`{"schema_version":"1","phase":"end","repo":"/repo/app","step":"2026-11-19T10:03:12.000000Z","verb":"fetch-local","channel":"cli","started_at":"2026-11-19T10:03:12.000000Z","ended_at":"2026-11-19T10:03:12.100000Z","refusal":null,"next":"","bundle":{"before":null,"after":"1111111111111111111111111111111111111111111111111111111111111111"},"wrote":[],"removed":[],"pid":51001,"exit":0,"warnings":0}`+"\n"+
			`{"schema_version":"1","phase":"start","repo":"/repo/app","step":"2026-11-19T10:04:00.000000Z","verb":"record","channel":"cli","version":"0.14.2","started_at":"2026-11-19T10:04:00.000000Z","argv":["record","acme/app","local-x"],"pid":51002}`+"\n"+
			`{"schema_version":"1","phase":"end","repo":"/repo/app","step":"2026-11-19T10:04:00.000000Z","verb":"record","channel":"cli","started_at":"2026-11-19T10:04:00.000000Z","ended_at":"2026-11-19T10:04:00.200000Z","refusal":null,"next":"","bundle":{"before":"2222222222222222222222222222222222222222222222222222222222222222","after":"3333333333333333333333333333333333333333333333333333333333333333"},"wrote":[],"removed":[],"pid":51002,"exit":0,"warnings":0}`+"\n"+
			`{"schema_version":"1","phase":"start","repo":"/repo/app","step":"2026-11-19T10:05:00.000000Z","verb":"report","channel":"cli","version":"0.14.2","started_at":"2026-11-19T10:05:00.000000Z","argv":["report","acme/app","local-x"],"pid":51003}`+"\n")
}

func journalArgs(flags ...string) []string {
	return append([]string{"journal", "acme/app", "local-x"}, flags...)
}

func goldenJournal(t *testing.T, name string, seed func(t *testing.T, dir, stateDir string), args ...string) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		t.Helper()
		t.Parallel()
		stateDir := t.TempDir()
		c := &golden.Case{
			Name: name,
			Env:  map[string]string{"XDG_STATE_HOME": stateDir},
			Setup: func(t *testing.T, dir, _ string) {
				t.Helper()
				if seed != nil {
					seed(t, dir, stateDir)
				}
			},
			Args:     args,
			ReadOnly: true,
		}
		c.Run(t, harness)
	})
}

func TestJournalGolden(t *testing.T) {
	t.Parallel()

	goldenJournal(t, "journal/human", journalPopulated, journalArgs()...)
	goldenJournal(t, "journal/json", journalPopulated, journalArgs("--json")...)
	goldenJournal(t, "journal/empty", journalEmpty, journalArgs()...)
	goldenJournal(t, "journal/empty-json", journalEmpty, journalArgs("--json")...)
	goldenJournal(t, "journal/crash-break", journalCrashAndBreak, journalArgs()...)
	goldenJournal(t, "journal/crash-break-json", journalCrashAndBreak, journalArgs("--json")...)
	goldenJournal(t, "journal/bad-flag", journalEmpty, journalArgs("--bogus")...)
	goldenJournal(t, "journal/missing-bundle", nil, journalArgs("--json")...)
}
