package query

import "slices"

// The grouping vocabularies the cost and precision views serve. Transports
// (serve, ACP, MCP) read these instead of copying the lists, so no surface
// can drift from the layer it renders.

// DefaultCostBy groups spend by model unless the caller names a grouping;
// it is the third `by` the cost view serves.
const DefaultCostBy = keyModel

var (
	costDimensions      = []string{"repo", "week", keyModel}
	precisionDimensions = []string{precisionCategory, precisionTier}
)

// CostDimensions returns the cost groupings the cost view serves. Each call
// returns a fresh slice so callers cannot mutate the layer's vocabulary.
func CostDimensions() []string { return slices.Clone(costDimensions) }

// PrecisionDimensions returns the precision groupings the precision view
// serves. Each call returns a fresh slice so callers cannot mutate the
// layer's vocabulary.
func PrecisionDimensions() []string { return slices.Clone(precisionDimensions) }
