package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestJSONVerbsWriteOneEnvelopeToStdout(t *testing.T) {
	hermeticEnv(t)
	root := t.TempDir()
	t.Chdir(root)
	base, head := gitRangeFixture(t, root)
	if got := runOne(t, "fetch-local", "acme/app", "stdout", "--range", base+".."+head, "--json"); got.code != 0 {
		t.Fatalf("fetch-local exit = %d\nstdout: %s\nstderr: %s", got.code, got.out, got.err)
	}
	writeReviewJSON(t, ".verdict/state/local-stdout/review.json")
	for _, args := range [][]string{
		{"next", "acme/app", "local-stdout", "--coverage", "--json"},
		{"record", "acme/app", "local-stdout", "--json"},
		{"report", "acme/app", "local-stdout", "--check", "important", "--max-risk", "medium", "--json"},
	} {
		got := runOne(t, args...)
		if got.code != 0 {
			t.Fatalf("%v exit = %d\nstderr: %s", args, got.code, got.err)
		}
		assertOneJSONValue(t, args[0], got.out)
	}
}

func assertOneJSONValue(t *testing.T, verb, out string) {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(out))
	var first any
	if err := dec.Decode(&first); err != nil {
		t.Fatalf("%s stdout is not a JSON value: %v\n%s", verb, err, out)
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		t.Fatalf("%s stdout carries more than one JSON value: %v\n%s", verb, err, out)
	}
}

func writeReviewJSON(t *testing.T, path string) {
	t.Helper()
	body := `{"schema_version":"1","summary":"A clean review with enough text.","findings":[]}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func gitRangeFixture(t *testing.T, dir string) (base, head string) {
	t.Helper()
	git := func(args ...string) string {
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	git("init", "-q")
	notes := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(notes, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "notes.txt")
	git("-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-qm", "one")
	base = git("rev-parse", "HEAD")
	if err := os.WriteFile(notes, []byte("one\nTWO\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "notes.txt")
	git("-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-qm", "two")
	head = git("rev-parse", "HEAD")
	return trimNewline(base), trimNewline(head)
}

func trimNewline(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
