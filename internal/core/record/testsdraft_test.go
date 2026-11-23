package recordcore

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
)

func TestDraftsEnabledOffDisablesDrafting(t *testing.T) {
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
	if st.draftsEnabled() {
		t.Fatal("off should disable drafting")
	}
}

func TestDraftsEnabledDefaultOnReproduce(t *testing.T) {
	for _, res := range []*config.Resolved{
		nil,
		{},
		{Tests: &core.ConfigTests{}},
		{Tests: &core.ConfigTests{Draft: "on-reproduce"}},
	} {
		st := &state{run: &config.RunState{Resolved: res}}
		if res == nil {
			st.run = nil
		}
		if !st.draftsEnabled() {
			t.Fatalf("resolved %+v should enable drafting", res)
		}
	}
}
