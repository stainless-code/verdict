package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

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

// envelopeAtField is the one envelope field that legitimately varies between
// invocations: `at` is generated per run, so two runs may straddle a second.
var envelopeAtField = regexp.MustCompile(`("at": ")([^"]*)(")`)

const normalizedAt = "<per-invocation-timestamp>"

// normalizeEnvelopeAt replaces the value of the single required envelope `at`
// field, leaving every other byte untouched. The field must be present exactly
// once and hold what the renderer emits (whole-second UTC), so a missing,
// duplicated, or mangled timestamp fails loudly instead of normalizing away.
func normalizeEnvelopeAt(out string) (string, error) {
	matches := envelopeAtField.FindAllStringSubmatchIndex(out, -1)
	switch len(matches) {
	case 1:
	default:
		return "", fmt.Errorf("stdout carries %d envelope `at` fields; want exactly one", len(matches))
	}
	m := matches[0]
	at := out[m[4]:m[5]]
	ts, err := time.Parse(time.RFC3339, at)
	if err != nil {
		return "", fmt.Errorf("envelope `at` = %q: %w", at, err)
	}
	if ts.UTC() != ts || !ts.Truncate(time.Second).Equal(ts) {
		return "", fmt.Errorf("envelope `at` = %q; want a whole-second UTC timestamp", at)
	}
	return out[:m[2]] + normalizedAt + out[m[5]:], nil
}

// TestGlobalJSONFlagPlacement keeps the same envelope anywhere.
func TestGlobalJSONFlagPlacement(t *testing.T) {
	hermeticEnv(t)
	t.Chdir(t.TempDir())
	want := mustNormalize(t, runOne(t, "report", "acme/app", "9", "--json"))
	for _, key := range []string{`"verb": "report"`, `"repo": "acme/app"`, `"pr": 9`} {
		if !strings.Contains(want.out, key) {
			t.Fatalf("baseline envelope = %s; want %s", want.out, key)
		}
	}
	for _, args := range [][]string{
		{"--json", "report", "acme/app", "9"},
		{"report", "--json", "acme/app", "9"},
	} {
		if got := mustNormalize(t, runOne(t, args...)); got != want {
			t.Errorf("%v = %+v; want the report envelope %+v", args, got, want)
		}
	}
}

// atEnvelope builds the refused-report envelope shape with the given verb,
// timestamp, and error code.
func atEnvelope(verb, at, code string) invocation {
	return invocation{
		code: cli.ExitRefused,
		out: "{\n  \"schema_version\": \"1\",\n  \"verb\": \"" + verb + "\",\n  \"at\": \"" + at +
			"\",\n  \"error\": {\n    \"code\": \"" + code + "\"\n  }\n}\n",
		err: "error: " + code + "\n",
	}
}

func mustNormalize(t *testing.T, inv invocation) invocation {
	t.Helper()
	out, err := normalizeEnvelopeAt(inv.out)
	if err != nil {
		t.Fatalf("%v\nstdout: %s", err, inv.out)
	}
	inv.out = out
	return inv
}

// TestEnvelopeTimestampIsTheOnlyRelaxedField pins why the placement test
// normalizes: envelopes differing only by the generated second must compare
// equal, where the raw whole-invocation comparison failed.
func TestEnvelopeTimestampIsTheOnlyRelaxedField(t *testing.T) {
	t.Parallel()
	early := atEnvelope("report", "2026-11-06T18:44:41Z", "missing-findings")
	late := atEnvelope("report", "2026-11-06T18:44:42Z", "missing-findings")
	if early == late {
		t.Fatal("fixture envelopes must differ byte-for-byte before normalization")
	}
	if got, want := mustNormalize(t, late), mustNormalize(t, early); got != want {
		t.Errorf("envelopes differing only by the generated second normalized apart:\n%+v\n%+v", got, want)
	}
}

// TestEnvelopeNormalizationKeepsSubstantiveDifferences pins the other half of
// the contract: normalization covers the generated timestamp and nothing else,
// so a regression in any compared field still fails, and a malformed timestamp
// is refused rather than masked.
func TestEnvelopeNormalizationKeepsSubstantiveDifferences(t *testing.T) {
	t.Parallel()
	base := atEnvelope("report", "2026-11-06T18:44:41Z", "missing-findings")
	baseNorm := mustNormalize(t, base)
	for _, tc := range []struct {
		name  string
		other invocation
	}{
		{name: "verb", other: atEnvelope("verify", "2026-11-06T18:44:41Z", "missing-findings")},
		{name: "error code", other: atEnvelope("report", "2026-11-06T18:44:41Z", "not-a-repo")},
		{name: "exit code", other: invocation{code: cli.ExitBroke, out: base.out, err: base.err}},
		{name: "stderr", other: invocation{code: base.code, out: base.out, err: "error: other\n"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := mustNormalize(t, tc.other)
			if got == baseNorm {
				t.Errorf("a %s difference normalized away: %+v", tc.name, got)
			}
		})
	}
}

// TestEnvelopeNormalizationRefusesMissingOrMalformedTimestamps pins the other
// guard: a missing, duplicated, offset, or sub-second timestamp is refused
// rather than replaced, so normalization can never hide a broken renderer.
func TestEnvelopeNormalizationRefusesMissingOrMalformedTimestamps(t *testing.T) {
	t.Parallel()
	present := atEnvelope("report", "2026-11-06T18:44:41Z", "missing-findings").out
	for _, tc := range []struct{ name, out string }{
		{name: "missing field", out: strings.Replace(present, "  \"at\": \"2026-11-06T18:44:41Z\",\n", "", 1)},
		{name: "duplicated field", out: strings.Replace(present, "{\n", "{\n  \"at\": \"2026-11-06T18:44:41Z\",\n", 1)},
		{name: "empty value", out: atEnvelope("report", "", "missing-findings").out},
		{name: "offset timestamp", out: atEnvelope("report", "2026-11-06T18:44:41+02:00", "missing-findings").out},
		{name: "sub-second timestamp", out: atEnvelope("report", "2026-11-06T18:44:41.500Z", "missing-findings").out},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if out, err := normalizeEnvelopeAt(tc.out); err == nil {
				t.Fatalf("normalized a bad timestamp to %q; want a refusal", out)
			}
		})
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
