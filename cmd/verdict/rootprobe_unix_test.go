//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/cli"
)

// brokenProbeRepo seeds a checkout whose git shim fails the way a broken
// checkout does, and returns the subdirectory to invoke from. The shim is a
// POSIX script, so these cases build on the same unix set as the other shell
// fixtures here rather than failing generically on Windows.
func brokenProbeRepo(t *testing.T) (sub string) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	sub = filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	stub := filepath.Join(bin, "git")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\necho 'fatal: detected dubious ownership' >&2\nexit 128\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Chdir(sub)
	return sub
}

// refusalEnvelope decodes the one envelope on stdout and proves nothing
// follows it, so a second value or trailing text fails here.
func refusalEnvelope(t *testing.T, out string) struct {
	Verb  string `json:"verb"`
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
} {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(out))
	var env struct {
		Verb  string `json:"verb"`
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := dec.Decode(&env); err != nil {
		t.Fatalf("stdout is not a JSON envelope: %v\n%s", err, out)
	}
	if dec.More() {
		t.Fatalf("stdout carries a second value after the envelope:\n%s", out)
	}
	var trailing json.RawMessage
	if err := dec.Decode(&trailing); err == nil {
		t.Fatalf("stdout carries trailing output after the envelope:\n%s", out)
	}
	return env
}

// TestBrokenRootProbeRefusesBeforeWriting pins the root-safety contract at the
// command boundary: inside a checkout whose git probe fails, a mutating verb
// must refuse instead of resolving the working directory as the review root.
// Otherwise a subdirectory run scaffolds or anchors .verdict/state outside the
// repository root, missing the base policy that lives there.
func TestBrokenRootProbeRefusesBeforeWriting(t *testing.T) {
	hermeticEnv(t)
	sub := brokenProbeRepo(t)

	got := runOne(t, "init")
	if got.code != cli.ExitRefused {
		t.Fatalf("init = %+v; want a refusal when the root probe fails", got)
	}
	assertNoSubdirVerdict(t, sub)
}

// TestBrokenRootProbeJSONKeepsTheEnvelope pins the machine contract: an
// unresolved root is a refusal like any other, so --json writes exactly one
// standard envelope on stdout, exits refused, and writes nothing to disk.
func TestBrokenRootProbeJSONKeepsTheEnvelope(t *testing.T) {
	hermeticEnv(t)
	sub := brokenProbeRepo(t)

	got := runOne(t, "report", "acme/app", "9", "--json")
	if got.code != cli.ExitRefused {
		t.Fatalf("exit = %d, want %d (%s)", got.code, cli.ExitRefused, got.err)
	}
	env := refusalEnvelope(t, got.out)
	if env.Verb != "report" {
		t.Errorf("envelope verb = %q, want report", env.Verb)
	}
	if env.Error.Code != "not-a-repo" {
		t.Errorf("envelope error code = %q, want not-a-repo", env.Error.Code)
	}
	if !strings.Contains(env.Error.Message, "repository root unresolved") {
		t.Errorf("envelope message misses the cause: %q", env.Error.Message)
	}
	human := runOne(t, "report", "acme/app", "9")
	if human.code != cli.ExitRefused {
		t.Fatalf("human exit = %d, want %d", human.code, cli.ExitRefused)
	}
	if !strings.Contains(human.err, "repository root unresolved") {
		t.Errorf("human diagnostic misses the cause: %q", human.err)
	}
	assertNoSubdirVerdict(t, sub)
}
