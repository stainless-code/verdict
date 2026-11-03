package cli_test

import (
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/cli"
)

// TestStripGlobalToggles checks the boolean global flags land on forces.
func TestStripGlobalToggles(t *testing.T) {
	rest, g := mustStrip(t, []string{
		"--compact", "--no-footer", "--overview", "--diagram-behavioral",
		"--no-diagram-structural", "--diagram-expanded", "--logo", "--no-json",
		"--no-attribution", "record", "acme/app", "local-x",
	})
	if len(rest) != 3 || rest[0] != "record" {
		t.Fatalf("rest = %v", rest)
	}
	checkForce(t, "compact", g.CompactForce, cli.FlagOn)
	checkForce(t, "footer", g.PublishFooterForce, cli.FlagOff)
	checkForce(t, "overview", g.PublishOverviewForce, cli.FlagOn)
	checkForce(t, "behavioral", g.DiagramsBehavioralForce, cli.FlagOn)
	checkForce(t, "structural", g.DiagramsStructuralForce, cli.FlagOff)
	checkForce(t, "expanded", g.DiagramsExpandedForce, cli.FlagOn)
	checkForce(t, "logo", g.PublishLogoForce, cli.FlagOn)
	checkForce(t, "output", g.OutputForce, "text")
	checkForce(t, "attribution", g.PublishAttributionForce, "off")
}

// checkForce checks one force value.
func checkForce(t *testing.T, name, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %q; want %q", name, got, want)
	}
}

// TestStripGlobalValues checks the value-taking global flags.
func TestStripGlobalValues(t *testing.T) {
	rest, g := mustStrip(t, []string{
		"--color", "never", "--attribution=full", "--logo-url", "https://x/y.png",
		"--attribution-text", "hi", "--copyright=c", "record",
	})
	if len(rest) != 1 || rest[0] != "record" {
		t.Fatalf("rest = %v", rest)
	}
	checkForce(t, "color", g.ColorForce, "never")
	checkForce(t, "attribution", g.PublishAttributionForce, "full")
	checkSetValue(t, "logo-url", g.PublishLogoURLForce, g.PublishLogoURLSet, "https://x/y.png")
	checkSetValue(t, "attribution-text", g.PublishAttributionText, g.PublishAttributionTextSet, "hi")
	checkSetValue(t, "copyright", g.PublishCopyright, g.PublishCopyrightSet, "c")
}

// checkSetValue checks one set-flag value.
func checkSetValue(t *testing.T, name, got string, set bool, want string) {
	t.Helper()
	if got != want || !set {
		t.Errorf("%s = %q %v; want %q true", name, got, set, want)
	}
}

// mustStrip strips globals or fails the test.
func mustStrip(t *testing.T, args []string) ([]string, *cli.GlobalForces) {
	t.Helper()
	rest, g, uerr, ref := cli.StripGlobal(args)
	if uerr != nil || ref != nil {
		t.Fatalf("unexpected error: %v %v", uerr, ref)
	}
	return rest, g
}

// TestStripGlobalBareAttribution checks bare --attribution means product.
func TestStripGlobalBareAttribution(t *testing.T) {
	_, g, uerr, ref := cli.StripGlobal([]string{"--attribution", "record"})
	if uerr != nil || ref != nil {
		t.Fatalf("unexpected error: %v %v", uerr, ref)
	}
	if g.PublishAttributionForce != "product" {
		t.Errorf("attribution = %q", g.PublishAttributionForce)
	}
	_, g, uerr, ref = cli.StripGlobal([]string{"--attribution"})
	if uerr != nil || ref != nil {
		t.Fatalf("unexpected error: %v %v", uerr, ref)
	}
	if g.PublishAttributionForce != "product" {
		t.Errorf("attribution = %q", g.PublishAttributionForce)
	}
}

// TestStripGlobalMissingValue pins the one missing-value text: a global value
// flag given without its value says so, and never reads as an unknown flag.
func TestStripGlobalMissingValue(t *testing.T) {
	cases := []struct {
		name string
		want string
		flag string
	}{
		{"color", "--color needs a value (want auto|always|never)", "--color"},
		{"logo-url", "--logo-url needs a value (want <url>)", "--logo-url"},
		{"attribution-text", "--attribution-text needs a value (want <text>)", "--attribution-text"},
		{"copyright", "--copyright needs a value (want <text>)", "--copyright"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			checkMissingValue(t, tc.flag, tc.want)
		})
	}
}

