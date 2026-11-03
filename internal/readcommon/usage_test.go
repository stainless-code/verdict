package readcommon_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// writeUsageFixture writes one file under root, creating parents.
func writeUsageFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// usageRefusalCode proves a handoff failure refuses typed as bad-usage.
func usageRefusalCode(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("want a bad-usage refusal, got nil")
	}
	var ref *core.RefusalError
	if !errors.As(err, &ref) || ref.Code != "bad-usage" {
		t.Fatalf("refusal = %v, want code bad-usage", err)
	}
}

// TestReadContractUsageObject proves one handed .json object reads its
// source, model, tokens, priced total, and window.
func TestReadContractUsageObject(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "usage.json")
	writeUsageFixture(t, file, `{"schema_version":"1","source":"bench-harness","model":"sonnet",
"tokens":{"input":1000,"output":50,"cache_read":800},"usd":0.42,
"started_at":"2026-01-01T00:00:00Z","ended_at":"2026-01-01T01:00:00Z"}`)
	usage, err := readcommon.ReadContractUsage(map[string]string{"VERDICT_USAGE": file})
	if err != nil {
		t.Fatal(err)
	}
	if len(usage) != 1 {
		t.Fatalf("records = %d, want 1", len(usage))
	}
	assertContractRecord(t, &usage[0])
}

// assertContractRecord checks one decoded usage record field by field.
func assertContractRecord(t *testing.T, got *readcommon.ContractUsage) {
	t.Helper()
	assertContractIdentity(t, got)
	assertContractPriced(t, got)
	assertContractWindow(t, got)
}

// assertContractIdentity checks a decoded record's source, model, and
// tokens.
func assertContractIdentity(t *testing.T, got *readcommon.ContractUsage) {
	t.Helper()
	if got.Source != "bench-harness" || got.Model != "sonnet" {
		t.Errorf("identity = %s/%s", got.Source, got.Model)
	}
	if !got.HasTokens || got.Input != 1000 || got.Output != 50 || got.CacheRead != 800 || got.CacheWrite != 0 {
		t.Errorf("tokens = %+v, want input 1000 output 50 cache_read 800", got)
	}
}

// assertContractPriced checks a decoded record's priced total.
func assertContractPriced(t *testing.T, got *readcommon.ContractUsage) {
	t.Helper()
	if got.USD == nil || *got.USD != 0.42 {
		t.Errorf("USD = %v, want 0.42", got.USD)
	}
}

// assertContractWindow checks a decoded record's window.
func assertContractWindow(t *testing.T, got *readcommon.ContractUsage) {
	t.Helper()
	if !got.Started.Equal(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("started = %v", got.Started)
	}
	if !got.Ended.Equal(time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)) {
		t.Errorf("ended = %v", got.Ended)
	}
}

// TestReadContractUsageShapes proves the handed shapes compose: a .json
// array reads every record, a directory reads its *.json and *.jsonl
// entries in order while ignoring anything else, and an empty handed
// directory reads as no measurement.
func TestReadContractUsageShapes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeUsageShapes(t, dir)
	usage, err := readcommon.ReadContractUsage(map[string]string{"VERDICT_USAGE": dir})
	if err != nil {
		t.Fatal(err)
	}
	assertUsageShapes(t, usage)

	if usage, err := readcommon.ReadContractUsage(map[string]string{"VERDICT_USAGE": t.TempDir()}); err != nil || len(usage) != 0 {
		t.Fatalf("empty dir = %v/%v, want no measurement", usage, err)
	}
	if usage, err := readcommon.ReadContractUsage(map[string]string{}); err != nil || len(usage) != 0 {
		t.Fatalf("unset = %v/%v, want no measurement", usage, err)
	}
}

// writeUsageShapes writes the directory-shape fixture: an array file, a
// log file, and an ignored non-usage entry.
func writeUsageShapes(t *testing.T, dir string) {
	t.Helper()
	record := `{"schema_version":"1","source":"%s","started_at":"2000-01-01T00:00:00Z","ended_at":"2099-01-01T00:00:00Z"}`
	writeUsageFixture(t, filepath.Join(dir, "b.json"),
		`[`+fmt.Sprintf(record, "second")+`,`+fmt.Sprintf(record, "third")+`]`)
	writeUsageFixture(t, filepath.Join(dir, "a.jsonl"),
		fmt.Sprintf(record, "first")+"\n\n"+fmt.Sprintf(record, "fourth")+"\n")
	writeUsageFixture(t, filepath.Join(dir, "notes.txt"), "not usage\n")
}

