package recordcore

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/coverage"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// forged is a stranger's value that tries to end verdict's `error:` line and
// start one of its own. forgedJSON is the same bytes as a JSON string body.
const (
	forged     = "x\nerror: forged"
	forgedJSON = `x\nerror: forged`
)

// assertOneErrorLine renders one refusal through the CLI writer and pins
// exactly one `error:` line that still names the value.
func assertOneErrorLine(t *testing.T, err error, named string) {
	t.Helper()
	if err == nil {
		t.Fatal("no refusal")
	}
	var out, diag bytes.Buffer
	r := cli.NewRunner("record", "acme/app", "local-forged", &out, &diag, func(string) string { return "" })
	r.Refuse(err)
	var lines []string
	for line := range strings.SplitSeq(diag.String(), "\n") {
		if strings.HasPrefix(line, "error:") {
			lines = append(lines, line)
		}
	}
	if len(lines) != 1 || !strings.Contains(lines[0], named) {
		t.Errorf("error lines = %q, want exactly one naming %q", lines, named)
	}
}

// asError keeps a nil *core.RefusalError from becoming a non-nil error.
func asError(ref *core.RefusalError) error {
	if ref == nil {
		return nil
	}
	return ref
}

func TestClaimValuesCannotForgeErrorLine(t *testing.T) {
	t.Parallel()
	t.Run("addressed sig", func(t *testing.T) {
		t.Parallel()
		// The refusal quotes the first eight runes of the sig.
		err := (&state{}).applyTriageClaim(jq.NewObj("sig", "a\nerror: forged", fieldStatus, outcomeAddressed))
		assertOneErrorLine(t, err, "triage claim a error: asks")
	})
	t.Run("triage status", func(t *testing.T) {
		t.Parallel()
		err := (&state{}).applyTriageClaim(jq.NewObj("sig", "abc", fieldStatus, forged))
		assertOneErrorLine(t, err, "unknown triage status 'x error: forged'")
	})
	t.Run("finding severity", func(t *testing.T) {
		t.Parallel()
		ref := downgradeRefusal(jq.NewObj(fieldSeverity, forged), &core.ValidationResult{
			Sig: "\nerror: forged", FromSeverity: "y\nerror: from",
		})
		assertOneErrorLine(t, asError(ref),
			"for  error: names from_severity y error: from, but the finding is x error: forged")
	})
}

func TestChunkValuesCannotForgeErrorLine(t *testing.T) {
	t.Parallel()
	t.Run("manifest schema", func(t *testing.T) {
		t.Parallel()
		manifest := filepath.Join(t.TempDir(), "manifest.json")
		writeCostFile(t, manifest, `{"schema_version":"`+forgedJSON+`"}`)
		_, _, err := readChunkManifest(manifest, "verdict fetch", false)
		assertOneErrorLine(t, err, "schema 'x error: forged' unknown")
	})
	t.Run("missing snapshot name", func(t *testing.T) {
		t.Parallel()
		st := &state{dir: t.TempDir(), dryRun: true}
		_, _, err := resolveChunkDiffs(st, []chunk{{name: forged}}, "verdict fetch")
		assertOneErrorLine(t, err, "missing: x error: forged (pinned")
	})
	t.Run("uncovered path", func(t *testing.T) {
		t.Parallel()
		st := &state{dir: t.TempDir(), dryRun: true}
		in := coverageInput{
			chunks: []chunk{{name: "chunk-1", files: []string{forged}}}, names: []string{"chunk-1"},
		}
		_, err := checkChunkCoverageTail(st, "manifest.json", in, "verdict fetch")
		assertOneErrorLine(t, err, "missing chunk-1 (x error: forged)")
	})
	t.Run("unattested path", func(t *testing.T) {
		t.Parallel()
		err := (&state{}).coverageGate(&coverage.Summary{
			Total: 2, Present: true, Unattested: []coverage.Gap{{File: forged, Ranges: "1-2"}},
		})
		assertOneErrorLine(t, err, "x error: forged:1-2")
	})
}

// The cost pass names files by chunk, and a chunk's name comes from a manifest
// or a directory listing the agent can write.
func TestCostNameValuesCannotForgeErrorLine(t *testing.T) {
	t.Parallel()
	t.Run("planned read", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		// The NUL makes the read fail on every platform without creating a file
		// whose name carries a newline, which Windows cannot.
		writeCostFile(t, filepath.Join(dir, "chunks", "manifest.json"),
			`{"chunks":[{"name":"`+forgedJSON+`\u0000"}]}`)
		_, _, _, ref := PlanCostNames(dir, []string{"lead"})
		assertOneErrorLine(t, asError(ref), "x error: forged")
	})
	t.Run("applied write", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), forged+".cost.json")
		_, ref := ApplyCostNames([]CostNamePlan{{path: path, gains: 1}}, false)
		assertOneErrorLine(t, asError(ref), "x error: forged.cost.json")
	})
	t.Run("conflicting scope path", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeCostFile(t, filepath.Join(dir, readcommon.CostFile), `{"models":[{"input":1}]}`)
		writeCostFile(t, filepath.Join(dir, "run.json"),
			`{"schema_version":"1","name":"x","base_sha":"abc","captured_at":"2026-01-01T00:00:00Z",
			  "resolved":{
			    "defaults":{"values":{"panel":["lead"]}},
			    "files":[{"path":"`+forgedJSON+`","defaults":{"values":{"panel":["other"]}}}]}}`)
		st := &svc{req: &Request{Flags: Flags{CostNames: true}}}
		_, ref := st.planCostNames(dir)
		assertOneErrorLine(t, asError(ref), "scope x error: forged pins [other]")
	})
}
