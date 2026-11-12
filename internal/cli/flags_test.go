package cli_test

import (
	"errors"
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

func checkSetValue(t *testing.T, name, got string, set bool, want string) {
	t.Helper()
	if got != want || !set {
		t.Errorf("%s = %q %v; want %q true", name, got, set, want)
	}
}

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

// TestApplyGlobalForcesKitchenSink sets every force on one call and asserts
// each Effective field it lands on, so a mapping swapped between two knobs
// cannot pass: the compact/color/footer/attribution/logo/overview/diagram
// overlays, the three set-only values (text, copyright, logo URL), the output
// layer, and the JSONMode derivation all come from one ApplyGlobalForces.
//
// Break killed: dropping any single overlay line (or wiring a force to the
// neighboring field) leaves that one field at its default and fails.
func TestApplyGlobalForcesKitchenSink(t *testing.T) {
	r := &cli.Runner{}
	g := &cli.GlobalForces{
		OutputForce:               "json",
		CompactForce:              cli.FlagOn,
		ColorForce:                "never",
		PublishFooterForce:        cli.FlagOff,
		PublishAttributionForce:   "full",
		PublishAttributionText:    "kept {model}",
		PublishAttributionTextSet: true,
		PublishCopyright:          "© 2026 acme",
		PublishCopyrightSet:       true,
		PublishLogoForce:          cli.FlagOff,
		PublishLogoURLForce:       "https://x/y.png",
		PublishLogoURLSet:         true,
		PublishOverviewForce:      cli.FlagOff,
		DiagramsStructuralForce:   cli.FlagOff,
		DiagramsBehavioralForce:   cli.FlagOn,
		DiagramsExpandedForce:     cli.FlagOn,
	}
	if err := cli.ApplyGlobalForces(r, g, false); err != nil {
		t.Fatalf("ApplyGlobalForces: %v", err)
	}
	eff := r.Effective
	for _, tc := range []struct{ name, got, want string }{
		{"compact", eff.Compact, cli.FlagOn},
		{"color", eff.Color, "never"},
		{"footer", eff.PublishFooter, cli.FlagOff},
		{"attribution", eff.PublishAttribution, "full"},
		{"attribution text", eff.PublishAttributionText, "kept {model}"},
		{"copyright", eff.PublishCopyright, "© 2026 acme"},
		{"logo", eff.PublishLogo, cli.FlagOff},
		{"logo url", eff.PublishLogoURL, "https://x/y.png"},
		{"overview", eff.PublishOverview, cli.FlagOff},
		{"structural", eff.DiagramsStructural, cli.FlagOff},
		{"behavioral", eff.DiagramsBehavioral, cli.FlagOn},
		{"expanded", eff.DiagramsExpanded, cli.FlagOn},
		{"output", r.Output, "json"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %q; want %q", tc.name, tc.got, tc.want)
		}
	}
	if !r.JSONMode {
		t.Error("JSONMode = false; want true with --json")
	}
}

// TestSettingsPacksKeyAccepted proves the shared settings loader accepts the
// explicit org pack array beside the presentation and personal sections.
func TestSettingsPacksKeyAccepted(t *testing.T) {
	t.Parallel()
	env := map[string]string{"HOME": "/home/operator"}
	body := []byte(`{"packs":["org/security"]}`)
	_, err := cli.ResolvePrefs(&cli.Runner{}, env, func(string) ([]byte, error) {
		return body, nil
	})
	if err != nil {
		t.Fatalf("ResolvePrefs refused packs: %v", err)
	}
}

// TestApplyGlobalForcesPlaceholders pins the one refusal ApplyGlobalForces can
// return, in both directions: an inventory placeholder is accepted, an unknown
// one is refused naming the source and the placeholder, and the field keeps
// its set value.
//
// Break killed: negating the `ref != nil` arm returns nil for `{bogus}` and
// keeps rendering a footer with a placeholder that never interpolates.
func TestApplyGlobalForcesPlaceholders(t *testing.T) {
	ok := &cli.Runner{}
	if err := cli.ApplyGlobalForces(ok, &cli.GlobalForces{
		PublishAttributionText: "reviewed with {model} for {attribution}", PublishAttributionTextSet: true,
	}, false); err != nil {
		t.Fatalf("inventory text refused: %v", err)
	}
	if ok.Effective.PublishAttributionText != "reviewed with {model} for {attribution}" {
		t.Errorf("text = %q; want the set value", ok.Effective.PublishAttributionText)
	}
	bad := &cli.Runner{}
	err := cli.ApplyGlobalForces(bad, &cli.GlobalForces{
		PublishAttributionText: "reviewed by {bogus}", PublishAttributionTextSet: true,
	}, false)
	if err == nil {
		t.Fatal("unknown placeholder {bogus} was accepted")
	}
	var ref *cli.RefusalError
	if !errors.As(err, &ref) {
		t.Fatalf("err = %T; want a typed refusal", err)
	}
	if ref.Code != "bad-settings" {
		t.Errorf("code = %q; want bad-settings", ref.Code)
	}
	if !strings.Contains(ref.Message, "{bogus}") || !strings.Contains(ref.Message, "publish.attribution_text") {
		t.Errorf("message = %q; want it to name the placeholder and the source", ref.Message)
	}
}

// TestApplyGlobalForcesNoColor pins the --no-color precedence rule in all
// four directions: it wins by default, and a forced mode, a configured mode
// (REVIEW_COLOR), or a stored preference each outrank it.
//
// Break killed: negating the body of applyColorFallback never reaches "never";
// negating the `!= colorAlways` guard lets --no-color beat an explicit
// --color always; negating the env or pref guards overrides a configured mode.
func TestApplyGlobalForcesNoColor(t *testing.T) {
	for _, tc := range []noColorCase{
		{"no --no-color leaves auto", "", "", "auto", false, false},
		{"--no-color alone picks never", "", "", "never", false, true},
		{"--color always outranks", "always", "", "always", false, true},
		{"REVIEW_COLOR outranks", "", "always", "auto", false, true},
		{"stored preference outranks", "", "", "auto", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) { checkNoColorRule(t, tc) })
	}
}

