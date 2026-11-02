package kb

// Join/credit field names (jq object keys).
const (
	fieldSig     = "sig"
	fieldRule    = "rule"
	fieldOutcome = "outcome"
	fieldVerb    = "verb"
)

// outcomeAddressed is the credit outcome that counts as a hit.
const outcomeAddressed = "addressed"

// statusActive is the live KB rule status.
const statusActive = "active"

// decimalBase is the radix for hand-rolled integer parsing.
const creditDecimalBase = 10
