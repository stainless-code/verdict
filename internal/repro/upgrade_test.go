package repro

import (
	"os"
	"path/filepath"
	"testing"
)

const rawArtifact = "tests/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-161139e764cc.py"

// TestRecaptureAcceptsSidecarWithRawControls covers a bundle whose sidecar was
// written before verdict escaped C1 and bidi controls: it holds them raw, so
// it no longer matches the re-encoded bytes, yet it is the same receipt and
// must not cost a re-record its reproduction.
//
//nolint:revive // the dry run, the real run, and the refusal share one raw bundle
func TestRecaptureAcceptsSidecarWithRawControls(t *testing.T) {
	// Built from code points so no tool rewrites the source into escapes.
	label := "x" + string([]rune{0x85, 0x9B, 0x202E, 0x2066})
	old := `{"path":"` + rawArtifact + `","command":"python3 ` + rawArtifact + ` ` + label + `","redacted":false}`
	dir, sidecar := rawSidecarBundle(t, old)
	for _, dry := range []bool{true, false} {
		receipt, err := Capture(dir, testSig, "probe.py", "python3 {artifact} "+label, dry)
		if err != nil {
			t.Fatalf("Capture(dry=%v) over a raw sidecar: %v", dry, err)
		}
		if receipt.Path != rawArtifact || receipt.Command != "python3 "+rawArtifact+" "+label {
			t.Fatalf("receipt = %+v", receipt)
		}
	}
	if raw, err := os.ReadFile(sidecar); err != nil || string(raw) != old {
		t.Fatalf("retained sidecar = %q, %v; immutable evidence must stay as written", raw, err)
	}
	// Only the spelling of those runes is forgiven: another command still refuses.
	if _, err := Capture(dir, testSig, "probe.py", "python3 {artifact} other"+label, false); err == nil {
		t.Fatal("accepted a different command at an immutable sidecar name")
	}
}

// rawSidecarBundle writes a bundle holding a probe, its retained artifact, and
// the given sidecar bytes; it returns the bundle and the sidecar path.
func rawSidecarBundle(t *testing.T, sidecarBody string) (dir, sidecar string) {
	t.Helper()
	dir = t.TempDir()
	sidecar = filepath.Join(dir, "tests", testSig+"-161139e764cc.command.json")
	if err := os.Mkdir(filepath.Join(dir, "tests"), 0o700); err != nil {
		t.Fatal(err)
	}
	for path, body := range map[string]string{
		filepath.Join(dir, "probe.py"):                      "assert False\n",
		filepath.Join(dir, filepath.FromSlash(rawArtifact)): "assert False\n",
		sidecar: sidecarBody,
	} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir, sidecar
}
