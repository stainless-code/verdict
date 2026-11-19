package journal

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
)

type stubProgress struct {
	warns []string
}

func (p *stubProgress) Human(string)  {}
func (p *stubProgress) Warn(m string) { p.warns = append(p.warns, m) }
func (p *stubProgress) Diag(string)   {}

func TestGuardWritesStartEndRedactsArgvAndSkipsReadOnly(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	stateDir := t.TempDir()
	env := map[string]string{"XDG_STATE_HOME": stateDir}
	bundleDir := filepath.Join(root, ".verdict", "state", "local-9")
	mustWriteFile(t, filepath.Join(bundleDir, "source.json"), []byte(`{"repo":"acme/app"}`), 0o644)

	t0 := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	ticks := 0
	clock := func() time.Time {
		ticks++
		return t0.Add(time.Duration(ticks) * 100 * time.Millisecond)
	}
	var warns []string
	code := Guard(&Invocation{
		Verb:     "record",
		Channel:  ChannelCLI,
		RepoRoot: root,
		Pr:       "local-9",
		Env:      env,
		Argv:     []string{"record", "acme/app", "local-9", "--token", "secret-cli-val", "--dry-run", "env-secret-12345"},
		Environ:  []string{"ANTHROPIC_API_KEY=env-secret-12345"},
		Version:  "0.14.2",
		Now:      clock,
		Warn:     func(m string) { warns = append(warns, m) },
		Warnings: func() int { return 2 },
		Next:     func() string { return "report the local bundle" },
	}, func() int {
		mustWriteFile(t, filepath.Join(bundleDir, "record.json"), []byte(`{"ok":true}`), 0o644)
		return 0
	})
	if code != 0 || len(warns) != 0 {
		t.Fatalf("Guard code=%d warns=%v, want 0 and none", code, warns)
	}
	jpath, absRepo, ok := Path(root, "local-9", env)
	if !ok {
		t.Fatal("Path returned ok=false")
	}
	assertNoRepoJournalFiles(t, bundleDir)
	assertGuardRecordResult(t, jpath, absRepo)
	assertReadOnlySkipsJournal(t, root, jpath, env)
}

func assertNoRepoJournalFiles(t *testing.T, bundleDir string) {
	t.Helper()
	for _, name := range []string{"journal.jsonl", "journal.jsonl.lock"} {
		if _, err := os.Stat(filepath.Join(bundleDir, name)); err == nil {
			t.Fatalf("found unexpected %s inside bundle dir %s", name, bundleDir)
		}
	}
}

func assertGuardRecordResult(t *testing.T, jpath, absRepo string) {
	t.Helper()
	raw, err := os.ReadFile(jpath)
	if err != nil {
		t.Fatalf("read journal: %v", err)
	}
	if strings.Contains(string(raw), "secret-cli-val") || strings.Contains(string(raw), "env-secret-12345") {
		t.Fatalf("journal leaked secret in raw file:\n%s", raw)
	}
	assertEveryLineHasRepo(t, raw, absRepo)
	sum, err := Read(jpath, "local-9")
	if err != nil || len(sum.Steps) != 1 {
		t.Fatalf("Read err=%v steps=%d, want 1", err, len(sum.Steps))
	}
	assertRecordedStepFields(t, &sum.Steps[0])
}

func assertEveryLineHasRepo(t *testing.T, raw []byte, wantRepo string) {
	t.Helper()
	for i, line := range splitWholeLines(raw) {
		var probe struct {
			Repo string `json:"repo"`
		}
		if err := json.Unmarshal(line, &probe); err != nil {
			t.Fatalf("line %d json: %v", i, err)
		}
		if probe.Repo != wantRepo {
			t.Fatalf("line %d repo = %q, want %q", i, probe.Repo, wantRepo)
		}
	}
}

