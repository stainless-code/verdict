package record_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// briefSlug is the local bundle the brief merge tests record into.
const briefSlug = "local-bm"

// Stored brief fragments: one with a site, one outcome-only.
const (
	briefFirst = `,"brief":{"sites":[{"path":"notes.txt","line":3}],` +
		`"outcome":"Reject duplicate deliveries before anything is enqueued."}`
	briefSecond = `,"brief":{"outcome":"Drop the duplicate instead of merging it into the queue."}`
	outcomeOne  = "Reject duplicate deliveries before anything is enqueued."
	outcomeTwo  = "Drop the duplicate instead of merging it into the queue."
)

// briefBundle writes a local bundle whose one inline comment carries the given
// brief fragment (empty means no brief key at all).
func briefBundle(t *testing.T, dir, slug, brief string) {
	t.Helper()
	review := `{"schema_version":"1","body":"> ✅ Brief merge check with enough length to pass.","approved":false,` +
		`"comments":[{"path":"notes.txt","line":3,"side":"RIGHT",` +
		`"body":"Brief-carrying finding.","rigor":"walked","severity":"important"` + brief + `}]}`
	writeFiles(t, dir, map[string]string{
		".review/" + slug + "/source.json": `{"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",` +
			`"base_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","head_ref":"work"}`,
		".review/" + slug + "/full.diff":   genDiff(2),
		".review/" + slug + "/review.json": review,
	})
}

// recordBrief records one pass over the bundle and fails on a refusal.
func recordBrief(t *testing.T, dir, brief string) {
	t.Helper()
	briefBundle(t, dir, briefSlug, brief)
	if out := runOne(t, dir, t.TempDir(), "record", "acme/app", briefSlug, "--json"); out.Status != 0 {
		t.Fatalf("record failed: %s", out.Stderr)
	}
}

// storedEntry reads the bundle's one stored finding.
func storedEntry(t *testing.T, dir, slug string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, ".review", slug, "findings.json"))
	if err != nil {
		t.Fatalf("read findings: %v", err)
	}
	var entries []map[string]any
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("decode findings: %v\n%s", err, raw)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	return entries[0]
}

// storedBrief reads the stored finding's brief, nil when the key is absent.
func storedBrief(t *testing.T, entry map[string]any) map[string]any {
	t.Helper()
	brief, ok := entry["brief"].(map[string]any)
	if !ok {
		return nil
	}
	return brief
}

// briefOutcome reads a stored brief's outcome.
func briefOutcome(t *testing.T, brief map[string]any) string {
	t.Helper()
	outcome, ok := brief["outcome"].(string)
	if !ok {
		t.Fatalf("stored brief has no outcome: %v", brief)
	}
	return outcome
}

// TestBriefPreservedWithoutFresh proves a re-record whose comment carries no
// brief keeps the stored one, under the same sig (one entry, not two).
func TestBriefPreservedWithoutFresh(t *testing.T) {
	dir := t.TempDir()
	recordBrief(t, dir, briefFirst)
	recordBrief(t, dir, "")
	brief := storedBrief(t, storedEntry(t, dir, briefSlug))
	if brief == nil {
		t.Fatal("brief dropped by a comment without one")
	}
	if got := briefOutcome(t, brief); got != outcomeOne {
		t.Errorf("preserved outcome = %q", got)
	}
}

// TestBriefReplacedByFresh proves a fresh brief replaces the stored one.
func TestBriefReplacedByFresh(t *testing.T) {
	dir := t.TempDir()
	recordBrief(t, dir, briefFirst)
	recordBrief(t, dir, briefSecond)
	brief := storedBrief(t, storedEntry(t, dir, briefSlug))
	if brief == nil {
		t.Fatal("fresh brief not stored")
	}
	if got := briefOutcome(t, brief); got != outcomeTwo {
		t.Errorf("replaced outcome = %q", got)
	}
	if sites, ok := brief["sites"].([]any); !ok || len(sites) != 0 {
		t.Errorf("replaced brief kept stale sites: %v", brief["sites"])
	}
}
