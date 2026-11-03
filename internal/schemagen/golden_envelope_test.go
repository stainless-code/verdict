package schemagen

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The parity harness scrubs real instants to this placeholder. It is not a
// valid RFC 3339 timestamp, so envelope validation substitutes a real one;
// otherwise every scrubbed golden would fail a format check that the
// unscrubbed product output passes.
const (
	scrubbedInstant = "0000-00-00T00:00:00Z"
	validInstant    = "2026-01-01T00:00:00Z"
	envelopeMarker  = "--- stdout ---"
	minEnvelopes    = 200
)

// TestCommittedEnvelopeGoldensValidate walks every *.golden in the repository
// and validates each stdout that is a schema_version envelope against
// schemas/envelope.schema.json, including the verb's pinned data definition
// when the schema carries one.
func TestCommittedEnvelopeGoldensValidate(t *testing.T) {
	doc := loadSchemaDocument(t, "envelope.schema.json")
	root, err := RepoRoot()
	if err != nil {
		t.Fatalf("RepoRoot: %v", err)
	}
	checked, err := walkEnvelopeGoldens(doc, root)
	if err != nil {
		t.Fatal(err)
	}
	if checked < minEnvelopes {
		t.Fatalf("validated %d envelope goldens; want at least %d (did the walk stop finding goldens?)",
			checked, minEnvelopes)
	}
	t.Logf("validated %d envelope goldens", checked)
}

// walkEnvelopeGoldens validates every envelope golden below root and returns
// how many it checked.
func walkEnvelopeGoldens(doc map[string]any, root string) (int, error) {
	checked := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		delta, err := visitGolden(doc, path, entry, err)
		checked += delta
		return err
	})
	return checked, err
}

// visitGolden validates one walk entry and returns 1 for a checked envelope.
func visitGolden(doc map[string]any, path string, entry fs.DirEntry, err error) (int, error) {
	if err != nil {
		return 0, err
	}
	if entry.IsDir() {
		return 0, skipGoldenDir(entry.Name())
	}
	if !strings.HasSuffix(path, ".golden") {
		return 0, nil
	}
	found, err := checkEnvelopeGolden(doc, path)
	if err != nil || !found {
		return 0, err
	}
	return 1, nil
}

// skipGoldenDir skips directories that never hold payload goldens.
func skipGoldenDir(name string) error {
	if name == ".git" || name == "node_modules" {
		return fs.SkipDir
	}
	return nil
}

// checkEnvelopeGolden reads one golden and validates its stdout envelope.
func checkEnvelopeGolden(doc map[string]any, path string) (bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	stdout, ok := goldenStdout(string(raw))
	if !ok {
		return false, nil
	}
	found, err := validateEnvelopeGolden(doc, stdout)
	if err != nil {
		return true, fmt.Errorf("%s: %w", path, err)
	}
	return found, nil
}

// validateEnvelopeGolden parses one stdout section and validates it when it is
// a schema_version envelope. found is false for non-envelope stdout.
func validateEnvelopeGolden(doc map[string]any, stdout string) (bool, error) {
	trimmed := strings.TrimSpace(stdout)
	if !strings.HasPrefix(trimmed, "{") {
		return false, nil
	}
	normalized := strings.ReplaceAll(trimmed, scrubbedInstant, validInstant)
	dec := json.NewDecoder(strings.NewReader(normalized))
	dec.UseNumber()
	var env map[string]any
	if err := dec.Decode(&env); err != nil {
		return true, fmt.Errorf("parse stdout envelope: %w", err)
	}
	if _, ok := env["schema_version"]; !ok {
		return false, nil
	}
	if err := schemaError(doc, doc, env); err != nil {
		return true, fmt.Errorf("envelope: %w", err)
	}
	if err := pinnedDataError(doc, env); err != nil {
		return true, err
	}
	if err := trailingJSONError(dec); err != nil {
		return true, err
	}
	return true, nil
}

// pinnedDataError validates data against <verb>Data when the schema pins one.
func pinnedDataError(doc map[string]any, env map[string]any) error {
	verb, ok := env["verb"].(string)
	if !ok {
		return nil
	}
	defs, ok := doc["$defs"].(map[string]any)
	if !ok {
		return nil
	}
	def, ok := defs[verb+"Data"]
	if !ok || env["data"] == nil {
		return nil
	}
	if err := schemaError(doc, def, env["data"]); err != nil {
		return fmt.Errorf("%s data: %w", verb, err)
	}
	return nil
}

// trailingJSONError rejects a second document after the envelope.
func trailingJSONError(dec *json.Decoder) error {
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return errors.New("stdout has content after the envelope")
	}
	return nil
}

// goldenStdout returns the text between the stdout marker and the next
// section marker.
func goldenStdout(content string) (string, bool) {
	lines := strings.Split(content, "\n")
	start := -1
	for i, line := range lines {
		if line == envelopeMarker {
			start = i
			break
		}
	}
	if start < 0 {
		return "", false
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "--- ") {
			end = i
			break
		}
	}
	return strings.Join(lines[start+1:end], "\n"), true
}

// TestEnvelopeGoldenWalkRejectsBrokenEnvelope proves the walk fails rather
// than silently skipping a malformed envelope.
func TestEnvelopeGoldenWalkRejectsBrokenEnvelope(t *testing.T) {
	doc := loadSchemaDocument(t, "envelope.schema.json")
	dir := t.TempDir()
	broken := envelopeMarker + "\n" +
		`{"schema_version":"1","verb":"report","repo":"acme/app","pr":"5101",` +
		`"at":"2026-11-02T12:00:00Z","next":"","warnings":[],"data":{},"error":null}` + "\n" +
		"--- stderr ---\n"
	writeFixture(t, filepath.Join(dir, "broken.golden"), broken)
	count, err := walkEnvelopeGoldens(doc, dir)
	if err == nil {
		t.Fatalf("broken envelope accepted (%d checked); want a validation error", count)
	}
	if !strings.Contains(err.Error(), "pr") {
		t.Errorf("error = %v; want it to name the offending pr field", err)
	}
}
