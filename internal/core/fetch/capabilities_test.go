package fetchcore

import (
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// allProviders lists every host the fetch verb accepts.
func allProviders() []string {
	return []string{providerGitHub, "gitlab", "gitcafe", "forgejo", "bitbucket", "azure"}
}

// postsTo lists the hosts whose submit path is implemented: GitHub's single
// atomic review POST and GitLab's discussion-per-finding replay.
func postsTo(provider string) bool {
	return provider == providerGitHub || provider == providerGitLab
}

// TestCapabilityWire proves every declared host surfaces the same additive
// object and that only the hosts with a submit path advertise post.
func TestCapabilityWire(t *testing.T) {
	t.Parallel()
	for _, provider := range allProviders() {
		t.Run(provider, func(t *testing.T) {
			t.Parallel()
			checkCapabilityWire(t, provider)
		})
	}
}

// checkCapabilityWire asserts one host's capability object.
func checkCapabilityWire(t *testing.T, provider string) {
	t.Helper()
	obj := capabilityWire(provider)
	for _, key := range []string{"provider", "fetch", "post", "inline_anchors", "thread_resolution", "review_verdicts"} {
		if _, ok := obj.Get(key); !ok {
			t.Errorf("capabilities lacks %q", key)
		}
	}
	checkCapabilityValue(t, obj, "provider", provider)
	checkCapabilityValue(t, obj, "fetch", true)
	checkCapabilityValue(t, obj, "post", postsTo(provider))
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
