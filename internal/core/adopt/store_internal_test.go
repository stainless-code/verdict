package adoptcore

import (
	"os"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/identity"
)

// TestScanPatchTwinsRefusesAStoreItCannotList proves the twin scan refuses
// corrupt-store naming a bundle home that is not a directory, instead of
// reporting no twin: a file where the store belongs must never read as
// "nothing to adopt".
func TestScanPatchTwinsRefusesAStoreItCannotList(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.MkdirAll(".verdict", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(".verdict/state", []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := &state{req: autoRequest("auto")}
	_, ref := st.scanPatchTwins(t.Context(), &identity.Identity{})
	if ref == nil || ref.Code != "corrupt-store" {
		t.Fatalf("scan over a file store = %v; want corrupt-store", ref)
	}
	if !strings.Contains(ref.Message, ".verdict/state") {
		t.Errorf("message = %q; want it to name the store", ref.Message)
	}
}
