package readycore

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadyKeepsPresentEmptyProviderMarker(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "PR.json"), []byte(`{"provider":""}`), 0o600); err != nil {
		t.Fatal(err)
	}
	st := &state{}
	_, ref := st.readyHost(dir)
	if ref == nil || ref.Code != "provider-unimplemented" {
		t.Fatalf("empty marker refusal = %+v", ref)
	}
}