// assertUsageShapes checks the directory shape reads every record in order
// without inventing data.
func assertUsageShapes(t *testing.T, usage []readcommon.ContractUsage) {
	t.Helper()
	if len(usage) != 4 {
		t.Fatalf("records = %d, want 4", len(usage))
	}
	for i, want := range []string{"first", "fourth", "second", "third"} {
		if usage[i].Source != want {
			t.Errorf("record %d source = %s, want %s", i, usage[i].Source, want)
		}
		if usage[i].HasTokens || usage[i].USD != nil {
			t.Errorf("record %d invents data: %+v", i, usage[i])
		}
	}
}

// TestReadContractUsageRefusals proves a broken explicit handoff refuses
// typed — naming the file — instead of silently degrading: missing paths,
// empty files, non-JSON, wrong stamps, missing sources, bad windows, and
// negative numbers.
func TestReadContractUsageRefusals(t *testing.T) {
	t.Parallel()
	bad := map[string]string{
		"empty.json":       ``,
		"garbage.json":     `not json`,
		"trailing.json":    `{"schema_version":"1"} {"schema_version":"1"}`,
		"array-bad.json":   `[{"schema_version":"1","source":"x","started_at":"2000-01-01T00:00:00Z","ended_at":"2099-01-01T00:00:00Z"},42]`,
		"stamp.json":       `{"schema_version":"2","source":"x","started_at":"2000-01-01T00:00:00Z","ended_at":"2099-01-01T00:00:00Z"}`,
		"no-source.json":   `{"schema_version":"1","started_at":"2000-01-01T00:00:00Z","ended_at":"2099-01-01T00:00:00Z"}`,
		"bad-start.json":   `{"schema_version":"1","source":"x","started_at":"yesterday","ended_at":"2099-01-01T00:00:00Z"}`,
		"backwards.json":   `{"schema_version":"1","source":"x","started_at":"2099-01-01T00:00:00Z","ended_at":"2000-01-01T00:00:00Z"}`,
		"negative.json":    `{"schema_version":"1","source":"x","usd":-1,"started_at":"2000-01-01T00:00:00Z","ended_at":"2099-01-01T00:00:00Z"}`,
		"tokens-lax.json":  `{"schema_version":"1","source":"x","tokens":{"input":1},"started_at":"2000-01-01T00:00:00Z","ended_at":"2099-01-01T00:00:00Z"}`,
		"tokens-neg.json":  `{"schema_version":"1","source":"x","tokens":{"input":1,"output":-1},"started_at":"2000-01-01T00:00:00Z","ended_at":"2099-01-01T00:00:00Z"}`,
		"empty-log.jsonl":  "\n  \n",
		"bad-line.jsonl":   "{\"schema_version\":\"1\",\"source\":\"x\",\"started_at\":\"2000-01-01T00:00:00Z\",\"ended_at\":\"2099-01-01T00:00:00Z\"}\nnope\n",
		"wrong-shape.json": `[1,2]`,
	}
	dir := t.TempDir()
	for name, content := range bad {
		writeUsageFixture(t, filepath.Join(dir, name), content)
	}
	for name := range bad {
		path := filepath.Join(dir, name)
		if _, err := readcommon.ReadContractUsage(map[string]string{"VERDICT_USAGE": path}); err == nil {
			t.Errorf("%s reads ok, want bad-usage", name)
		} else {
			usageRefusalCode(t, err)
		}
	}
	missing := filepath.Join(t.TempDir(), "absent.json")
	_, err := readcommon.ReadContractUsage(map[string]string{"VERDICT_USAGE": missing})
	usageRefusalCode(t, err)
}

// TestMatchContractUsage proves a cost record joins the usage session whose
// window brackets it, nearest end first, and that no containment reads as
// no measurement.
func TestMatchContractUsage(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	usage := []readcommon.ContractUsage{
		{Source: "outer", Started: start, Ended: start.Add(4 * time.Hour)},
		{Source: "inner", Started: start.Add(time.Hour), Ended: start.Add(2 * time.Hour)},
	}
	match := readcommon.MatchContractUsage(usage, start.Add(90*time.Minute))
	if match == nil || match.Source != "inner" {
		t.Fatalf("match = %+v, want inner", match)
	}
	if got := readcommon.MatchContractUsage(usage, start.Add(5*time.Hour)); got != nil {
		t.Fatalf("outside match = %+v, want nil", got)
	}
}
