package fetchcore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

func TestFetchLocalSpecMarksRoleAndTOC(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeContextFixture(t, "spec.md", "- Must return 401 on expired token\n- Must log request ID\n")
	writeContextFixture(t, "notes.md", "- Reference note, not a spec item\n")

	cmdr := stagedLocalCommander(t, cleanStagedDiff)
	var human, diag bytes.Buffer
	var streamed []string
	resp, err := RunLocal(t.Context(), localScriptReq(cmdr, &human, &diag, &streamed, nil,
		core.FetchLocalFlags{
			Staged:  true,
			Context: []string{"notes.md"},
			Spec:    []string{"spec.md"},
		}))
	if err != nil {
		t.Fatalf("RunLocal with --spec and --context: %v", err)
	}
	if !strings.Contains(resp.Next, "context.txt") {
		t.Fatalf("Next = %q, want context.txt", resp.Next)
	}

	bundleDir := filepath.Join(".verdict", "state", "local-mine")
	entries := readContextJSON(t, bundleDir)
	assertContextRoles(t, entries, map[string]string{
		"notes.md": "context",
		"spec.md":  "spec",
	})

	toc := readFileString(t, filepath.Join(bundleDir, "toc.txt"))
	if !strings.Contains(toc, "## context\n- notes.md\n- spec.md [spec]\n") {
		t.Fatalf("toc.txt missing marked spec row:\n%s", toc)
	}
	ctxTxt := readFileString(t, filepath.Join(bundleDir, "context.txt"))
	if !strings.Contains(ctxTxt, "spec.md\n- Must return 401 on expired token") {
		t.Fatalf("context.txt missing fenced spec body:\n%s", ctxTxt)
	}
}

func TestFetchLocalContextAloneHasNoSpecRole(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeContextFixture(t, "notes.md", "- Plain reference bullet\n")

	cmdr := stagedLocalCommander(t, cleanStagedDiff)
	var human, diag bytes.Buffer
	var streamed []string
	_, err := RunLocal(t.Context(), localScriptReq(cmdr, &human, &diag, &streamed, nil,
		core.FetchLocalFlags{Staged: true, Context: []string{"notes.md"}}))
	if err != nil {
		t.Fatalf("RunLocal with --context: %v", err)
	}

	bundleDir := filepath.Join(".verdict", "state", "local-mine")
	entries := readContextJSON(t, bundleDir)
	assertContextRoles(t, entries, map[string]string{"notes.md": "context"})
	toc := readFileString(t, filepath.Join(bundleDir, "toc.txt"))
	if strings.Contains(toc, "[spec]") {
		t.Fatalf("toc.txt unexpectedly marked [spec] for plain --context:\n%s", toc)
	}
}

func TestFetchLocalSpecCap60Passes(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeContextFixture(t, "spec60.md", numberedSpecItems(60))

	cmdr := stagedLocalCommander(t, cleanStagedDiff)
	var human, diag bytes.Buffer
	var streamed []string
	resp, err := RunLocal(t.Context(), localScriptReq(cmdr, &human, &diag, &streamed, nil,
		core.FetchLocalFlags{Staged: true, Spec: []string{"spec60.md"}}))
	if err != nil {
		t.Fatalf("RunLocal with 60 spec items failed: %v", err)
	}
	if !strings.Contains(resp.Next, "context.txt") {
		t.Fatalf("Next = %q, want context.txt for --spec-only fetch", resp.Next)
	}
	bundleDir := filepath.Join(".verdict", "state", "local-mine")
	assertContextRoles(t, readContextJSON(t, bundleDir), map[string]string{"spec60.md": "spec"})
}

func TestFetchLocalSpecCap61RefusesWithoutWrites(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeContextFixture(t, "spec-a.md", numberedSpecItems(40))
	writeContextFixture(t, "spec-b.md", numberedSpecItemsFrom(41, 21))

	cmdr := stagedLocalCommander(t, cleanStagedDiff)
	var human, diag bytes.Buffer
	var streamed []string
	_, err := RunLocal(t.Context(), localScriptReq(cmdr, &human, &diag, &streamed, nil,
		core.FetchLocalFlags{Staged: true, Spec: []string{"spec-a.md", "spec-b.md"}}))
	ref := requireRefusal(t, err, "spec-too-large")
	if !strings.Contains(ref.Message, "61") || !strings.Contains(ref.Message, "60") {
		t.Fatalf("refusal message = %q, want item count 61 and cap 60", ref.Message)
	}
	const wantHint = "try: narrow the --spec file under 60 checklist items or split the PR, then re-fetch"
	if got := core.HintFor(ref.Code); got != wantHint {
		t.Fatalf("HintFor(spec-too-large) = %q, want %q", got, wantHint)
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".verdict")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf(".verdict exists after spec-too-large refusal (err=%v): fetch must write nothing", statErr)
	}
}

func TestFetchLocalSpecValidationMatchesContext(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeContextFixture(t, "bad.go", "package bad\n")

	cmdr := stagedLocalCommander(t, cleanStagedDiff)
	var human, diag bytes.Buffer
	var streamed []string
	_, err := RunLocal(t.Context(), localScriptReq(cmdr, &human, &diag, &streamed, nil,
		core.FetchLocalFlags{Staged: true, Spec: []string{"bad.go"}}))
	ref := requireRefusal(t, err, "context-invalid")
	if !strings.Contains(ref.Message, "--spec") {
		t.Fatalf("refusal message = %q, want --spec", ref.Message)
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".verdict")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf(".verdict exists after invalid --spec refusal (err=%v)", statErr)
	}
}

func numberedSpecItems(count int) string {
	return numberedSpecItemsFrom(1, count)
}

func numberedSpecItemsFrom(start, count int) string {
	var sb strings.Builder
	for i := range count {
		fmt.Fprintf(&sb, "- Requirement number %d must hold\n", start+i)
	}
	return sb.String()
}

func readContextJSON(t *testing.T, bundleDir string) []contextEntry {
	t.Helper()
	raw := readFileString(t, filepath.Join(bundleDir, "context.json"))
	var entries []contextEntry
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		t.Fatalf("decode context.json: %v\n%s", err, raw)
	}
	return entries
}

func readFileString(t *testing.T, p string) string {
	t.Helper()
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return string(raw)
}

func assertContextRoles(t *testing.T, entries []contextEntry, want map[string]string) {
	t.Helper()
	if len(entries) != len(want) {
		t.Fatalf("context.json entries = %+v, want %d entries", entries, len(want))
	}
	for _, entry := range entries {
		wantRole, ok := want[entry.Source]
		if !ok {
			t.Fatalf("unexpected source %q in %+v", entry.Source, entries)
		}
		if entry.Role != wantRole {
			t.Fatalf("entry %q role = %q, want %q", entry.Source, entry.Role, wantRole)
		}
	}
}
