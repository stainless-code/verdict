package initcore

// The two commits a generated workflow pins, each with the release its
// trailing comment names. action.yml and examples/verdict-review.yml carry the
// same pins; internal/archtest/ci_workflow_test.go fails when either drifts.
const (
	// CheckoutPin is actions/checkout v7.
	CheckoutPin     = "3d3c42e5aac5ba805825da76410c181273ba90b1"
	CheckoutVersion = "v7"
	// SetupVerdictPin is the stainless-code/verdict setup action at v0.7.0.
	SetupVerdictPin     = "b4c36344f03375985a8edf859b9092ba7d59f077"
	SetupVerdictVersion = "v0.7.0"
)
