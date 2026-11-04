package fetchcore

import (
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// capabilityWire renders one host's declared capabilities in published key
// order: the identity first, then the booleans an agent plans with.
func capabilityWire(provider string) *jq.Obj {
	caps := core.CapabilitiesFor(provider)
	return jq.NewObj(
		keyProvider, caps.Provider,
		"fetch", caps.Fetch,
		"post", caps.Post,
		"inline_anchors", caps.InlineAnchors,
		"thread_resolution", caps.ThreadResolution,
		"review_verdicts", caps.ReviewVerdicts,
	)
}
