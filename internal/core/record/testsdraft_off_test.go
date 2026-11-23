package recordcore

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

func TestCaptureOffWritesNoArtifact(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "probe.py"), []byte("assert False\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	st := &state{
		dir: dir,
		cmd: recordCommand,
		run: &config.RunState{Resolved: &config.Resolved{
			Tests: &core.ConfigTests{Draft: "off"},
		}},
	}
	entry := jq.NewObj("sig", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "rigor", "reproduced",
		"reproduction", jq.NewObj("path", "probe.py", "command", "python3 {artifact}"))
	st.captureEntryReproduction(entry, 1048576)
	if _, has := entry.Get("reproduction"); has {
		t.Fatal("off should retain no reproduction receipt")
	}
	if _, err := os.Stat(filepath.Join(dir, "tests")); !os.IsNotExist(err) {
		t.Fatalf("off should write no artifact dir: %v", err)
	}
}
