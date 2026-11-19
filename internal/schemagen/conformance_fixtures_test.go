package schemagen

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	qualitycore "github.com/stainless-code/verdict/internal/core/quality"
	"github.com/stainless-code/verdict/internal/jq"
)

// TestRuntimeFixturesEnvelopeValidates proves the fixtures report's data
// validates against the published fixtures profile, and that report.json is
// that same object: one shape, one schema.
func TestRuntimeFixturesEnvelopeValidates(t *testing.T) {
	doc := loadSchemaDocument(t, "envelope.schema.json")
	artifact := loadSchemaDocument(t, "fixtures.schema.json")
	root := t.TempDir()
	source := filepath.Join("..", "..", "bench", "quality")
	if err := os.MkdirAll(filepath.Join(root, "bench", "quality"), 0o755); err != nil {
		t.Fatal(err)
	}
	copyQualityTree(t, source, root)
	resp, ref := qualitycore.RunFixtures(context.Background(), &qualitycore.Request{RepoRoot: root})
	if ref != nil {
		t.Fatalf("fixtures refusal: %s: %s", ref.Code, ref.Message)
	}
	data := resp.Data.Wire()
	assertWireFields(t, data, map[string]any{
		"schema_version": "1",
		"mode":           "fixtures",
		"recordings":     json.Number("8"),
		"scored":         json.Number("8"),
	})
	calibration := wireSubMap(t, data, "calibration")
	assertWireFields(t, calibration, map[string]any{
		"rows": json.Number("8"), "reproduced": true,
	})
	assertEnvelope(t, doc, "backtest", "", resp.At, resp.Next, resp.Warnings, data)
	validateWire(t, artifact, "report.json", artifact, decodeWire(t, data))
	raw, err := os.ReadFile(filepath.Join(root, ".verdict", "backtest", "fixtures", "report.json"))
	if err != nil {
		t.Fatalf("read report.json: %v", err)
	}
	if string(raw) != jq.PrettyOrdered(resp.Data.Wire()) {
		t.Errorf("report.json differs from the envelope data:\n%s\n---\n%s", raw, jq.PrettyOrdered(resp.Data.Wire()))
	}
}

func copyQualityTree(t *testing.T, source, root string) {
	t.Helper()
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(root, "bench", "quality", rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyOneFile(path, target)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func copyOneFile(source, target string) error {
	raw, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return os.WriteFile(target, raw, 0o600)
}
