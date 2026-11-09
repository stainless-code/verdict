package fetchcore

import (
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// capabilityWire renders one host's declared capabilities in published key
// order: the identity first, then the booleans an agent plans with, then the
// compatibility sum, then one machine-readable reason per absent capability.
// review_verdicts stays in place as approve && request_changes so a pre-split
// consumer keeps reading the same truth.
func capabilityWire(provider string) *jq.Obj {
	caps := core.CapabilitiesFor(provider)
	obj := jq.NewObj(
		keyProvider, caps.Provider,
		core.CapabilityFetch, caps.Fetch,
		core.CapabilityPost, caps.Post,
		core.CapabilityInlineAnchors, caps.InlineAnchors,
		core.CapabilityApprove, caps.Approve,
		core.CapabilityRequestChanges, caps.RequestChanges,
		core.CapabilityThreadResolution, caps.ThreadResolution,
		"review_verdicts", caps.ReviewVerdicts,
	)
	unsupported := jq.NewObj()
	for _, gap := range caps.Unsupported() {
		unsupported.Set(gap.Capability, gap.Reason)
	}
	obj.Set("unsupported", unsupported)
	return obj
}
