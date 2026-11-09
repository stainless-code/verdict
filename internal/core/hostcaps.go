package core

// HostCapabilities declares what verdict can do through one code host's own
// CLI or API. It is the single source for the fetch envelope's
// data.capabilities object and for the gates that depend on a host feature, so
// an agent learns "post is GitHub and GitLab only" before writing a review
// instead of by refusal.
//
// A capability that reads true has code behind it and a test that fails when
// it flips: post and inline_anchors are the submit paths, approve is the
// host's own approval write, and request_changes is the review event a host
// accepts. thread_resolution has no code on any host, so it reads false
// everywhere and its reason says why.
type HostCapabilities struct {
	Provider         string
	Fetch            bool
	Post             bool
	InlineAnchors    bool
	Approve          bool
	RequestChanges   bool
	ThreadResolution bool
	// ReviewVerdicts is the compatibility sum of Approve and RequestChanges.
	// CapabilitiesFor derives it, so no table row can claim a verdict pair it
	// does not carry; new readers branch on the two split flags instead.
	ReviewVerdicts bool
}

// Capability names as published in data.capabilities, in wire order. The
// reason map keys these same names.
const (
	capabilityFetch            = "fetch"
	capabilityPost             = "post"
	capabilityInlineAnchors    = "inline_anchors"
	capabilityApprove          = "approve"
	capabilityRequestChanges   = "request_changes"
	capabilityThreadResolution = "thread_resolution"
)

// Capability reasons are machine-readable codes, not prose: an agent branches
// on the exact string, and docs/manual.md § Providers maps each code to the
// operator sentence. Codes are stable; a new gap adds a code.
const (
	// reasonUnknownHost is the whole row of a provider verdict has never heard
	// of: every host verdict knows how to read reads.
	reasonUnknownHost = "unknown-host"
	// reasonFetchOnly names a host with no submit path at all.
	reasonFetchOnly = "fetch-only"
	// reasonNoApproveVerdict names a host that posts without an approval write.
	reasonNoApproveVerdict = "no-approve-verdict"
	// reasonNoChangesEvent names a host with no request-changes review event;
	// the submit path posts the findings and summary note and says so.
	reasonNoChangesEvent = "no-request-changes-event"
	// reasonNoInlineAnchors names a host that takes a body but no anchored
	// comment.
	reasonNoInlineAnchors = "no-inline-anchors"
	// reasonNoResolveMutation names the gap every host shares: verdict ships
	// no thread-resolve call, so resolution stays a manual host/CLI step.
	reasonNoResolveMutation = "no-resolve-mutation"
)

// CapabilityGap is one absent capability and the code that says why.
type CapabilityGap struct {
	Capability string
	Reason     string
}

// hostCapabilities lists every supported host. Reading threads is part of
// fetch, so fetch is true everywhere; posting — and with it inline anchors —
// lands on GitHub (one atomic review) and GitLab (one discussion per finding,
// then the summary note, then the approval last). GitHub carries both binding
// verdicts; GitLab carries the approval only, because it has no
// request-changes review event, so request_changes reads false there and the
// submit path posts the findings plus a warning instead of inventing a verdict
// the host cannot record. review_verdicts is derived from that pair by
// CapabilitiesFor, never declared here. Thread resolution reads false on every
// host: verdict has no resolve mutation, and the bundled prompts leave
// resolution to the host's own CLI.
var hostCapabilities = map[string]HostCapabilities{
	"github": {
		Provider: "github", Fetch: true, Post: true,
		InlineAnchors: true, Approve: true, RequestChanges: true,
	},
	"gitlab": {
		Provider: "gitlab", Fetch: true, Post: true, InlineAnchors: true,
		Approve: true,
	},
	"gitcafe":   {Provider: "gitcafe", Fetch: true},
	"forgejo":   {Provider: "forgejo", Fetch: true},
	"bitbucket": {Provider: "bitbucket", Fetch: true},
	"azure":     {Provider: "azure", Fetch: true},
}

// CapabilitiesFor returns the declared capabilities for provider. Unknown
// providers read as fetch-less so a caller never advertises a host it cannot
// read. The returned value carries the derived verdict sum, so a caller never
// has to recompute or trust a declared one.
func CapabilitiesFor(provider string) HostCapabilities {
	caps, ok := hostCapabilities[provider]
	if !ok {
		caps = HostCapabilities{Provider: provider}
	}
	caps.ReviewVerdicts = caps.Approve && caps.RequestChanges
	return caps
}

// Unsupported lists every absent capability with its reason, in capability
// order. Reasons are derived from the flags, never stored beside them, so a
// capability that flips on loses its reason instead of keeping a stale one.
func (c HostCapabilities) Unsupported() []CapabilityGap {
	if !c.Fetch {
		// Only an unregistered provider is fetch-less, so one cause explains
		// the whole row.
		return []CapabilityGap{
			{Capability: capabilityFetch, Reason: reasonUnknownHost},
			{Capability: capabilityPost, Reason: reasonUnknownHost},
			{Capability: capabilityInlineAnchors, Reason: reasonUnknownHost},
			{Capability: capabilityApprove, Reason: reasonUnknownHost},
			{Capability: capabilityRequestChanges, Reason: reasonUnknownHost},
			{Capability: capabilityThreadResolution, Reason: reasonUnknownHost},
		}
	}
	gaps := make([]CapabilityGap, 0, 5)
	if !c.Post {
		gaps = append(gaps, CapabilityGap{Capability: capabilityPost, Reason: reasonFetchOnly})
	}
	if !c.InlineAnchors {
		gaps = append(gaps, CapabilityGap{Capability: capabilityInlineAnchors, Reason: c.submitReason(reasonNoInlineAnchors)})
	}
	if !c.Approve {
		gaps = append(gaps, CapabilityGap{Capability: capabilityApprove, Reason: c.submitReason(reasonNoApproveVerdict)})
	}
	if !c.RequestChanges {
		gaps = append(gaps, CapabilityGap{Capability: capabilityRequestChanges, Reason: c.submitReason(reasonNoChangesEvent)})
	}
	if !c.ThreadResolution {
		gaps = append(gaps, CapabilityGap{Capability: capabilityThreadResolution, Reason: reasonNoResolveMutation})
	}
	return gaps
}

// submitReason names why one submit-side capability is absent: the host has no
// submit path at all, or it posts without that one write.
func (c HostCapabilities) submitReason(without string) string {
	if !c.Post {
		return reasonFetchOnly
	}
	return without
}
