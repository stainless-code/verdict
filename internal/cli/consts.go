package cli

// Boolean-valued settings travel as "true"/"false" strings (the shell's
// idiom); FlagOn/FlagOff name the two spellings for cross-package knob
// comparisons.
const (
	FlagOn  = "true"
	FlagOff = "false"
)

// codeBadSettings names the refusal code for invalid settings files.
const codeBadSettings = "bad-settings"

// formatJSON is the JSON output format name.
const formatJSON = "json"

// Color and output enum spellings.
const (
	colorAuto   = "auto"
	colorAlways = "always"
	colorNever  = "never"
	formatText  = "text"
)

// Attribution tiers for publish.attribution.
const (
	AttributionOff     = "off"
	AttributionProduct = "product"
	AttributionFull    = "full"
)
