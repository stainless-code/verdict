package fetchcore

import (
	"slices"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// capabilityKeys is the published key order of data.capabilities: identity,
// the booleans, the compatibility sum, then the reasons. It is the one thing
// this file still spells by hand — the wire order a pre-split consumer reads —
// and it is a property of the renderer, not a copy of the capability matrix.
var capabilityKeys = []string{
	"provider", core.CapabilityFetch, core.CapabilityPost, core.CapabilityInlineAnchors,
	core.CapabilityApprove, core.CapabilityRequestChanges, core.CapabilityThreadResolution,
	"review_verdicts", "unsupported",
}

// allProviders lists every host the fetch verb accepts.
func allProviders() []string {
	return []string{providerGitHub, providerGitLab, "gitcafe", "forgejo", "bitbucket", "azure"}
}

// postsTo lists the hosts whose submit path is implemented: GitHub's and
// Forgejo's single atomic review POST, and GitLab's discussion-per-finding
// replay. It is the independent half of TestCapabilitiesMatchPostGate: an
// advertised bit, not a table this file restates.
func postsTo(provider string) bool {
	return provider == providerGitHub || provider == providerGitLab || provider == "forgejo"
}

// TestCapabilityWire proves the envelope's capability object is the capability
// table rendered, for every host verdict reads and for one it does not: the
// published key order, every boolean the table declares, the derived verdict
// sum, and exactly the reason codes CapabilitiesFor reports for the absent
// ones.
//
// This file used to carry a hand-copied per-host table. That copy was the drift
// shape STA-298 exists to kill: two places to edit, one of them wrong. The
// matrix is pinned where it lives (internal/core/hostcaps_test.go), and what
// each host actually does is pinned by the post conformance suite, which
// flips a declared bit and watches the submit path move. What is left here is
// the renderer: the ordering and the cross-check that a rendered boolean never
// contradicts the reason map beside it, which is what catches a field the
// renderer wired to the wrong flag.
func TestCapabilityWire(t *testing.T) {
	t.Parallel()
	for _, provider := range append(allProviders(), "codeberg") {
		t.Run(provider, func(t *testing.T) {
			t.Parallel()
			checkCapabilityWire(t, provider)
		})
	}
}

// checkCapabilityWire asserts one host's rendered capability object against
// the table it must render.
func checkCapabilityWire(t *testing.T, provider string) {
	t.Helper()
	caps := core.CapabilitiesFor(provider)
	obj := capabilityWire(provider)
	if !slices.Equal(obj.Keys, capabilityKeys) {
		t.Fatalf("keys = %v, want %v", obj.Keys, capabilityKeys)
	}
	checkCapabilityValue(t, obj, "provider", caps.Provider)
	for _, field := range []struct {
		key  string
		want bool
	}{
		{key: core.CapabilityFetch, want: caps.Fetch},
		{key: core.CapabilityPost, want: caps.Post},
		{key: core.CapabilityInlineAnchors, want: caps.InlineAnchors},
		{key: core.CapabilityApprove, want: caps.Approve},
		{key: core.CapabilityRequestChanges, want: caps.RequestChanges},
		{key: core.CapabilityThreadResolution, want: caps.ThreadResolution},
		{key: "review_verdicts", want: caps.Approve && caps.RequestChanges},
	} {
		checkCapabilityValue(t, obj, field.key, field.want)
	}
	checkUnsupported(t, obj, caps)
}

// checkUnsupported asserts the reason map holds exactly the capabilities the
// table reports absent, keyed in capability order: a false flag without a
// reason, a reason on a true flag, and a stale code all fail. It is also the
// guard against a renderer field wired to the wrong flag, because a swap moves
// a rendered boolean away from the reason the table publishes for it.
func checkUnsupported(t *testing.T, obj *jq.Obj, caps core.HostCapabilities) {
	t.Helper()
	raw, ok := obj.Get("unsupported")
	unsupported, isObj := raw.(*jq.Obj)
	if !ok || !isObj || unsupported == nil {
		t.Fatalf("unsupported = %v, want an object", raw)
	}
	want := map[string]string{}
	for _, gap := range caps.Unsupported() {
		want[gap.Capability] = gap.Reason
	}
	gotKeys := slices.Sorted(slices.Values(unsupported.Keys))
	wantKeys := slices.Sorted(slices.Values(mapKeys(want)))
	if !slices.Equal(gotKeys, wantKeys) {
		t.Fatalf("unsupported keys = %v, want %v", unsupported.Keys, wantKeys)
	}
	for _, key := range wantKeys {
		checkAbsentCapability(t, obj, unsupported, key, want[key])
	}
}

// mapKeys lists a map's keys so the comparison pattern stays one shape.
func mapKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	return out
}

// checkCapabilityValue compares one rendered capability field.
func checkCapabilityValue(t *testing.T, obj *jq.Obj, key string, want any) {
	t.Helper()
	if got, _ := obj.Get(key); got != want {
		t.Errorf("%s = %v, want %v", key, got, want)
	}
}

// TestCapabilitiesMatchPostGate proves the advertised table and the post
// gate agree: only hosts whose capabilities allow posting pass the gate.
func TestCapabilitiesMatchPostGate(t *testing.T) {
	t.Parallel()
	for _, provider := range allProviders() {
		if got, want := core.CapabilitiesFor(provider).Post, postsTo(provider); got != want {
			t.Errorf("CapabilitiesFor(%s).Post = %v, want %v", provider, got, want)
		}
	}
}

// checkAbsentCapability asserts one reason names the right cause and sits
// beside a capability that really reads false: a reason on a working
// capability is a stale code, not a gap.
func checkAbsentCapability(t *testing.T, obj, unsupported *jq.Obj, key, reason string) {
	t.Helper()
	if got, _ := unsupported.Get(key); got != reason {
		t.Errorf("unsupported[%q] = %v, want %q", key, got, reason)
	}
	if got, _ := obj.Get(key); got != false {
		t.Errorf("unsupported names %q while the capability reads %v", key, got)
	}
}
