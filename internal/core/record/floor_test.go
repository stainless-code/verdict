package recordcore

import (
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

func TestFloorWithholdsAndWarnsForNull(t *testing.T) {
	info := jq.NewObj("sig", "info", "path", "a.go", "severity", "informational", "status", "open", "history", []any{})
	unlabelled := jq.NewObj("sig", "unknown", "path", "b.go", "severity", nil, "status", "open", "history", []any{})
	st := &state{cmd: "post", minSeverity: "important", entries: []any{info, unlabelled}, now: "2026-11-13T12:00:00Z"}
	st.applyPostFloor()
	if readcommon.WithheldSource(info) != "below-severity" || suppressionString(info, "status") != "open" {
		t.Fatalf("info = %s", info.MarshalCompact())
	}
	if unlabelled.Has("withheld") {
		t.Fatal("null severity was withheld")
	}
	if strings.Join(st.warnings, "\n") != "1 finding(s) carry no severity; the floor does not apply" {
		t.Fatalf("warnings = %q", st.warnings)
	}
	st.minSeverity = "off"
	st.applyPostFloor()
	if info.Has("withheld") {
		t.Fatal("off did not clear below-severity withholding")
	}
}