// checkMissingValue asserts one global flag with no value reports want.
func checkMissingValue(t *testing.T, flag, want string) {
	t.Helper()
	_, _, uerr, ref := cli.StripGlobal([]string{"record", flag})
	if ref != nil {
		t.Fatalf("%s: refusal %v; want a usage error", flag, ref)
	}
	if uerr == nil {
		t.Fatalf("%s: no usage error", flag)
	}
	if uerr.Message != want {
		t.Errorf("%s = %q; want %q", flag, uerr.Message, want)
	}
	if strings.Contains(uerr.Message, "unknown flag") {
		t.Errorf("%s: a known flag must not read as unknown: %q", flag, uerr.Message)
	}
}

// TestMissingValue checks the shared helper renders both message shapes.
func TestMissingValue(t *testing.T) {
	for _, tc := range []struct{ name, flag, want, wantMsg string }{
		{"bare", "--version", "", "--version needs a value"},
		{"with hint", "--format", "md or json", "--format needs a value (want md or json)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := cli.MissingValue(tc.flag, tc.want).Message; got != tc.wantMsg {
				t.Errorf("MissingValue(%q, %q) = %q; want %q", tc.flag, tc.want, got, tc.wantMsg)
			}
		})
	}
}

// TestStripGlobalErrors checks bad values are typed refusals.
func TestStripGlobalErrors(t *testing.T) {
	if _, _, uerr, ref := cli.StripGlobal([]string{"--color", "bogus"}); ref == nil || uerr != nil {
		t.Errorf("bad --color value should be a refusal, got %v %v", uerr, ref)
	}
	if _, _, uerr, ref := cli.StripGlobal([]string{"--attribution=nope"}); ref == nil || uerr != nil {
		t.Errorf("bad --attribution value should be a refusal, got %v %v", uerr, ref)
	}
}

// TestStripGlobalDashDash checks flags after -- pass through untouched.
func TestStripGlobalDashDash(t *testing.T) {
	rest, g, uerr, ref := cli.StripGlobal([]string{"--", "--compact"})
	if uerr != nil || ref != nil {
		t.Fatalf("unexpected error: %v %v", uerr, ref)
	}
	if len(rest) != 2 || rest[1] != "--compact" {
		t.Fatalf("rest = %v", rest)
	}
	if g.CompactForce != "" {
		t.Errorf("compact should not be set, got %q", g.CompactForce)
	}
}

// TestStripGlobalJSONToggle checks --json is one global toggle: it is
// consumed anywhere before --, and the last of --json/--no-json wins.
func TestStripGlobalJSONToggle(t *testing.T) {
	cases := []struct {
		name string
		want string
		args []string
	}{
		{name: "json", want: "json", args: []string{"--json", "record"}},
		{name: "json after verb", want: "json", args: []string{"record", "--json"}},
		{name: "json then no-json", want: "text", args: []string{"--json", "--no-json", "record"}},
		{name: "no-json then json", want: "json", args: []string{"--no-json", "--json", "record"}},
	}
	for _, tc := range cases {
		rest, g := mustStrip(t, tc.args)
		if g.OutputForce != tc.want {
			t.Errorf("%s: output = %q; want %q", tc.name, g.OutputForce, tc.want)
		}
		if len(rest) != 1 || rest[0] != "record" {
			t.Errorf("%s: rest = %v", tc.name, rest)
		}
	}
}

// TestStripGlobalJSONAfterDashDash checks --json after -- stays positional.
func TestStripGlobalJSONAfterDashDash(t *testing.T) {
	rest, g, uerr, ref := cli.StripGlobal([]string{"--", "--json"})
	if uerr != nil || ref != nil {
		t.Fatalf("unexpected error: %v %v", uerr, ref)
	}
	if len(rest) != 2 || rest[1] != "--json" {
		t.Fatalf("rest = %v", rest)
	}
	if g.OutputForce != "" {
		t.Errorf("output should not be set, got %q", g.OutputForce)
	}
}

// TestApplyGlobalForcesJSONMode checks the toggle reaches JSONMode without
// the early peek (the peek only covers the pre-prefs phase).
func TestApplyGlobalForcesJSONMode(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want bool
	}{
		{"json", []string{"--json"}, true},
		{"json then no-json", []string{"--json", "--no-json"}, false},
		{"no-json then json", []string{"--no-json", "--json"}, true},
		{"neither", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkApplyJSONMode(t, tc.args, tc.want)
		})
	}
}

// checkApplyJSONMode strips args and asserts the resolved toggle.
func checkApplyJSONMode(t *testing.T, args []string, want bool) {
	t.Helper()
	_, g, uerr, ref := cli.StripGlobal(args)
	if uerr != nil || ref != nil {
		t.Fatalf("unexpected error: %v %v", uerr, ref)
	}
	r := &cli.Runner{}
	if err := cli.ApplyGlobalForces(r, g, false); err != nil {
		t.Fatalf("ApplyGlobalForces: %v", err)
	}
	if r.JSONMode != want {
		t.Errorf("JSONMode = %v; want %v", r.JSONMode, want)
	}
}