type noColorCase struct {
	name     string
	color    string
	env      string
	wantMode string
	pref     bool
	noColor  bool
}

func checkNoColorRule(t *testing.T, tc noColorCase) {
	t.Helper()
	r := &cli.Runner{EnvReviewColorMode: tc.env, PrefColorSet: tc.pref}
	r.Effective = cli.Defaults()
	if err := cli.ApplyGlobalForces(r, &cli.GlobalForces{ColorForce: tc.color}, tc.noColor); err != nil {
		t.Fatalf("ApplyGlobalForces: %v", err)
	}
	if got := r.Effective.Color; got != tc.wantMode {
		t.Errorf("color = %q; want %q", got, tc.wantMode)
	}
}

// TestBoolFlagTableUncoveredHalf pins the seven setters no other test names, so
// the whole boolFlags table is exercised. An entry that flipped its On/Off
// constant, or pointed at the neighboring field, fails one row.
//
// Break killed: negating any of these setters' constants (e.g. --no-compact
// setting FlagOn) or retargeting a pair.
func TestBoolFlagTableUncoveredHalf(t *testing.T) {
	for _, tc := range []boolFlagCase{
		{func(g *cli.GlobalForces) string { return g.CompactForce }, "--no-compact", cli.FlagOff},
		{func(g *cli.GlobalForces) string { return g.PublishFooterForce }, "--footer", cli.FlagOn},
		{func(g *cli.GlobalForces) string { return g.PublishOverviewForce }, "--no-overview", cli.FlagOff},
		{func(g *cli.GlobalForces) string { return g.DiagramsBehavioralForce }, "--no-diagram-behavioral", cli.FlagOff},
		{func(g *cli.GlobalForces) string { return g.DiagramsStructuralForce }, "--diagram-structural", cli.FlagOn},
		{func(g *cli.GlobalForces) string { return g.DiagramsExpandedForce }, "--no-diagram-expanded", cli.FlagOff},
		{func(g *cli.GlobalForces) string { return g.PublishLogoForce }, "--no-logo", cli.FlagOff},
	} {
		t.Run(tc.flag, func(t *testing.T) { checkBoolFlag(t, tc) })
	}
}

type boolFlagCase struct {
	field func(*cli.GlobalForces) string
	flag  string
	want  string
}

func checkBoolFlag(t *testing.T, tc boolFlagCase) {
	t.Helper()
	rest, g := mustStrip(t, []string{"record", tc.flag})
	if len(rest) != 1 {
		t.Fatalf("rest = %v; want the verb alone", rest)
	}
	if got := tc.field(g); got != tc.want {
		t.Errorf("%s = %q; want %q", tc.flag, got, tc.want)
	}
}

