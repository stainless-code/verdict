package cli

import "github.com/stainless-code/verdict/internal/core"

// Boolean-valued settings travel as "true"/"false" strings (the shell's
// idiom); FlagOn/FlagOff name the two spellings for cross-package knob
// comparisons.
const (
	FlagOn  = core.FlagOn
	FlagOff = core.FlagOff
)

// codeBadSettings names the refusal code for invalid settings files.
const codeBadSettings = "bad-settings"

// formatJSON is the JSON output format name.
const formatJSON = core.FormatJSON

// Color and output enum spellings.
const (
	colorAuto   = core.ColorAuto
	colorAlways = core.ColorAlways
	colorNever  = core.ColorNever
	formatText  = core.FormatText
)

// Attribution tiers for publish.attribution.
const (
	AttributionOff     = core.AttributionOff
	AttributionProduct = core.AttributionProduct
	AttributionFull    = core.AttributionFull
)
