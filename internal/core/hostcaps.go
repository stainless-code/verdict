package core

// HostCapabilities declares what verdict can do against one code host. It is
// the single source for the fetch envelope's data.capabilities object and the
// post gate, so an agent learns which hosts can take a review before writing
// one instead of by refusal.
type HostCapabilities struct {
	Provider         string
	Fetch            bool
	Post             bool
	InlineAnchors    bool
	ThreadResolution bool
	ReviewVerdicts   bool
}

// hostCapabilities lists every supported host. Reading threads is part of
// fetch, so fetch is true everywhere; posting — and with it inline anchors —
// lands on GitHub (one atomic review) and GitLab (one discussion per finding,
// then the summary note, then the approval last). GitLab carries an approval
// but has no request-changes review event, so review_verdicts stays false
// there: the flag advertises both verdicts, and a half-claim would let an
// agent ask for the one that silently degrades to a comment. Thread
// resolution stays with each host's own UI.
var hostCapabilities = map[string]HostCapabilities{
	"github": {
		Provider: "github", Fetch: true, Post: true,
		InlineAnchors: true, ThreadResolution: true, ReviewVerdicts: true,
	},
	"gitlab": {
		Provider: "gitlab", Fetch: true, Post: true, InlineAnchors: true,
	},
	"gitcafe":   {Provider: "gitcafe", Fetch: true},
	"forgejo":   {Provider: "forgejo", Fetch: true},
	"bitbucket": {Provider: "bitbucket", Fetch: true},
	"azure":     {Provider: "azure", Fetch: true},
}

// CapabilitiesFor returns the declared capabilities for provider. Unknown
// providers read as fetch-less so a caller never advertises a host it cannot
// read.
func CapabilitiesFor(provider string) HostCapabilities {
	if caps, ok := hostCapabilities[provider]; ok {
		return caps
	}
	return HostCapabilities{Provider: provider}
}
