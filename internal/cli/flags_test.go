package cli_test

import (
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
	rest, g, uerr, ref := cli.StripGlobal(args, "record")
	if uerr != nil || ref != nil {
		t.Fatalf("unexpected error: %v %v", uerr, ref)
	}
	return rest, g
}

// TestStripGlobalBareAttribution checks bare --attribution means product.
func TestStripGlobalBareAttribution(t *testing.T) {
	_, g, uerr, ref := cli.StripGlobal([]string{"--attribution", "record"}, "record")
	if uerr != nil || ref != nil {
		t.Fatalf("unexpected error: %v %v", uerr, ref)
	}
	if g.PublishAttributionForce != "product" {
		t.Errorf("attribution = %q", g.PublishAttributionForce)
	}
	_, g, uerr, ref = cli.StripGlobal([]string{"--attribution"}, "record")
	if uerr != nil || ref != nil {
		t.Fatalf("unexpected error: %v %v", uerr, ref)
	}
	if g.PublishAttributionForce != "product" {
		t.Errorf("attribution = %q", g.PublishAttributionForce)
	}
}

// TestStripGlobalErrors checks missing values are usage errors and bad
// values are typed refusals.
func TestStripGlobalErrors(t *testing.T) {
	if _, _, uerr, ref := cli.StripGlobal([]string{"--color"}, "record"); uerr == nil || ref != nil {
		t.Errorf("missing --color value should be a usage error, got %v %v", uerr, ref)
	}
	if _, _, uerr, ref := cli.StripGlobal([]string{"--color", "bogus"}, "record"); ref == nil || uerr != nil {
		t.Errorf("bad --color value should be a refusal, got %v %v", uerr, ref)
	}
	if _, _, uerr, ref := cli.StripGlobal([]string{"--attribution=nope"}, "record"); ref == nil || uerr != nil {
		t.Errorf("bad --attribution value should be a refusal, got %v %v", uerr, ref)
	}
}

// TestStripGlobalDashDash checks flags after -- pass through untouched.
func TestStripGlobalDashDash(t *testing.T) {
	rest, g, uerr, ref := cli.StripGlobal([]string{"--", "--compact"}, "record")
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
