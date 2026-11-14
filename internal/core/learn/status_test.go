package learncore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

type statusProgress struct {
	human []string
	warns []string
}

func (p *statusProgress) Human(line string) { p.human = append(p.human, line) }
func (p *statusProgress) Warn(msg string)   { p.warns = append(p.warns, msg) }
func (p *statusProgress) Diag(string)       {}

type seedBundle struct {
	name     string
	findings string
	recall   string
	hasRec   bool
}

type statusExposureCase struct {
	wantJSON map[string]string
	wantText map[string]string
	name     string
	kbLines  []string
	bundles  []seedBundle
}

func writeBundleFixture(t *testing.T, root string, b seedBundle) {
	t.Helper()
	dir := filepath.Join(root, ".verdict", "state", b.name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "findings.json"), []byte(b.findings), 0o600); err != nil {
		t.Fatal(err)
	}
	if !b.hasRec {
		return
	}
	if err := os.WriteFile(filepath.Join(dir, "kb-recall.json"), []byte(b.recall), 0o600); err != nil {
		t.Fatal(err)
	}
}

func seedStatusRepo(t *testing.T, kbLines []string, bundles []seedBundle) string {
	t.Helper()
	root := t.TempDir()
	kbPath := filepath.Join(root, ".verdict", "kb.jsonl")
	if err := os.MkdirAll(filepath.Dir(kbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(kbPath, []byte(strings.Join(kbLines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, b := range bundles {
		writeBundleFixture(t, root, b)
	}
	return root
}

func checkExposureCase(t *testing.T, tc *statusExposureCase) {
	t.Helper()
	root := seedStatusRepo(t, tc.kbLines, tc.bundles)
	checkExposureJSON(t, root, tc.wantJSON)
	checkExposureText(t, root, tc.wantText)
}

func checkExposureJSON(t *testing.T, root string, want map[string]string) {
	t.Helper()
	jsonReq := &Request{Status: true, JSONMode: true}
	jsonReq.ReadRequest = core.ReadRequest{RepoRoot: root}
	resp, ref := Run(t.Context(), jsonReq)
	if ref != nil {
		t.Fatalf("JSON run failed: %v", ref)
	}
	byID := make(map[string]string, len(resp.Data.Rules))
	for _, raw := range resp.Data.Rules {
		obj, ok := raw.(*jq.Obj)
		if !ok {
			t.Fatalf("rule row type = %T, want *jq.Obj", raw)
		}
		byID[idOf(obj)] = obj.MarshalCompact()
	}
	for id, wantSub := range want {
		if got := byID[id]; !strings.Contains(got, wantSub) {
			t.Errorf("JSON row[%s] = %s; want substring %s", id, got, wantSub)
		}
	}
}

func checkExposureText(t *testing.T, root string, want map[string]string) {
	t.Helper()
	prog := &statusProgress{}
	textReq := &Request{Status: true}
	textReq.ReadRequest = core.ReadRequest{
		Runtime:  &core.Runtime{Progress: prog},
		RepoRoot: root,
	}
	if _, ref := Run(t.Context(), textReq); ref != nil {
		t.Fatalf("text run failed: %v", ref)
	}
	joined := strings.Join(prog.human, "\n")
	for id, wantLine := range want {
		if !strings.Contains(joined, wantLine) {
			t.Errorf("text output missing %s line:\ngot:\n%s\nwant line:\n%s", id, joined, wantLine)
		}
	}
}

func TestLearnStatusRuleExposure(t *testing.T) {
	t.Parallel()
	const kbRowTemplate = `{"id":"%s","rule":"Rule %s","scope":"*.go","provenance":"manual","rigor":"walked","hits":0,"false_hits":0,"judged":0,"status":"active"}`
	rowLine := func(id string) string {
		return strings.ReplaceAll(kbRowTemplate, "%s", id)
	}

	cases := []statusExposureCase{
		{
			name: "never_cited_and_cited_once_addressed",
			kbLines: []string{
				rowLine("r-never"),
				rowLine("r-once"),
			},
			bundles: []seedBundle{
				{
					name:     "pr-1",
					findings: `[{"sig":"s1","status":"addressed","kb":["r-once"]}]`,
					hasRec:   true,
					recall:   `[{"id":"r-never"},{"id":"r-once"}]`,
				},
				{
					name:     "pr-2",
					findings: `[]`,
					hasRec:   true,
					recall:   `[{"id":"r-never"},{"id":"r-once"}]`,
				},
			},
			wantJSON: map[string]string{
				"r-never": `"recalled_in":2,"cited_in":0,"exposure":0,"acceptance":null`,
				"r-once":  `"recalled_in":2,"cited_in":1,"exposure":0.5,"acceptance":1`,
			},
			wantText: map[string]string{
				"r-never": "r-never [active] walked hits=0 false_hits=0 judged=0 precision=n/a recalled_in=2 cited_in=0 exposure=0 acceptance=n/a",
				"r-once":  "r-once [active] walked hits=0 false_hits=0 judged=0 precision=n/a recalled_in=2 cited_in=1 exposure=0.5 acceptance=1",
			},
		},
		{
			name:    "zero_recalled_in_measured_repo_keeps_null_exposure",
			kbLines: []string{rowLine("r-unrecalled")},
			bundles: []seedBundle{
				{
					name:     "pr-1",
					findings: `[{"sig":"s1","status":"addressed","kb":["r-unrecalled","r-unrecalled"]},{"sig":"s2","status":"open","kb":["r-unrecalled"]}]`,
					hasRec:   true,
					recall:   `[]`,
				},
			},
			wantJSON: map[string]string{
				"r-unrecalled": `"recalled_in":0,"cited_in":1,"exposure":null,"acceptance":0.5`,
			},
			wantText: map[string]string{
				"r-unrecalled": "r-unrecalled [active] walked hits=0 false_hits=0 judged=0 precision=n/a recalled_in=0 cited_in=1 exposure=n/a acceptance=0.5",
			},
		},
		{
			name: "missing_manifests_read_unmeasured",
			kbLines: []string{
				rowLine("r-legacy"),
				rowLine("r-idle"),
			},
			bundles: []seedBundle{
				{
					name:     "pr-1",
					findings: `[{"sig":"s1","status":"addressed","kb":["r-legacy"]}]`,
					hasRec:   false,
				},
			},
			wantJSON: map[string]string{
				"r-legacy": `"recalled_in":null,"cited_in":1,"exposure":null,"acceptance":1`,
				"r-idle":   `"recalled_in":null,"cited_in":0,"exposure":null,"acceptance":null`,
			},
			wantText: map[string]string{
				"r-legacy": "r-legacy [active] walked hits=0 false_hits=0 judged=0 precision=n/a recalled_in=unmeasured cited_in=1 exposure=unmeasured acceptance=1",
				"r-idle":   "r-idle [active] walked hits=0 false_hits=0 judged=0 precision=n/a recalled_in=unmeasured cited_in=0 exposure=unmeasured acceptance=n/a",
			},
		},
		{
			name:    "duplicate_manifest_entry_counts_one_bundle",
			kbLines: []string{rowLine("r-dup")},
			bundles: []seedBundle{
				{
					name:     "pr-1",
					findings: `[{"sig":"s1","status":"open","kb":["r-dup","r-dup"]}]`,
					hasRec:   true,
					recall:   `[{"id":"r-dup"},{"id":"r-dup"}]`,
				},
			},
			wantJSON: map[string]string{
				"r-dup": `"recalled_in":1,"cited_in":1,"exposure":1,"acceptance":0`,
			},
			wantText: map[string]string{
				"r-dup": "r-dup [active] walked hits=0 false_hits=0 judged=0 precision=n/a recalled_in=1 cited_in=1 exposure=1 acceptance=0",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkExposureCase(t, &tc)
		})
	}
}
