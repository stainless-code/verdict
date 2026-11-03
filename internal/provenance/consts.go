package provenance

// Shared rendering and parsing constants.
const (
	// unknown marks an unreadable value in human/budget output.
	unknown = "unknown"
	// prFile is the bundle's PR record filename.
	prFile = "PR.json"
	// floatBits is the precision for cost/rate parsing.
	floatBits = 64
	// decimalBase is the radix for integer rendering.
	decimalBase = 10
	// maxExactInt is the magnitude below which floats print integrally.
	maxExactInt = 1e21
)

// unrecorded marks a cost the run could not determine.
const unrecorded = "unrecorded"
