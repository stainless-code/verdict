package reportcore

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
)

func TestReportCountsBySourceAndGateIncludesToolFindings(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), ".verdict", "state", "77")
	writeSourceFixture(t, dir)
	rt := &core.Runtime{Now: func() time.Time { return time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC) }}
	repoRoot := filepath.Dir(filepath.Dir(filepath.Dir(dir)))
	base := core.ReadRequest{Runtime: rt, RepoRoot: repoRoot, Repo: "o/r", Pr: "77"}
	resp, ref := Run(context.Background(), &core.ReportRequest{
		ReadRequest: base,
		Flags:       core.ReportFlags{Check: "important", Coverage: "warn"},
		Effective:   core.Defaults(),
	})
	if ref != nil {
		t.Fatalf("unexpected refusal: %+v", ref)
	}
	if resp.Data.Counts.BySource["model"] != 1 || resp.Data.Counts.BySource["tool"] != 1 {
		t.Fatalf("by_source = %v, want model=1 tool=1", resp.Data.Counts.BySource)
	}
	if !strings.Contains(strings.Join(resp.Human, "\n"), "by source: model 1, tool 1") {
		t.Fatalf("expected human by source line in %v", resp.Human)
	}
	if !resp.GateFailed || resp.Data.Gate == nil || resp.Data.Gate.Passed {
		t.Fatalf("expected report --check important to fail on tool finding: gateFailed=%v gate=%+v", resp.GateFailed, resp.Data.Gate)
	}
}

func writeSourceFixture(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "PR.json"), []byte(`{"headRefOid":"abc1234"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	findings := `[
  {
    "sig": "sigmodel1",
    "sig_version": "1",
    "kind": "inline",
    "path": "a.go",
    "line": 10,
    "side": "RIGHT",
    "anchored": true,
    "body": "model bug",
    "rigor": "walked",
    "rigor_source": "model",
    "severity": "informational",
    "status": "open",
    "firstSeen": "abc1234",
    "lastSeen": "abc1234",
    "history": [{"at": "2026-04-15T10:00:00Z", "event": "reported"}]
  },
  {
    "sig": "sigtool1",
    "sig_version": "1",
    "kind": "inline",
    "path": "a.go",
    "line": 12,
    "side": "RIGHT",
    "anchored": true,
    "body": "semgrep security issue",
    "rigor": "said",
    "rigor_source": "default",
    "severity": "important",
    "status": "open",
    "firstSeen": "abc1234",
    "lastSeen": "abc1234",
    "source": "tool",
    "tool": "semgrep",
    "rule_id": "go.sec.1",
    "fingerprint": "fp-1",
    "history": [{"at": "2026-04-15T10:00:00Z", "event": "reported"}]
  }
]
`
	if err := os.WriteFile(filepath.Join(dir, "findings.json"), []byte(findings), 0o644); err != nil {
		t.Fatal(err)
	}
}
