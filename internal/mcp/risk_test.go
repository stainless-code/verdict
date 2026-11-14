package mcp

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core/risk"
)

// riskQueueFixture seeds the queue's three shapes: an open PR with one
// critical finding, a local review with a clean store, and a merged PR the
// default filter skips.
func riskQueueFixture(t *testing.T) {
	t.Helper()
	t.Chdir(t.TempDir())
	pr := func(state string) string {
		return `{"state":"` + state + `","headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",` +
			`"baseRefOid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`
	}
	writeFile(t, ".verdict/state/42/PR.json", pr("OPEN"))
	writeFile(t, ".verdict/state/42/findings.json", `[{"sig":"c1","kind":"inline","path":"notes.txt",`+
		`"line":2,"side":"RIGHT","anchored":true,"body":"The changed line drops the saved value.",`+
		`"status":"open","severity":"critical","category":"security"}]`)
	writeFile(t, ".verdict/state/local-wip/source.json",
		`{"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","base_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`)
	writeFile(t, ".verdict/state/local-wip/findings.json", `[]`)
	writeFile(t, ".verdict/state/51/PR.json", pr("MERGED"))
	writeFile(t, ".verdict/state/51/findings.json", `[]`)
}

// TestRiskToolSchema proves the published tool takes the queue's two filters
// and no bundle: repo is required, status and limit are optional and bounded.
func TestRiskToolSchema(t *testing.T) {
	t.Parallel()
	schema := inputSchema(findTool(t, "risk"))
	props, ok := schema[schemaProperties].(map[string]any)
	if !ok {
		t.Fatal("risk schema has no properties")
	}
	if got, want := sortedKeys(props), []string{"limit", "repo", "status"}; !slices.Equal(got, want) {
		t.Errorf("risk schema arguments = %v, want %v", got, want)
	}
	if _, ok := props["pr"]; ok {
		t.Error("risk schema advertises a pr argument; the tool ranks the queue")
	}
	limit, ok := props["limit"].(map[string]any)
	if !ok || limit[schemaMinimum] != 1 || limit[schemaMaximum] != 200 {
		t.Errorf("limit = %v, want the 1..200 bound", props["limit"])
	}
	if required, ok := schema[schemaRequired].([]string); !ok || !slices.Equal(required, []string{"repo"}) {
		t.Errorf("required = %v, want [repo]", schema[schemaRequired])
	}
}

// riskToolData is the published data shape the tool returns, key for key.
type riskToolData struct {
	Rows       []riskToolRow `json:"rows"`
	Bundles    int           `json:"bundles"`
	NeedsHuman int           `json:"needs_human"`
	ByBand     struct {
		Critical int `json:"critical"`
		Low      int `json:"low"`
		Medium   int `json:"medium"`
		Unknown  int `json:"unknown"`
		High     int `json:"high"`
	} `json:"by_band"`
}

// riskToolRow is one decoded queue row.
type riskToolRow struct {
	Pr         *int    `json:"pr"`
	Head       *string `json:"head"`
	Bundle     string  `json:"bundle"`
	State      string  `json:"state"`
	Reported   string  `json:"reported"`
	Rank       int     `json:"rank"`
	NeedsHuman bool    `json:"needs_human"`
	Score      struct {
		Value int `json:"value"`
	} `json:"score"`
}

// TestRiskToolDataMatchesTheQueue proves the tool serves the queue's own data
// and writes nothing: the state tree is byte-identical afterwards.
func TestRiskToolDataMatchesTheQueue(t *testing.T) {
	riskQueueFixture(t)
	before := stateTree(t)
	data := callRisk(t)
	assertRiskTallies(t, data)
	if after := stateTree(t); after != before {
		t.Errorf("risk tool changed the bundle tree:\nbefore %q\nafter  %q", before, after)
	}
}

// callRisk runs one risk tool call over the fixture and decodes its data.
func callRisk(t *testing.T) *riskToolData {
	t.Helper()
	cfg := testConfig()
	frames := serve(t, &cfg,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"risk","arguments":{"repo":"acme/app"}}}`)
	var result callToolResult
	if err := json.Unmarshal(frames["1"].Result, &result); err != nil {
		t.Fatalf("risk result: %v", err)
	}
	if result.IsError {
		t.Fatalf("risk refused: %s", result.StructuredContent)
	}
	var data riskToolData
	if err := json.Unmarshal(result.StructuredContent, &data); err != nil {
		t.Fatalf("risk structuredContent: %v", err)
	}
	return &data
}

// assertRiskTallies checks the counts and the two ranked rows.
func assertRiskTallies(t *testing.T, data *riskToolData) {
	t.Helper()
	if data.Bundles != 2 || data.NeedsHuman != 2 {
		t.Errorf("bundles/needs_human = %d/%d, want 2/2 (an unassessed local review needs a human too)",
			data.Bundles, data.NeedsHuman)
	}
	if data.ByBand.Critical != 1 || data.ByBand.Medium != 1 ||
		data.ByBand.High != 0 || data.ByBand.Unknown != 0 || data.ByBand.Low != 0 {
		t.Errorf("by_band = %+v, want critical=1 medium=1 and the rest zero", data.ByBand)
	}
	if len(data.Rows) != 2 || data.Rows[0].Bundle != "42" || data.Rows[1].Bundle != "local-wip" {
		t.Fatalf("rows = %+v, want 42 then local-wip", data.Rows)
	}
	if !topRowOK(&data.Rows[0]) {
		t.Errorf("top row = %+v, want rank 1 critical at score 40 needing a human", data.Rows[0])
	}
}

// topRowOK reads the top row's published fields against the fixture.
func topRowOK(row *riskToolRow) bool {
	if row.Rank != 1 || row.Reported != risk.LevelCritical || row.State != "open" {
		return false
	}
	if row.Pr == nil || *row.Pr != 42 || row.Head == nil || *row.Head != "aaaaaaa" {
		return false
	}
	return row.Score.Value == 40 && row.NeedsHuman
}

// sortedKeys returns a property map's keys in order.
func sortedKeys(props map[string]any) []string {
	keys := make([]string, 0, len(props))
	for key := range props {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

// stateTree renders every path and byte under the state home so a write shows
// up as a difference.
func stateTree(t *testing.T) string {
	t.Helper()
	var tree strings.Builder
	root := filepath.Join(".verdict", "state")
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		tree.WriteString(path)
		if entry.IsDir() {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		tree.WriteString("=" + string(raw) + "\n")
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return tree.String()
}
