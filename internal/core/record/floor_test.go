package recordcore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/controls"
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

func TestFloorFiltersBeforeSplitHook(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/full.diff", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	body := "### Low\nprivate finding\n```text\n## example heading\nPRIVATE_AFTER_FENCE\n```\n### High\npublic finding\n"
	review := jq.NewObj("schema_version", "1", "body", body, "comments", []any{}, "sections", []any{jq.NewObj("heading", "### Low", "severity", "informational"), jq.NewObj("heading", "### High", "severity", "critical")})
	captured := ""
	st := &state{dir: dir, review: review, cmd: "post", minSeverity: "important", controls: &controls.Snapshot{}, now: "2026-11-13T12:00:00Z", preFooter: func(body string) string { captured = body; return body }}
	if _, _, err := st.validateAndMerge(body); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(captured, "PRIVATE_AFTER_FENCE") || strings.Contains(captured, "private finding") || !strings.Contains(captured, "public finding") {
		t.Fatalf("split hook received %q", captured)
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
