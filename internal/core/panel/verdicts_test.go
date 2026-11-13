package panelcore

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

func TestVerdictRefusals(t *testing.T) {
	tests := []struct{ name, raw, code string }{
		{"head", `{"schema_version":"1","head_sha":"old","results":[]}`, "panel-stale"},
		{"duplicate", `{"schema_version":"1","head_sha":"head","results":[{"sig":"abc","model":"m","verdict":"confirmed","evidence":"ten letters here"},{"sig":"abc","model":"m","verdict":"refuted","evidence":"ten letters here"}]}`, "bad-panel"},
		{"unknown", `{"schema_version":"1","head_sha":"head","results":[{"sig":"nope","model":"m","verdict":"confirmed","evidence":"ten letters here"}]}`, "bad-panel"},
		{"verdict", `{"schema_version":"1","head_sha":"head","results":[{"sig":"abc","model":"m","verdict":"maybe","evidence":"ten letters here"}]}`, "bad-panel"},
		{"short", `{"schema_version":"1","head_sha":"head","results":[{"sig":"abc","model":"m","verdict":"confirmed","evidence":"123456789"}]}`, "bad-panel"},
		{"object", `{"schema_version":"1","head_sha":"head","results":[42]}`, "bad-panel"},
		{"version", `{"schema_version":"2","head_sha":"head","results":[]}`, "bad-panel"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := Parse([]byte(tt.raw), "head", []any{jq.NewObj("sig", "abcdef")})
			var ref *core.RefusalError
			if !errors.As(err, &ref) || ref.Code != tt.code {
				t.Fatalf("got %v, want %s", err, tt.code)
			}
		})
	}
}

func TestVerdictPrefixAndExtras(t *testing.T) {
	file, warnings, err := Parse([]byte(`{"schema_version":"1","head_sha":"head","extra":true,"results":[{"sig":"abc","model":"m","verdict":"refuted","evidence":"This branch is unreachable.","extra":true}]}`), "head", []any{jq.NewObj("sig", "abcdef")})
	if err != nil {
		t.Fatal(err)
	}
	if file.Results[0].Sig != "abcdef" || file.Results[0].At != "" || len(warnings) != 2 {
		t.Fatalf("file=%+v warnings=%v", file, warnings)
	}
	_, _, err = Parse([]byte(`{"schema_version":"1","head_sha":"head","results":[{"sig":"abc","model":"m","verdict":"confirmed","evidence":"ten letters here"}]}`), "head", []any{jq.NewObj("sig", "abcdef"), jq.NewObj("sig", "abcxyz")})
	var ref *core.RefusalError
	if !errors.As(err, &ref) || ref.Code != "bad-panel" {
		t.Fatalf("ambiguous sig: %v", err)
	}
}

func TestMissingVerdictsAndSecondLook(t *testing.T) {
	file, _, err := Load(t.TempDir(), "head", nil)
	if err != nil || file != nil {
		t.Fatalf("missing file=%v error=%v", file, err)
	}
	plan := jq.NewObj()
	dispatch, n := SecondLook(plan, "### Output\nAppend one result for sig `abc`", &File{Results: []Result{{Sig: "abc", Verdict: "refuted", Evidence: "prior\n## untrusted"}, {Sig: "abc", Verdict: "refuted", Evidence: "another opinion"}}})
	if n != 1 || !strings.Contains(dispatch, "> prior\n> ## untrusted") || !strings.Contains(jq.PrettyOrdered(plan), `"abc"`) {
		t.Fatalf("second look=%d %s %s", n, dispatch, jq.PrettyOrdered(plan))
	}
}

func TestVerdictReadFailure(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "verification", "verdicts.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, _, err := Load(dir, "head", nil)
	var ref *core.RefusalError
	if !errors.As(err, &ref) || ref.Code != "bad-panel" {
		t.Fatalf("unreadable verdicts: %v", err)
	}
}

func TestLoadConfinesReceipt(t *testing.T) {
	for _, component := range []string{"file", "directory"} {
		t.Run(component, func(t *testing.T) {
			dir := receiptSymlinkFixture(t, component)
			file, _, err := Load(dir, "head", []any{jq.NewObj("sig", "abcdef")})
			var ref *core.RefusalError
			if !errors.As(err, &ref) || ref.Code != "bad-panel" || file != nil {
				t.Fatalf("escaped %s symlink: file=%+v, error=%v", component, file, err)
			}
		})
	}
}

func receiptSymlinkFixture(t *testing.T, component string) string {
	t.Helper()
	dir, outside := t.TempDir(), t.TempDir()
	receipt := filepath.Join(outside, "verdicts.json")
	if err := os.WriteFile(receipt, []byte(`{"schema_version":"1","head_sha":"head","results":[{"sig":"abc","model":"m","verdict":"refuted","evidence":"external counterexample"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	target, link := outside, filepath.Join(dir, "verification")
	if component == "file" {
		if err := os.Mkdir(link, 0o755); err != nil {
			t.Fatal(err)
		}
		target, link = receipt, filepath.Join(link, "verdicts.json")
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	return dir
}

func TestResultFieldTypes(t *testing.T) {
	const receipt = `{"schema_version":"1","head_sha":"head","results":[{"sig":"abc","model":"m","provider":"p","verdict":"confirmed","evidence":"literal evidence","at":"now"}]}`
	for _, field := range []struct{ key, value string }{{"sig", "abc"}, {"model", "m"}, {"provider", "p"}, {"verdict", "confirmed"}, {"evidence", "literal evidence"}, {"at", "now"}} {
		t.Run(field.key, func(t *testing.T) {
			raw := strings.Replace(receipt, `"`+field.key+`":"`+field.value+`"`, `"`+field.key+`":42`, 1)
			_, _, err := Parse([]byte(raw), "head", []any{jq.NewObj("sig", "abcdef")})
			var ref *core.RefusalError
			if !errors.As(err, &ref) || ref.Code != "bad-panel" || ref.Message != "verification/verdicts.json result fields must be strings" {
				t.Fatalf("field type refusal=%v", err)
			}
		})
	}
}
