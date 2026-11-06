package readcommon

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLocalHeadUsesSourceOnlyWithoutPR checks source and provider heads.
func TestLocalHeadUsesSourceOnlyWithoutPR(t *testing.T) {
	for _, tc := range []struct{ name, bundle, pr, want string }{
		{"local source", "local-review", "", "local-head"},
		{"provider missing", "42", "", ""},
		{"corrupt PR", "local-review", "{broken", ""},
		{"provider wins", "local-review", `{"headRefOid":"provider-head"}`, "provider-head"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := localHeadFixture(t, tc.bundle, tc.pr)
			if got := OpenBundle(dir).Head(); got != tc.want {
				t.Errorf("Head() = %q; want %q", got, tc.want)
			}
		})
	}
}

func localHeadFixture(t *testing.T, bundle, pr string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), bundle)
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeStore(t, dir, "source.json", `{"kind":"range","head_sha":"local-head"}`)
	if pr != "" {
		writeStore(t, dir, "PR.json", pr)
	}
	return dir
}
