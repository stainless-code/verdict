package cli

import "github.com/stainless-code/verdict/internal/core"

// Boolean-valued settings travel as "true"/"false" strings (the
// idiom); FlagOn/FlagOff name the two spellings for cross-package knob
// comparisons.
const (
	FlagOn  = core.FlagOn
	FlagOff = core.FlagOff
)

const codeBadSettings = "bad-settings"

// CodeCanceled names the cancellation refusal (GO-CLI-05). Every adapter
// exits 3 on it so scripts can tell a cancel from a failed gate.
const CodeCanceled = "canceled"

const formatJSON = core.FormatJSON

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