func assertRecordedStepFields(t *testing.T, st *Step) {
	t.Helper()
	wantArgv := []string{"record", "acme/app", "local-9", "--token", "***REDACTED***", "--dry-run", "***REDACTED***"}
	if !slices.Equal(st.Argv, wantArgv) {
		t.Fatalf("Argv = %q, want %q", st.Argv, wantArgv)
	}
	if st.Warnings != 2 || st.Next != "report the local bundle" || len(st.Wrote) != 1 || st.Wrote[0].Path != "record.json" {
		t.Fatalf("Step = %+v, want warnings=2 next='report the local bundle' wrote=[record.json]", st)
	}
}

func assertReadOnlySkipsJournal(t *testing.T, root, jpath string, env map[string]string) {
	t.Helper()
	before, err := os.ReadFile(jpath)
	if err != nil {
		t.Fatal(err)
	}
	for _, verb := range []string{"gate", "show", "next"} {
		code := Guard(&Invocation{
			Verb:     verb,
			Channel:  ChannelCLI,
			RepoRoot: root,
			Pr:       "local-9",
			Env:      env,
			Argv:     []string{verb, "acme/app", "local-9"},
		}, func() int { return 0 })
		if code != 0 {
			t.Fatalf("%s returned %d, want 0", verb, code)
		}
	}
	after, err := os.ReadFile(jpath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("read-only verbs modified external journal")
	}
}

func TestGuardAppendFailureWarnsAndSucceeds(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	stateDir := t.TempDir()
	env := map[string]string{"XDG_STATE_HOME": stateDir}
	bundleDir := filepath.Join(root, ".verdict", "state", "local-warn")
	mustWriteFile(t, filepath.Join(bundleDir, "source.json"), []byte(`{}`), 0o644)
	jpath, _, ok := Path(root, "local-warn", env)
	if !ok {
		t.Fatal("Path returned ok=false")
	}
	if err := os.MkdirAll(filepath.Dir(jpath), 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "target.txt")
	mustWriteFile(t, outside, []byte("untouched"), 0o600)
	if err := os.Symlink(outside, jpath); err != nil {
		t.Fatal(err)
	}
	var warns []string
	code := Guard(&Invocation{
		Verb:     "report",
		Channel:  ChannelCLI,
		RepoRoot: root,
		Pr:       "local-warn",
		Env:      env,
		Argv:     []string{"report", "acme/app", "local-warn"},
		Warn:     func(m string) { warns = append(warns, m) },
	}, func() int { return 0 })
	if code != 0 {
		t.Fatalf("Guard exit code = %d, want 0 even when journal append fails", code)
	}
	wantPrefix := "journal: could not append to "
	if len(warns) == 0 || !strings.HasPrefix(warns[0], wantPrefix) {
		t.Fatalf("warns = %q, want prefix %q", warns, wantPrefix)
	}
}

func TestGuardAbsentRefusalWritesNoRepoDir(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	stateDir := t.TempDir()
	env := map[string]string{"XDG_STATE_HOME": stateDir}

	code := Guard(&Invocation{
		Verb:     "record",
		Channel:  ChannelCLI,
		RepoRoot: root,
		Pr:       "local-missing",
		Env:      env,
		Argv:     []string{"record", "acme/app", "local-missing"},
		Refusal:  func() string { return "missing-bundle" },
	}, func() int { return 1 })
	if code != 1 {
		t.Fatalf("absent refusal code = %d, want 1", code)
	}
	if _, err := os.Stat(filepath.Join(root, ".verdict")); err == nil {
		t.Fatal("Guard created .verdict directory on refusal")
	}
	missingPath, _, _ := Path(root, "local-missing", env)
	refSum, err := Read(missingPath, "local-missing")
	if err != nil || len(refSum.Steps) != 1 || refSum.Steps[0].Refusal == nil || *refSum.Steps[0].Refusal != "missing-bundle" {
		t.Fatalf("absent refusal journal = (%+v, %v), want 1 step with missing-bundle refusal", refSum, err)
	}
}

