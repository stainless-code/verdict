//go:build unix

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// recordingHostStubs puts a gh and a glab that log every call and fail first
// on PATH, returning the log path.
func recordingHostStubs(t *testing.T) string {
	t.Helper()
	bin, logPath := t.TempDir(), filepath.Join(t.TempDir(), "calls.log")
	stub := "#!/bin/sh\necho \"$@\" >> " + logPath + "\nexit 1\n"
	for _, tool := range []string{"gh", "glab"} {
		if err := os.WriteFile(filepath.Join(bin, tool), []byte(stub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

// TestMistypedFlagMakesNoHostCall pins that a host verb's own flag check runs
// before target resolution: with a recording gh first on PATH, a verb given an
// unknown flag in a checkout whose branch no bundle records exits without one
// host call and without a target line.
func TestMistypedFlagMakesNoHostCall(t *testing.T) {
	targetCheckout(t, false)
	logPath := recordingHostStubs(t)
	for _, args := range [][]string{
		{"fetch", "--frobnicate"},
		{"pr-wait", "--frobnicate", "x"},
		{"ready", "--frobnicate"},
		{"threads", "--frobnicate"},
		{"threads", "resolve", "--frobnicate"},
		{"drain", "--post", "--frobnicate"},
	} {
		assertFlagErrorOnly(t, args)
	}
	if raw, err := os.ReadFile(logPath); err == nil {
		t.Fatalf("the host CLI was called before the flags were checked:\n%s", raw)
	}
}

func assertFlagErrorOnly(t *testing.T, args []string) {
	t.Helper()
	code, out, derr := runCapture(t, args)
	if code == 0 || out != "" || strings.Contains(derr, "target:") || strings.Contains(derr, "no target") {
		t.Errorf("%v: exit %d stdout %q stderr %q; want a flag error and nothing else", args, code, out, derr)
	}
	if !strings.Contains(derr, "frobnicate") {
		t.Errorf("%v: stderr does not name the bad flag:\n%s", args, derr)
	}
}
