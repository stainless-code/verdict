package readgolden_test

import (
	"testing"
)

func journalPopulated(t *testing.T, dir, _ string) {
	t.Helper()
	mkLocalBundle(t, dir, "local-x")
	writeFiles(t, dir, map[string]string{
		".verdict/state/local-x/journal.jsonl": `{"schema_version":"1","phase":"start","step":"2026-11-19T10:03:12.000000Z","verb":"fetch-local","channel":"cli","version":"0.14.2","started_at":"2026-11-19T10:03:12.000000Z","argv":["fetch-local","acme/app","local-x"],"pid":51001}` + "\n" +
			`{"schema_version":"1","phase":"end","step":"2026-11-19T10:03:12.000000Z","verb":"fetch-local","channel":"cli","started_at":"2026-11-19T10:03:12.000000Z","ended_at":"2026-11-19T10:03:12.250000Z","refusal":null,"next":"run the review","bundle":{"before":null,"after":"1111111111111111111111111111111111111111111111111111111111111111"},"wrote":[{"path":"full.diff","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","bytes":42}],"removed":[],"pid":51001,"exit":0,"warnings":0}` + "\n" +
			`{"schema_version":"1","phase":"start","step":"2026-11-19T10:05:44.000000Z","verb":"record","channel":"cli","version":"0.14.2","started_at":"2026-11-19T10:05:44.000000Z","argv":["record","acme/app","local-x","--dry-run"],"pid":51002}` + "\n" +
			`{"schema_version":"1","phase":"end","step":"2026-11-19T10:05:44.000000Z","verb":"record","channel":"cli","started_at":"2026-11-19T10:05:44.000000Z","ended_at":"2026-11-19T10:05:44.500000Z","refusal":"review-empty","next":"","bundle":{"before":"1111111111111111111111111111111111111111111111111111111111111111","after":"1111111111111111111111111111111111111111111111111111111111111111"},"wrote":[],"removed":[],"pid":51002,"exit":1,"warnings":2}` + "\n",
	})
}

func journalEmpty(t *testing.T, dir, _ string) {
	t.Helper()
	mkLocalBundle(t, dir, "local-x")
}

func journalCrashAndBreak(t *testing.T, dir, _ string) {
	t.Helper()
	mkLocalBundle(t, dir, "local-x")
	writeFiles(t, dir, map[string]string{
		".verdict/state/local-x/journal.jsonl": `{"schema_version":"1","phase":"start","step":"2026-11-19T10:03:12.000000Z","verb":"fetch-local","channel":"cli","version":"0.14.2","started_at":"2026-11-19T10:03:12.000000Z","argv":["fetch-local","acme/app","local-x"],"pid":51001}` + "\n" +
			`{"schema_version":"1","phase":"end","step":"2026-11-19T10:03:12.000000Z","verb":"fetch-local","channel":"cli","started_at":"2026-11-19T10:03:12.000000Z","ended_at":"2026-11-19T10:03:12.100000Z","refusal":null,"next":"","bundle":{"before":null,"after":"1111111111111111111111111111111111111111111111111111111111111111"},"wrote":[],"removed":[],"pid":51001,"exit":0,"warnings":0}` + "\n" +
			`{"schema_version":"1","phase":"start","step":"2026-11-19T10:04:00.000000Z","verb":"record","channel":"cli","version":"0.14.2","started_at":"2026-11-19T10:04:00.000000Z","argv":["record","acme/app","local-x"],"pid":51002}` + "\n" +
			`{"schema_version":"1","phase":"end","step":"2026-11-19T10:04:00.000000Z","verb":"record","channel":"cli","started_at":"2026-11-19T10:04:00.000000Z","ended_at":"2026-11-19T10:04:00.200000Z","refusal":null,"next":"","bundle":{"before":"2222222222222222222222222222222222222222222222222222222222222222","after":"3333333333333333333333333333333333333333333333333333333333333333"},"wrote":[],"removed":[],"pid":51002,"exit":0,"warnings":0}` + "\n" +
			`{"schema_version":"1","phase":"start","step":"2026-11-19T10:05:00.000000Z","verb":"report","channel":"cli","version":"0.14.2","started_at":"2026-11-19T10:05:00.000000Z","argv":["report","acme/app","local-x"],"pid":51003}` + "\n",
	})
}

func journalArgs(flags ...string) []string {
	return append([]string{"journal", "acme/app", "local-x"}, flags...)
}

func TestJournalGolden(t *testing.T) {
	t.Parallel()

	goldenRead(t, "journal/human", journalPopulated, nil, nil, journalArgs()...)
	goldenRead(t, "journal/json", journalPopulated, nil, nil, journalArgs("--json")...)
	goldenRead(t, "journal/empty", journalEmpty, nil, nil, journalArgs()...)
	goldenRead(t, "journal/empty-json", journalEmpty, nil, nil, journalArgs("--json")...)
	goldenRead(t, "journal/crash-break", journalCrashAndBreak, nil, nil, journalArgs()...)
	goldenRead(t, "journal/crash-break-json", journalCrashAndBreak, nil, nil, journalArgs("--json")...)
	goldenRead(t, "journal/bad-flag", journalEmpty, nil, nil, journalArgs("--bogus")...)
	goldenRead(t, "journal/missing-bundle", nil, nil, nil, journalArgs("--json")...)
}
