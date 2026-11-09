package fetchcore

import (
	"slices"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// capabilityKeys is the published key order of data.capabilities: identity,
// the booleans, the compatibility sum, then the reasons.
var capabilityKeys = []string{
	"provider", "fetch", "post", "inline_anchors", "approve",
	"request_changes", "thread_resolution", "review_verdicts", "unsupported",
}

// allProviders lists every host the fetch verb accepts.
func allProviders() []string {
	return []string{providerGitHub, providerGitLab, "gitcafe", "forgejo", "bitbucket", "azure"}
}

// postsTo lists the hosts whose submit path is implemented: GitHub's single
// atomic review POST and GitLab's discussion-per-finding replay.
func postsTo(provider string) bool {
	return provider == providerGitHub || provider == providerGitLab
}

// capabilityRow pins one host's whole capability answer. Flipping a bit in
// core/hostCapabilities — on or off — fails this table, and so does a reason
// that stops matching its flag.
type capabilityRow struct {
	provider         string
	fetch            bool
	post             bool
	inlineAnchors    bool
	approve          bool
	requestChanges   bool
	threadResolution bool
	reasons          map[string]string
}

// submitGaps are the reasons every fetch-only host carries: no submit path, so
// no anchors and no binding verdicts, while thread resolution is absent on
// every host for its own reason.
var submitGaps = map[string]string{
	"post":              "fetch-only",
	"inline_anchors":    "fetch-only",
	"approve":           "fetch-only",
	"request_changes":   "fetch-only",
	"thread_resolution": "no-resolve-mutation",
}

// capabilityRows is the pinned per-host table the envelope must publish.
func capabilityRows() []capabilityRow {
	return []capabilityRow{
		{
			provider: providerGitHub, fetch: true, post: true, inlineAnchors: true,
			approve: true, requestChanges: true,
			reasons: map[string]string{"thread_resolution": "no-resolve-mutation"},
		},
		{
			provider: providerGitLab, fetch: true, post: true, inlineAnchors: true,
			approve: true,
			reasons: map[string]string{
				"request_changes":   "no-request-changes-event",
				"thread_resolution": "no-resolve-mutation",
			},
		},
		{provider: "gitcafe", fetch: true, reasons: submitGaps},
		{provider: "forgejo", fetch: true, reasons: submitGaps},
		{provider: "bitbucket", fetch: true, reasons: submitGaps},
		{provider: "azure", fetch: true, reasons: submitGaps},
	}
}

// TestCapabilityWire proves every declared host surfaces the same additive
// object with its pinned booleans, the derived verdict sum, and one reason per
// absent capability.
func TestCapabilityWire(t *testing.T) {
	t.Parallel()
	for _, row := range capabilityRows() {
		t.Run(row.provider, func(t *testing.T) {
			t.Parallel()
			checkCapabilityWire(t, row)
		})
	}
	t.Run("unknown host", func(t *testing.T) {
		t.Parallel()
		checkCapabilityWire(t, capabilityRow{
			provider: "codeberg",
			reasons: map[string]string{
				"fetch":             "unknown-host",
				"post":              "unknown-host",
				"inline_anchors":    "unknown-host",
				"approve":           "unknown-host",
				"request_changes":   "unknown-host",
				"thread_resolution": "unknown-host",
			},
		})
	})
}

// checkCapabilityWire asserts one host's capability object.
func checkCapabilityWire(t *testing.T, row capabilityRow) {
	t.Helper()
	obj := capabilityWire(row.provider)
	if !slices.Equal(obj.Keys, capabilityKeys) {
		t.Fatalf("keys = %v, want %v", obj.Keys, capabilityKeys)
	}
	for _, field := range []struct {
		key  string
		want bool
		got  any
	}{
		{key: "fetch", want: row.fetch},
		{key: "post", want: row.post},
		{key: "inline_anchors", want: row.inlineAnchors},
		{key: "approve", want: row.approve},
		{key: "request_changes", want: row.requestChanges},
		{key: "thread_resolution", want: row.threadResolution},
	} {
		checkCapabilityValue(t, obj, field.key, field.want)
	}
	checkCapabilityValue(t, obj, "provider", row.provider)
	checkCapabilityValue(t, obj, "review_verdicts", row.approve && row.requestChanges)
	checkUnsupported(t, obj, row.reasons)
}

// checkUnsupported asserts the reason map holds exactly the absent
// capabilities, keyed in capability order: a false flag without a reason, a
// reason on a true flag, and a stale code all fail.
func checkUnsupported(t *testing.T, obj *jq.Obj, want map[string]string) {
	t.Helper()
	raw, ok := obj.Get("unsupported")
	unsupported, isObj := raw.(*jq.Obj)
	if !ok || !isObj || unsupported == nil {
		t.Fatalf("unsupported = %v, want an object", raw)
	}
	wantKeys := make([]string, 0, len(want))
	for key := range want {
		wantKeys = append(wantKeys, key)
	}
	slices.Sort(wantKeys)
	gotKeys := slices.Clone(unsupported.Keys)
	slices.Sort(gotKeys)
	if !slices.Equal(gotKeys, wantKeys) {
		t.Fatalf("unsupported keys = %v, want %v", unsupported.Keys, wantKeys)
	}
	for _, key := range wantKeys {
		if got, _ := unsupported.Get(key); got != want[key] {
			t.Errorf("unsupported[%q] = %v, want %q", key, got, want[key])
		}
		if got, _ := obj.Get(key); got != false {
			t.Errorf("unsupported names %q while the capability reads %v", key, got)
		}
	}
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
