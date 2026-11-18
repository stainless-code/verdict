package recordcore

import (
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
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

func TestFloorHidesSectionAcrossHorizontalRule(t *testing.T) {
	e := jq.NewObj("heading", "### Low", "withheld", jq.NewObj("source", "below-severity", "reason", "below floor", "at", "2026-11-13T12:00:00Z"))
	st := &state{entries: []any{e}, effective: core.Effective{PublishFooter: core.FlagOn}}
	body := "### Low\nprivate\n---\nprivate after rule\n### High\nvisible\n\n---\n\n<!-- verdict-footer -->\nReceipt\n"
	got := st.hideWithheldSections(body)
	if got != "### High\nvisible\n\n---\n\n<!-- verdict-footer -->\nReceipt\n" {
		t.Fatalf("section leaked or footer changed: %q", got)
	}
}

func TestWithheldSectionNestedFences(t *testing.T) {
	for _, code := range []string{
		"````text\n```\n## fake\nPRIVATE\n```\n### fake\nPRIVATE\n````\n",
		"~~~~text\n~~~\n## fake\nPRIVATE\n```\n### fake\nPRIVATE\n~~~~\n",
	} {
		e := jq.NewObj("heading", "### Low", "withheld", jq.NewObj("source", "agent", "reason", "An intentional compatibility exception.", "at", "2026-11-13T12:00:00Z"))
		st := &state{entries: []any{e}}
		got := st.hideWithheldSections("### Low\n" + code + "### High\npublic finding\n")
		if got != "### High\npublic finding\n" {
			t.Fatalf("fenced section leaked: %q", got)
		}
	}
}

func TestFloorDoesNotWarnForAlreadyWithheldNullSeverity(t *testing.T) {
	agentWithheld := jq.NewObj("sig", "a1", "path", "a.go", "severity", nil, "status", "open",
		"withheld", jq.NewObj("source", "agent", "reason", "An intentional compatibility exception.", "at", "2026-11-13T12:00:00Z"))
	exclWithheld := jq.NewObj("sig", "a2", "path", "vendor/a.go", "severity", nil, "status", "open",
		"withheld", jq.NewObj("source", "exclusion", "reason", "excluded path", "at", "2026-11-13T12:00:00Z"))
	st := &state{cmd: "post", minSeverity: "important", entries: []any{agentWithheld, exclWithheld}, now: "2026-11-13T12:00:00Z"}
	st.applyPostFloor()
	if len(st.warnings) != 0 {
		t.Fatalf("warnings = %q; want none for already-withheld unlabelled findings", st.warnings)
	}
}
