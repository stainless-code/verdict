package core

import (
	"slices"
	"testing"
)

// TestCapabilityTable pins every registered host's whole capability row, so
// flipping any declared bit — on or off — fails here instead of shipping a
// claim no code backs.
func TestCapabilityTable(t *testing.T) {
	t.Parallel()
	want := map[string]HostCapabilities{
		"github": {
			Provider: "github", Fetch: true, Post: true, InlineAnchors: true,
			Approve: true, RequestChanges: true, ReviewVerdicts: true,
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
	got := make([]string, 0, len(hostCapabilities))
	for provider := range hostCapabilities {
		got = append(got, provider)
	}
	slices.Sort(got)
	wantProviders := make([]string, 0, len(want))
	for provider := range want {
		wantProviders = append(wantProviders, provider)
	}
	slices.Sort(wantProviders)
	if !slices.Equal(got, wantProviders) {
		t.Fatalf("registered hosts = %v, want %v", got, wantProviders)
	}
	for provider, caps := range want {
		if gotCaps := CapabilitiesFor(provider); gotCaps != caps {
			t.Errorf("CapabilitiesFor(%q) = %+v, want %+v", provider, gotCaps, caps)
		}
	}
}

// TestReviewVerdictsIsTheAND proves the compatibility sum cannot be declared:
// it is approve && request_changes on every host, including the unknown one.
func TestReviewVerdictsIsTheAND(t *testing.T) {
	t.Parallel()
	for _, provider := range append([]string{"codeberg"}, "github", "gitlab", "gitcafe", "forgejo", "bitbucket", "azure") {
		caps := CapabilitiesFor(provider)
		if got, want := caps.ReviewVerdicts, caps.Approve && caps.RequestChanges; got != want {
			t.Errorf("CapabilitiesFor(%q).ReviewVerdicts = %v, want %v", provider, got, want)
		}
	}
}

// TestUnknownHostClaimsNothing proves an unregistered provider advertises no
// capability and says why for each one.
func TestUnknownHostClaimsNothing(t *testing.T) {
	t.Parallel()
	caps := CapabilitiesFor("codeberg")
	if caps.Fetch || caps.Post || caps.InlineAnchors || caps.Approve ||
		caps.RequestChanges || caps.ReviewVerdicts || caps.ThreadResolution {
		t.Fatalf("unknown host claims %+v", caps)
	}
	gaps := caps.Unsupported()
	if len(gaps) != 6 {
		t.Fatalf("gaps = %v, want six capabilities", gaps)
	}
	for _, gap := range gaps {
		if gap.Reason != reasonUnknownHost {
			t.Errorf("gap %q reason = %q, want %q", gap.Capability, gap.Reason, reasonUnknownHost)
		}
	}
}

// TestUnsupportedGapsAreDerived proves the reason map follows the flags: every
// absent capability has exactly one reason, and no present capability has one.
func TestUnsupportedGapsAreDerived(t *testing.T) {
	t.Parallel()
	for _, provider := range []string{"github", "gitlab", "gitcafe", "forgejo", "bitbucket", "azure", "codeberg"} {
		caps := CapabilitiesFor(provider)
		unsupported := reasonsByCapability(t, provider, caps)
		for _, field := range capabilityFlags(caps) {
			if want, has := !field.on, unsupported[field.name]; want != has {
				t.Errorf("%s: %s absent=%v, reason reported=%v", provider, field.name, want, has)
			}
		}
	}
}

// reasonsByCapability collects one host's published reason map, failing on a
// gap that carries no reason and on a capability reported twice.
func reasonsByCapability(t *testing.T, provider string, caps HostCapabilities) map[string]bool {
	t.Helper()
	unsupported := map[string]bool{}
	for _, gap := range caps.Unsupported() {
		if gap.Reason == "" {
			t.Errorf("%s: gap %q carries no reason", provider, gap.Capability)
		}
		if unsupported[gap.Capability] {
			t.Errorf("%s: gap %q reported twice", provider, gap.Capability)
		}
		unsupported[gap.Capability] = true
	}
	return unsupported
}

// capabilityFlag pairs a published capability name with the host's value for
// it, so the reason map can be checked once per name without a hand-written
// list in each test.
func capabilityFlags(caps HostCapabilities) []struct {
	name string
	on   bool
} {
	return []struct {
		name string
		on   bool
	}{
		{name: capabilityFetch, on: caps.Fetch},
		{name: capabilityPost, on: caps.Post},
		{name: capabilityInlineAnchors, on: caps.InlineAnchors},
		{name: capabilityApprove, on: caps.Approve},
		{name: capabilityRequestChanges, on: caps.RequestChanges},
		{name: capabilityThreadResolution, on: caps.ThreadResolution},
	}
}