func TestGuardCreatingVerbStartsWithNilBefore(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	stateDir := t.TempDir()
	env := map[string]string{"XDG_STATE_HOME": stateDir}

	code := Guard(&Invocation{
		Verb:     "fetch-local",
		Channel:  ChannelCLI,
		RepoRoot: root,
		Pr:       "fresh",
		Env:      env,
		Argv:     []string{"fetch-local", "acme/app", "fresh"},
	}, func() int {
		mustWriteFile(t, filepath.Join(root, ".verdict", "state", "local-fresh", "source.json"), []byte(`{"repo":"acme/app"}`), 0o644)
		return 0
	})
	if code != 0 {
		t.Fatalf("fetch-local code = %d, want 0", code)
	}
	freshPath, _, _ := Path(root, "local-fresh", env)
	sum, err := Read(freshPath, "local-fresh")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(sum.Steps) != 1 || sum.Steps[0].Bundle.Before != nil || sum.Steps[0].Version != core.VersionUnknown {
		t.Fatalf("creating step = %+v, want 1 step with before=nil and version=%q", sum.Steps, core.VersionUnknown)
	}
}

// TestGuardPreSweepFailurePublishesNoFileDiff proves a broken pre-sweep is
// published as unknown instead of as a diff against an empty snapshot, which
// would claim every bundle file was written.
func TestGuardPreSweepFailurePublishesNoFileDiff(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	env := map[string]string{"XDG_STATE_HOME": t.TempDir()}
	bundleDir := loopBundleDir(t, root, "local-pre")
	var warns []string
	code := runGuarded(root, env, "local-pre", func(m string) { warns = append(warns, m) }, func() int {
		mustRemove(t, bundleDir)
		mustWriteFile(t, filepath.Join(bundleDir, "findings.json"), []byte(`{"v":1}`), 0o644)
		return 0
	})
	assertUnobservedEndLine(t, root, env, "local-pre", code, warns)
}

// TestGuardPostSweepFailurePublishesNoFileDiff proves a broken post-sweep is
// published as unknown instead of as a diff against an empty snapshot, which
// would claim every bundle file was removed.
func TestGuardPostSweepFailurePublishesNoFileDiff(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	env := map[string]string{"XDG_STATE_HOME": t.TempDir()}
	bundleDir := filepath.Join(root, ".verdict", "state", "local-post")
	mustWriteFile(t, filepath.Join(bundleDir, "source.json"), []byte(`{}`), 0o644)
	var warns []string
	code := runGuarded(root, env, "local-post", func(m string) { warns = append(warns, m) }, func() int {
		mustRemoveAll(t, bundleDir)
		mustSymlink(t, bundleDir, bundleDir)
		return 0
	})
	assertUnobservedEndLine(t, root, env, "local-post", code, warns)
}

func mustRemoveAll(t *testing.T, path string) {
	t.Helper()
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}
}

func mustRemove(t *testing.T, path string) {
	t.Helper()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
}

func mustSymlink(t *testing.T, oldname, newname string) {
	t.Helper()
	if err := os.Symlink(oldname, newname); err != nil {
		t.Fatal(err)
	}
}

func runGuarded(root string, env map[string]string, name string, warn func(string), fn func() int) int {
	return Guard(&Invocation{
		Verb: "record", Channel: ChannelCLI, RepoRoot: root, Pr: name, Env: env,
		Argv: []string{"record", "acme/app", name}, Warn: warn,
	}, fn)
}

// loopBundleDir creates a bundle directory that cannot be swept: a symlink to
// itself fails every walk with ELOOP.
func loopBundleDir(t *testing.T, root, name string) string {
	t.Helper()
	dir := filepath.Join(root, ".verdict", "state", name)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dir, dir); err != nil {
		t.Fatal(err)
	}
	return dir
}

func assertUnobservedEndLine(t *testing.T, root string, env map[string]string, name string, code int, warns []string) {
	t.Helper()
	if code != 0 {
		t.Fatalf("Guard code = %d, want 0: a failed sweep never changes the exit code", code)
	}
	if len(warns) != 1 || !strings.HasPrefix(warns[0], "journal: could not append to ") {
		t.Fatalf("warns = %q, want one journal warning for the failed sweep", warns)
	}
	jpath, _, ok := Path(root, name, env)
	if !ok {
		t.Fatal("Path returned ok=false")
	}
	assertUnobservedEndRecord(t, jpath)
	assertUnobservedFold(t, jpath, name)
}