// TestStripEqForms pins the --flag=value spellings, including the pass-through
// arm: an unknown --name=value stays a positional argument rather than being
// eaten as a global.
//
// Break killed: dropping the TrimPrefix from a setter keeps "--color=" in the
// value; negating the default arm consumes an unknown flag.
func TestStripEqForms(t *testing.T) {
	rest, g := mustStrip(t, []string{"--color=always", "--logo-url=https://x/y.png", "--attribution-text=hi", "record"})
	if len(rest) != 1 || rest[0] != "record" {
		t.Fatalf("rest = %v", rest)
	}
	checkForce(t, "color=", g.ColorForce, "always")
	checkSetValue(t, "logo-url=", g.PublishLogoURLForce, g.PublishLogoURLSet, "https://x/y.png")
	checkSetValue(t, "attribution-text=", g.PublishAttributionText, g.PublishAttributionTextSet, "hi")
	passed, pg := mustStrip(t, []string{"--compac=1", "record"})
	if len(passed) != 2 || passed[0] != "--compac=1" {
		t.Errorf("rest = %v; want the unknown flag kept as a positional", passed)
	}
	if pg.OutputForce != "" || pg.CompactForce != "" || pg.ColorForce != "" {
		t.Errorf("unknown --name=value set a force: %+v", pg)
	}
}

// TestStripAttributionTier pins the spaced spelling with a tier operand: the
// tier is consumed and kept verbatim, while a non-tier operand reads as the
// bare form so `--attribution record` leaves the verb alone.
//
// Break killed: dropping the tier arm keeps "full" as a positional; negating
// isAttributionTier turns every operand into a tier.
func TestStripAttributionTier(t *testing.T) {
	rest, g := mustStrip(t, []string{"--attribution", "full", "record"})
	if len(rest) != 1 || rest[0] != "record" {
		t.Fatalf("rest = %v; want [record] (the tier is consumed)", rest)
	}
	checkForce(t, "attribution", g.PublishAttributionForce, "full")
	rest, g = mustStrip(t, []string{"--attribution", "record"})
	if len(rest) != 1 || rest[0] != "record" {
		t.Fatalf("rest = %v; want [record] (bare form)", rest)
	}
	checkForce(t, "attribution", g.PublishAttributionForce, "product")
}

// TestFlagValue pins the value-operand helper both the global strip and the
// verb flag readers use: the operand plus its index, or the typed usage error
// naming the flag and what it wants when the flag is last in argv.
//
// Break killed: negating the `i+1 >= len(args)` bound indexes past the end
// (panic) instead of reporting the missing value; returning i instead of i+1
// makes every caller re-read the flag as its own value.
func TestFlagValue(t *testing.T) {
	args := []string{"--color", "never", "record"}
	if got, next, uerr := cli.FlagValue(args, 0, "--color", "auto|always|never"); uerr != nil || got != "never" || next != 1 {
		t.Errorf("FlagValue = %q, %d, %v; want \"never\", 1, nil", got, next, uerr)
	}
	if got, next, uerr := cli.FlagValue(args, 2, "--format", "md"); uerr == nil {
		t.Errorf("FlagValue(last) = %q, %d, nil; want the missing-value usage error", got, next)
	} else if want := "--format needs a value (want md)"; uerr.Message != want {
		t.Errorf("FlagValue(last) message = %q; want %q", uerr.Message, want)
	}
}

// TestPeekJSON pins the cheap pre-scan that decides whether the process must
// keep stdout for one envelope: a --json anywhere before the `--` separator
// counts, and nothing after it does.
//
// Break killed: negating the `a == "--"` break makes row 3 report true by
// scanning past the separator; negating the `a == "--json"` test reports false
// for a real --json.
func TestPeekJSON(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want bool
	}{
		{"leading", []string{"--json", "record"}, true},
		{"trailing", []string{"record", "acme/app", "--json"}, true},
		{"after separator", []string{"--", "--json"}, false},
		{"absent", []string{"record", "acme/app"}, false},
		{"no-json only", []string{"--no-json", "record"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := cli.PeekJSON(tc.args); got != tc.want {
				t.Errorf("PeekJSON(%v) = %v; want %v", tc.args, got, tc.want)
			}
		})
	}
}
