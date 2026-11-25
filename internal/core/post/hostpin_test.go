package postcore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

// TestPinBundleHost pins where a post's host calls go: always to the host the
// bundle was fetched from. A target naming another host refuses naming both;
// a typed pair (no host on the context) takes the bundle's; Forgejo, which has
// no CLI to pin, refuses when FORGEJO_HOST names another host.
func TestPinBundleHost(t *testing.T) {
	t.Parallel()
	const ghPR = `{"url":"https://github.com/acme/app/pull/5"}`
	const forgePR = `{"provider":"forgejo","url":"https://forge.corp.example:3000/acme/app/pulls/5"}`
	for _, tc := range []pinCase{
		{name: "typed pair takes the bundle's host", record: ghPR, want: "github.com"},
		{name: "same host", record: ghPR, target: "github.com", want: "github.com"},
		{
			name: "target on another host", record: ghPR, target: "ghe.corp.example",
			refusal: []string{"ghe.corp.example", "github.com"},
		},
		{name: "no recorded URL keeps the target", record: `{}`, target: "github.com", want: "github.com"},
		{
			name: "forgejo on FORGEJO_HOST", record: forgePR, forgejoHost: "https://forge.corp.example:3000",
			want: "forge.corp.example:3000",
		},
		{
			name: "forgejo host moved", record: forgePR, forgejoHost: "https://forge.corp.example",
			refusal: []string{"forge.corp.example:3000", "FORGEJO_HOST names forge.corp.example and"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkPin(t, &tc)
		})
	}
}

type pinCase struct {
	name, record, target, forgejoHost string
	want                              string
	refusal                           []string
}

func checkPin(t *testing.T, tc *pinCase) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "PR.json"), []byte(tc.record), 0o644); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{}
	if tc.forgejoHost != "" {
		env["FORGEJO_HOST"] = tc.forgejoHost
	}
	st := &state{dir: dir, req: &Request{Runtime: &core.Runtime{Env: env}}}
	ctx, ref := st.pinBundleHost(core.WithHost(t.Context(), tc.target))
	if tc.refusal != nil {
		checkPinRefusal(t, ref, tc.refusal)
		return
	}
	if ref != nil || core.HostOf(ctx) != tc.want {
		t.Fatalf("pinned host = %q (refusal %v), want %q", core.HostOf(ctx), ref, tc.want)
	}
}

func checkPinRefusal(t *testing.T, ref *core.RefusalError, names []string) {
	t.Helper()
	if ref == nil || ref.Code != core.CodeHostMismatch {
		t.Fatalf("want a host-mismatch refusal, got %v", ref)
	}
	for _, s := range names {
		if !strings.Contains(ref.Message, s) {
			t.Errorf("refusal does not name %q: %s", s, ref.Message)
		}
	}
}