func assertUnobservedEndRecord(t *testing.T, jpath string) {
	t.Helper()
	raw, err := os.ReadFile(jpath)
	if err != nil {
		t.Fatalf("read journal: %v", err)
	}
	lines := splitWholeLines(raw)
	var end EndLine
	if err := json.Unmarshal(lines[len(lines)-1], &end); err != nil {
		t.Fatalf("decode end line: %v", err)
	}
	if len(end.Wrote) != 0 || len(end.Removed) != 0 {
		t.Fatalf("end line published wrote=%+v removed=%v, want no file diff", end.Wrote, end.Removed)
	}
	if !end.Bundle.Unobserved || end.Bundle.Before != nil || end.Bundle.After != "" {
		t.Fatalf("end line bundle = %+v, want an unobserved bundle with no digests", end.Bundle)
	}
}

func assertUnobservedFold(t *testing.T, jpath, name string) {
	t.Helper()
	sum, err := Read(jpath, name)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(sum.Steps) != 1 || !sum.Steps[0].Bundle.Unobserved {
		t.Fatalf("folded steps = %+v, want one unobserved step", sum.Steps)
	}
	if human := strings.Join(FormatHuman(sum), "\n"); !strings.Contains(human, "file diff unknown (content sweep failed)") {
		t.Fatalf("FormatHuman = %q, want the unknown file diff called out", human)
	}
}

// TestGuardOverlappingStartsShareOneInstantStayDistinct proves the pairing key
// is invocation-unique: two starts that read the same injected clock do not
// collapse, so the call that never ended still shows as unfinished.
func TestGuardOverlappingStartsShareOneInstantStayDistinct(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	env := map[string]string{"XDG_STATE_HOME": t.TempDir()}
	mustWriteFile(t, filepath.Join(root, ".verdict", "state", "local-dup", "source.json"), []byte(`{}`), 0o644)
	frozen := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	clock := func() time.Time { return frozen }
	invocation := func(verb string) *Invocation {
		return &Invocation{
			Verb: verb, Channel: ChannelCLI, RepoRoot: root, Pr: "local-dup", Env: env,
			Argv: []string{verb, "acme/app", "local-dup"}, Now: clock,
		}
	}
	unfinished := beginGuard(invocation("record"), "record")
	finished := beginGuard(invocation("report"), "report")
	if unfinished.start.Step == finished.start.Step {
		t.Fatalf("both starts share step %q, want one key per invocation", unfinished.start.Step)
	}
	finishGuard(invocation("report"), finished, 0)

	jpath, _, ok := Path(root, "local-dup", env)
	if !ok {
		t.Fatal("Path returned ok=false")
	}
	assertOneFinishedOneUnfinished(t, jpath)
}

func assertOneFinishedOneUnfinished(t *testing.T, jpath string) {
	t.Helper()
	sum, err := Read(jpath, "local-dup")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(sum.Steps) != 1 || sum.Steps[0].Verb != "report" {
		t.Fatalf("Steps = %+v, want the one report step that ended", sum.Steps)
	}
	if len(sum.Unfinished) != 1 || sum.Unfinished[0].Verb != "record" {
		t.Fatalf("Unfinished = %+v, want the record start that never ended", sum.Unfinished)
	}
	if n := sum.UnreadableLines; n != 0 {
		t.Fatalf("UnreadableLines = %d, want 0", n)
	}
}

func TestCountingProgressCountsAndForwards(t *testing.T) {
	t.Parallel()
	inner := &stubProgress{}
	cp := &CountingProgress{Inner: inner}
	cp.Human("hi")
	cp.Diag("diag")
	cp.Warn("w1")
	cp.Warn("w2")
	if cp.Count() != 2 || !slices.Equal(inner.warns, []string{"w1", "w2"}) {
		t.Fatalf("CountingProgress count=%d warns=%v, want 2 and [w1 w2]", cp.Count(), inner.warns)
	}
}
