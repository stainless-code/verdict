// Package core is verdict's private domain boundary. It owns the typed
// request/response contracts shared by every transport, the single refusal
// type, the injected runtime, and the wire shapes the published schemas are
// generated from. Core never reads argv, environment variables, or stdio,
// never calls [os.Exit], and never imports a transport or verb package; the
// CLI/MCP adapters parse, call, write bytes, and map exit codes.
package core

// Boolean-valued settings travel as "true"/"false" strings (the shell's
// idiom); FlagOn/FlagOff name the two spellings for cross-package knob
// comparisons.
const (
	FlagOn  = "true"
	FlagOff = "false"
)

// Color and output enum spellings shared by the CLI contract.
const (
	ColorAuto   = "auto"
	ColorAlways = "always"
	ColorNever  = "never"
	FormatJSON  = "json"
	FormatText  = "text"
)

// Attribution tiers for publish.attribution.
const (
	AttributionOff     = "off"
	AttributionProduct = "product"
	AttributionFull    = "full"
)

// Product/maker constants mirror the shell's frozen footer strings.
const (
	DefaultProductURL = "https://stainless-code.com/verdict"
	DefaultMakerURL   = "https://stainless-code.com"
	DefaultMakerName  = "Stainless Code"
	FooterDivider     = "<!-- verdict-footer -->"
)

// VersionUnknown is the one degrade string for builds with no release
// metadata: --version, the skill payload, and the footer all report it, so
// the same fact never renders two ways.
const VersionUnknown = "0.0.0-unknown"

// Effective holds the resolved presentation knobs as "true"/"false"/enum
// strings, mirroring the shell's EFFECTIVE_* globals.
type Effective struct {
	Compact                string
	Color                  string
	PublishFooter          string
	PublishAttribution     string
	PublishAttributionText string
	PublishCopyright       string
	PublishLogo            string
	PublishLogoURL         string
	PublishOverview        string
	DiagramsStructural     string
	DiagramsBehavioral     string
	DiagramsExpanded       string
}

// Defaults mirrors the shell's global initializers.
func Defaults() Effective {
	return Effective{
		Compact:                FlagOff,
		Color:                  ColorAuto,
		PublishFooter:          FlagOn,
		PublishAttribution:     AttributionProduct,
		PublishAttributionText: "",
		PublishCopyright:       "",
		PublishLogo:            FlagOn,
		PublishLogoURL:         "https://raw.githubusercontent.com/stainless-code/verdict/main/apps/docs/public/logo.svg",
		PublishOverview:        FlagOn,
		DiagramsStructural:     FlagOn,
		DiagramsBehavioral:     FlagOff,
		DiagramsExpanded:       FlagOff,
	}
}

// GlobalForces carries the flag-layer overrides from the global strip.
type GlobalForces struct {
	PublishLogoURLForce       string
	PublishOverviewForce      string
	ColorForce                string
	PublishFooterForce        string
	PublishAttributionForce   string
	PublishAttributionText    string
	CompactForce              string
	PublishCopyright          string
	OutputForce               string
	PublishLogoForce          string
	DiagramsExpandedForce     string
	DiagramsBehavioralForce   string
	DiagramsStructuralForce   string
	PublishLogoURLSet         bool
	PublishCopyrightSet       bool
	PublishAttributionTextSet bool
}
