package acp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/testenv"
)

var fixedNow = time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

const (
	headA   = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	headB   = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	sigCrit = "c111111111111111111111111111111111111111"
	sigImp  = "b222222222222222222222222222222222222222"
	sigWith = "f666666666666666666666666666666666666666"
)

func testConfig(root string) Config {
	return Config{
		Version: "1.2.3",
		Root:    root,
		Now:     func() time.Time { return fixedNow },
		Stderr:  &bytes.Buffer{},
	}
}

func writeFile(t *testing.T, base, rel, content string) {
	t.Helper()
	full := filepath.Join(base, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(testenv.MaybeFindingsReview(rel, content)), 0o644); err != nil {
		t.Fatal(err)
	}
}

func finding(sig, status, severity, rigor string) string {
	return fmt.Sprintf(`{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,`+
		`"anchored":true,"body":"Usable body here with enough substance.","suggestion":null,`+
		`"inBounds":true,"rigor":%q,"severity":%q,"sig":%q,"sig_version":"1",`+
		`"firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":%q,"rationale":"","history":[]}`,
		rigor, severity, sig, status)
}

func briefFinding(sig, status, severity string) string {
	return `{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,` +
		`"anchored":true,"inBounds":true,"rigor":"walked",` +
		`"body":"Usable body here with enough substance.","suggestion":null,` +
		`"severity":` + jsonStr(severity) + `,"sig":` + jsonStr(sig) + `,` +
		`"status":` + jsonStr(status) + `,"rationale":"","history":[],` +
		`"brief":{"sites":[{"path":"notes.txt","line":2}],` +
		`"outcome":"Remove the stale read before the next release.",` +
		`"approach":"Delete the dead branch and add a regression test.",` +
		`"questions":["Confirm the cache window with the owner."]}}`
}

func withheldFinding(sig string) string {
	return `{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,` +
		`"body":"Usable body here with enough substance.","suggestion":null,` +
		`"severity":"critical","sig":` + jsonStr(sig) + `,"status":"open","rationale":"",` +
		`"history":[],"withheld":{"source":"agent","reason":"operator suppressed","at":"2026-11-02T12:00:00Z"},` +
		`"brief":{"sites":[{"path":"notes.txt","line":2}],` +
		`"outcome":"Remove the stale read before the next release.",` +
		`"approach":"Delete the dead branch and add a regression test.",` +
		`"questions":["Confirm the cache window with the owner."]}}`
}

func jsonStr(s string) string {
	return fmt.Sprintf("%q", s)
}

const fullDiff = "diff --git a/notes.txt b/notes.txt\n" +
	"index 1111111..2222222 100644\n--- a/notes.txt\n+++ b/notes.txt\n" +
	"@@ -1,3 +1,3 @@\n top\n-old line\n+new line\n bottom\n"

func fixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, ".verdict/state/1234/PR.json",
		`{"state":"OPEN","headRefOid":`+jsonStr(headA)+`,"baseRefOid":`+jsonStr(headB)+`,`+
			`"author":{"login":"teammate"},"url":"https://github.com/acme/app/pull/1234"}`)
	writeFile(t, dir, ".verdict/state/1234/full.diff", fullDiff)
	writeFile(t, dir, ".verdict/state/1234/findings.json", "["+strings.Join([]string{
		briefFinding(sigCrit, "open", "critical"),
		finding(sigImp, "open", "important", "pointed"),
		withheldFinding(sigWith),
	}, ",")+"]")
	writeFile(t, dir, ".verdict/state/local-1/source.json",
		`{"schema_version":"1","kind":"range","repo":"acme/app","slug":"work",`+
			`"head_ref":"v2","base_sha":`+jsonStr(headB)+`,"head_sha":`+jsonStr(headB)+`,"dirty":false}`)
	writeFile(t, dir, ".verdict/state/local-1/full.diff", fullDiff)
	writeFile(t, dir, ".verdict/state/local-1/findings.json", "["+finding(sigImp, "open", "critical", "said")+"]")
	return dir
}

type frame struct { //nolint:govet // field order mirrors the decoded wire frame
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

func serveFrames(t *testing.T, cfg *Config, lines ...string) map[string]frame {
	t.Helper()
	var out bytes.Buffer
	input := strings.NewReader(strings.Join(lines, "\n") + "\n")
	if err := New(cfg).Serve(context.Background(), input, &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	return decodeFrames(t, out.Bytes())
}

//nolint:revive // frame decoding asserts one line of protocol output
func decodeFrames(t *testing.T, raw []byte) map[string]frame {
	t.Helper()
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return map[string]frame{}
	}
	out := map[string]frame{}
	for i, line := range strings.Split(text, "\n") {
		if !json.Valid([]byte(line)) {
			t.Fatalf("stdout line %d is not JSON: %q", i+1, line)
		}
		var f frame
		if err := json.Unmarshal([]byte(line), &f); err != nil {
			t.Fatalf("stdout line %d: %v", i+1, err)
		}
		if f.JSONRPC != jsonRPCVersion {
			t.Errorf("line %d jsonrpc = %q", i+1, f.JSONRPC)
		}
		out[string(f.ID)] = f
	}
	return out
}

func resultMap(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var out map[string]any
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	if err := dec.Decode(&out); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	return out
}

func asMap(t *testing.T, value any) map[string]any {
	t.Helper()
	out, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("value %v is not an object", value)
	}
	return out
}

func asList(t *testing.T, value any) []any {
	t.Helper()
	out, ok := value.([]any)
	if !ok {
		t.Fatalf("value %v is not an array", value)
	}
	return out
}

func errorCode(t *testing.T, f frame) string {
	t.Helper()
	if f.Error == nil {
		t.Fatalf("frame has no error: result=%s", f.Result)
	}
	data := asMap(t, f.Error.Data)
	code, ok := data["code"].(string)
	if !ok {
		t.Fatalf("error data code = %v, want a string", data["code"])
	}
	return code
}
