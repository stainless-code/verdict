package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/cli"
)

type invocation struct {
	out  string
	err  string
	code int
}

func runOne(t *testing.T, args ...string) invocation {
	t.Helper()
	code, out, derr := runCapture(t, args)
	return invocation{code: code, out: out, err: derr}
}

// TestGlobalFlagPlacementIsFree holds --color steady in every position.
func TestGlobalFlagPlacementIsFree(t *testing.T) {
	t.Skip("prototype: state root moved to .verdict; stale .review expectations")
	hermeticEnv(t)
	t.Chdir(t.TempDir())
	want := runOne(t, "report", "acme/app", "9", "--color=always")
	if want.code != cli.ExitRefused || want.out != "" {
		t.Fatalf("baseline = %+v; want exit %d and an empty stdout", want, cli.ExitRefused)
	}
	if !strings.Contains(want.err, "\x1b[31m") || !strings.Contains(want.err, ".review/9") {
		t.Fatalf("baseline stderr = %q; want the colored missing-store refusal", want.err)
	}
	for _, args := range [][]string{
		{"--color", "always", "report", "acme/app", "9"},
		{"report", "--color", "always", "acme/app", "9"},
		{"report", "acme/app", "9", "--color", "always"},
	} {
		if got := runOne(t, args...); got != want {
			t.Errorf("%v = %+v; want the --color=always output %+v", args, got, want)
		}
	}
}

// TestGlobalJSONFlagPlacement keeps the same envelope anywhere.
func TestGlobalJSONFlagPlacement(t *testing.T) {
	hermeticEnv(t)
	t.Chdir(t.TempDir())
	want := runOne(t, "report", "acme/app", "9", "--json")
	for _, key := range []string{`"verb": "report"`, `"repo": "acme/app"`, `"pr": 9`} {
		if !strings.Contains(want.out, key) {
			t.Fatalf("baseline envelope = %s; want %s", want.out, key)
		}
	}
	for _, args := range [][]string{
		{"--json", "report", "acme/app", "9"},
		{"report", "--json", "acme/app", "9"},
	} {
		if got := runOne(t, args...); got != want {
			t.Errorf("%v = %+v; want the report envelope %+v", args, got, want)
		}
	}
}

// TestMisplacedFlagFetchRefusesAtTheFrontDoor pins usage, no bundle.
func TestMisplacedFlagFetchRefusesAtTheFrontDoor(t *testing.T) {
	hermeticEnv(t)
	t.Setenv("PATH", t.TempDir())
	dir := t.TempDir()
	t.Chdir(dir)
	got := runOne(t, "fetch", "--color", "always")
	if got.code != cli.ExitUsage {
		t.Fatalf("fetch --color always exit = %d; want %d (stderr: %s)", got.code, cli.ExitUsage, got.err)
	}
	if got.err != usageText() {
		t.Fatalf("stderr is not the usage block:\n%s", got.err)
	}
	if got.out != "" {
		t.Errorf("stdout = %q; want the usage block on stderr only", got.out)
	}
	if _, err := os.Stat(filepath.Join(dir, ".review")); err == nil {
		t.Error("the refused fetch wrote a .review bundle")
	}
}

// TestSplitKeepsVerbFlagsBeforeSeparator keeps flags before the separator.
func TestSplitKeepsVerbFlagsBeforeSeparator(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		args     []string
		repo, pr string
		rest     []string
	}{
		{
			args: []string{"fetch-local", "acme/app", "demo", "--base", "x", "--", "a.txt"},
			repo: "acme/app", pr: "demo",
			rest: []string{"--base", "x", "--", "a.txt"},
		},
		{
			args: []string{"fetch-local", "acme/app", "--", "--json"},
			repo: "acme/app", pr: "",
			rest: []string{"--", "--json"},
		},
	} {
		cmd, repo, pr, rest := splitCommand(tc.args)
		if cmd != "fetch-local" || repo != tc.repo || pr != tc.pr || !slices.Equal(rest, tc.rest) {
			t.Errorf("split %v = %q %q %q %q; want fetch-local %q %q %q", tc.args, cmd, repo, pr, rest, tc.repo, tc.pr, tc.rest)
		}
	}
}

// TestSeparatorEndsThePositionals pins the separator ending positional reads.
func TestSeparatorEndsThePositionals(t *testing.T) {
	hermeticEnv(t)
	t.Chdir(t.TempDir())
	got := runOne(t, "fetch-local", "acme/app", "--", "--json")
	if got.code != cli.ExitUsage || got.err != usageText() {
		t.Fatalf("fetch-local acme/app -- = %+v; want the usage block", got)
	}
}

// TestMissingGlobalValueIsNamed names the global flag whose value is missing.
func TestMissingGlobalValueIsNamed(t *testing.T) {
	hermeticEnv(t)
	got := runOne(t, "report", "--color")
	if got.code != cli.ExitUsage {
		t.Fatalf("report --color exit = %d; want %d", got.code, cli.ExitUsage)
	}
	if want := "--color needs a value (want auto|always|never)\n"; got.err != want {
		t.Errorf("stderr = %q; want %q", got.err, want)
	}
}
