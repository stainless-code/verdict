package fetchcore

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

const (
	incrementalHead = "head111"
	incrementalBase = "base111"
)

// writeIncrementalPR plants the one PR field the compare reads.
func writeIncrementalPR(t *testing.T, dir, head string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "PR.json"), []byte(`{"provider":"gitlab","head_sha":"`+head+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestGitLabCompareDiff pins the compare-to-unified join against literals for
// a modified file, a rename, a new file, and a deletion: every section keeps
// its header, the rename keeps both sides, and dev-null falls back to the
// surviving path.
func TestGitLabCompareDiff(t *testing.T) {
	t.Parallel()
	entries := []any{
		map[string]any{
			"old_path": "one.txt", "new_path": "one.txt",
			"diff": "@@ -1 +1 @@\n-a\n+b\n",
		},
		map[string]any{
			"old_path": "old.go", "new_path": "new.go", "renamed_file": true,
			"diff": "@@ -1,2 +1,2 @@\n top\n-gone\n+added\n",
		},
		map[string]any{
			"old_path": "/dev/null", "new_path": "fresh.txt", "new_file": true,
			"diff": "@@ -0,0 +1 @@\n+hello\n",
		},
		map[string]any{
			"old_path": "gone.txt", "new_path": "/dev/null", "deleted_file": true,
			"diff": "@@ -1 +0,0 @@\n-bye\n",
		},
	}
	want := "diff --git a/one.txt b/one.txt\n@@ -1 +1 @@\n-a\n+b\n\n" +
		"diff --git a/old.go b/new.go\n@@ -1,2 +1,2 @@\n top\n-gone\n+added\n\n" +
		"diff --git a/fresh.txt b/fresh.txt\n@@ -0,0 +1 @@\n+hello\n\n" +
		"diff --git a/gone.txt b/gone.txt\n@@ -1 +0,0 @@\n-bye\n"
	if got := gitlabCompareDiff(entries); got != want {
		t.Fatalf("compare diff:\n%q\nwant:\n%q", got, want)
	}
}

// TestGitLabIncrementalWritesCompare proves the compare call's exact argv,
// the converted incremental.diff bytes, the empty error sidecar, and the
// returned base.
func TestGitLabIncrementalWritesCompare(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeIncrementalPR(t, dir, incrementalHead)
	payload := `{"diffs":[{"old_path":"notes.txt","new_path":"notes.txt",` +
		`"new_file":false,"renamed_file":false,"deleted_file":false,` +
		`"diff":"@@ -1,3 +1,3 @@\n top\n-old line\n+new line\n bottom\n"}]}`
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{
		"glab\x00api\x00projects/11/repository/compare?from=" + incrementalBase + "&to=" + incrementalHead: {stdout: payload},
	}}
	s, _ := newTestSession(cmdr)
	got, err := gitlabIncremental(context.Background(), s, dir, "11",
		&core.FetchFlags{BaseSHA: incrementalBase, HasBase: true})
	if err != nil {
		t.Fatalf("gitlabIncremental: %v", err)
	}
	if got != incrementalBase {
		t.Errorf("incremental base = %q, want %q", got, incrementalBase)
	}
	assertOneCall(t, cmdr, 0, []string{
		"glab", "api",
		"projects/11/repository/compare?from=" + incrementalBase + "&to=" + incrementalHead,
	})
	want := "diff --git a/notes.txt b/notes.txt\n@@ -1,3 +1,3 @@\n top\n-old line\n+new line\n bottom\n"
	raw, rerr := os.ReadFile(filepath.Join(dir, "incremental.diff"))
	if rerr != nil {
		t.Fatal(rerr)
	}
	if string(raw) != want {
		t.Errorf("incremental.diff = %q, want %q", raw, want)
	}
	if sidecar := readOptional(filepath.Join(dir, ".compare.err")); sidecar != "" {
		t.Errorf(".compare.err = %q, want empty", sidecar)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "incremental.diff.note")); !os.IsNotExist(statErr) {
		t.Errorf("a successful compare wrote a note (err=%v)", statErr)
	}
}

// TestGitLabIncrementalFailWritesNote proves a failed compare keeps the fetch
// alive: the note names glab's stderr head, the delta is absent, and the error
// sidecar holds the same text.
func TestGitLabIncrementalFailWritesNote(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeIncrementalPR(t, dir, incrementalHead)
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{
		"glab\x00api\x00projects/11/repository/compare?from=" + incrementalBase + "&to=" + incrementalHead: {
			stderr: "glab: compare unavailable", fail: true,
		},
	}}
	s, _ := newTestSession(cmdr)
	got, err := gitlabIncremental(context.Background(), s, dir, "11",
		&core.FetchFlags{BaseSHA: incrementalBase, HasBase: true})
	if err != nil {
		t.Fatalf("gitlabIncremental: %v", err)
	}
	if got != nullStr {
		t.Errorf("incremental base = %q, want null", got)
	}
	wantNote := "incrementalUnavailable: glab: compare unavailable\n"
	if note := readOptional(filepath.Join(dir, "incremental.diff.note")); note != wantNote {
		t.Errorf("note = %q, want %q", note, wantNote)
	}
	if sidecar := readOptional(filepath.Join(dir, ".compare.err")); sidecar != "glab: compare unavailable" {
		t.Errorf(".compare.err = %q, want glab's stderr", sidecar)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "incremental.diff")); !os.IsNotExist(statErr) {
		t.Errorf("a failed compare left incremental.diff (err=%v)", statErr)
	}
}

// TestGitLabIncrementalUnreadableWritesNote proves an exit-zero payload that
// is not the compare document reads unavailable instead of an empty delta.
func TestGitLabIncrementalUnreadableWritesNote(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeIncrementalPR(t, dir, incrementalHead)
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{
		"glab\x00api\x00projects/11/repository/compare?from=" + incrementalBase + "&to=" + incrementalHead: {
			stdout: "not the compare document",
		},
	}}
	s, _ := newTestSession(cmdr)
	got, err := gitlabIncremental(context.Background(), s, dir, "11",
		&core.FetchFlags{BaseSHA: incrementalBase, HasBase: true})
	if err != nil {
		t.Fatalf("gitlabIncremental: %v", err)
	}
	if got != nullStr {
		t.Errorf("incremental base = %q, want null", got)
	}
	wantNote := "incrementalUnavailable: GitLab compare payload unreadable\n"
	if note := readOptional(filepath.Join(dir, "incremental.diff.note")); note != wantNote {
		t.Errorf("note = %q, want %q", note, wantNote)
	}
}

// TestUnsupportedIncrementalNote proves the shared note names the provider
// and drops a delta a previous run left behind.
func TestUnsupportedIncrementalNote(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "incremental.diff"), []byte("stale\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, _ := newTestSession(&scriptCommander{t: t})
	reason := "--base-sha/--since is not supported for provider gitcafe"
	if ref := writeIncrementalNote(s, dir, reason); ref != nil {
		t.Fatalf("writeIncrementalNote: %v", ref)
	}
	wantNote := "incrementalUnavailable: " + reason + "\n"
	if note := readOptional(filepath.Join(dir, "incremental.diff.note")); note != wantNote {
		t.Errorf("note = %q, want %q", note, wantNote)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "incremental.diff")); !os.IsNotExist(statErr) {
		t.Errorf("the note left a stale incremental.diff (err=%v)", statErr)
	}
}
