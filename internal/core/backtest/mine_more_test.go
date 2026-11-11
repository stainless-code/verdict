package backtest

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
)

// recorder captures the human and warning lines a service emits.
type recorder struct {
	lines    []string
	warnings []string
}

// Human records one line.
func (r *recorder) Human(line string) { r.lines = append(r.lines, line) }

// Warn records one warning.
func (r *recorder) Warn(line string) { r.warnings = append(r.warnings, line) }

// Diag is a no-op for the recorder.
func (*recorder) Diag(string) {}

// TestMineExclusions proves every whole-fix exclusion is counted, not refused.
func TestMineExclusions(t *testing.T) {
	tests := []struct { //nolint:govet // test table readability
		name   string
		want   string
		detail string
		fix    string
		build  func(*testing.T) (*repo, string)
	}{
		{"vendor", "vendor", "vendor/lib/x.go:3", "2dc62ead5dbb0e1ff77ac91dd91163dec6bc3523", vendorFixture},
		{"node_modules", "node_modules", "web/node_modules/pkg/index.js:1", "3cbe2c7068d516c44b1cb1523ac103917db67997", nodeModulesFixture},
		{"third_party", "third_party", "third_party/z/base.c:1", "f25d3d0d231c039082d2a42ed1eb16658f6ee05a", thirdPartyFixture},
		{"generated", "generated", "api.pb.go:3", "8151bf37fd9ee19d8795f1741b966280b168ebab", generatedFixture},
		{"wide", "wide-fix", "", "760bbd98efadf0d6b83e9b49d487a6057d4f0bcb", wideFixture},
		{"docs", "docs-test-only", "", "b7677beb5224368c15e4451e4d03111ada8aa1fb", docsFixture},
		{"added-only", "added-only", "", "bd36d1e1be4acafc3b439acc37ba1e3f8ef95c59", addedOnlyFixture},
		{"binary", "binary", "", "9462456c220ab3129f1bceeaa1057528f2a6de05", binaryFixture},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			checkExclusion(t, tc.build, tc.want, tc.detail, tc.fix)
		})
	}
}

// checkExclusion proves one fixture yields exactly its skip row: the reason,
// the position of a line-level exclusion, and the fix.
func checkExclusion(t *testing.T, build func(*testing.T) (*repo, string), wantReason, wantDetail, wantFix string) {
	t.Helper()
	r, base := build(t)
	fix := r.commit("fix: change it")
	data := runMine(t, r, base)
	if len(data.Mine.Candidates) != 0 {
		t.Fatalf("candidates = %+v; want none", data.Mine.Candidates)
	}
	want := Skipped{Reason: wantReason, Fix: fix, Detail: wantDetail}
	if len(data.Mine.Skipped) != 1 || data.Mine.Skipped[0] != want {
		t.Errorf("skipped = %+v; want [%+v]", data.Mine.Skipped, want)
	}
	if fix != wantFix {
		t.Errorf("fixture fix = %s; test expects %s", fix, wantFix)
	}
}

// vendorFixture changes a vendored line.
func vendorFixture(t *testing.T) (*repo, string) {
	t.Helper()
	r := newRepo(t)
	r.write("main.go", "package main\n")
	base := r.commit("feat: base")
	r.switchNew("feature")
	r.write("vendor/lib/x.go", "package lib\n\nvar X = 1\n")
	r.commit("feat: add vendor")
	r.switchMain()
	r.merge("feature", "Merge pull request #3 from acme/feature")
	r.write("vendor/lib/x.go", "package lib\n\nvar X = 2\n")
	return r, base
}

// nodeModulesFixture changes a dependency file.
func nodeModulesFixture(t *testing.T) (*repo, string) {
	t.Helper()
	r := newRepo(t)
	r.write("main.go", "package main\n")
	base := r.commit("feat: base")
	r.switchNew("feature")
	r.write("web/node_modules/pkg/index.js", "let x = 1;\n")
	r.commit("feat: add a dependency")
	r.switchMain()
	r.merge("feature", "Merge pull request #7 from acme/feature")
	r.write("web/node_modules/pkg/index.js", "let x = 2;\n")
	return r, base
}

// thirdPartyFixture changes a vendored third-party file.
func thirdPartyFixture(t *testing.T) (*repo, string) {
	t.Helper()
	r := newRepo(t)
	r.write("main.go", "package main\n")
	base := r.commit("feat: base")
	r.switchNew("feature")
	r.write("third_party/z/base.c", "int x = 0;\n")
	r.commit("feat: add third party code")
	r.switchMain()
	r.merge("feature", "Merge pull request #8 from acme/feature")
	r.write("third_party/z/base.c", "int x = 1;\n")
	return r, base
}

// generatedFixture changes a generated line.
func generatedFixture(t *testing.T) (*repo, string) {
	t.Helper()
	r := newRepo(t)
	r.write("main.go", "package main\n")
	base := r.commit("feat: base")
	r.switchNew("feature")
	r.write("api.pb.go", "package main\n\nvar A = 1\n")
	r.commit("feat: add generated")
	r.switchMain()
	r.merge("feature", "Merge pull request #4 from acme/feature")
	r.write("api.pb.go", "package main\n\nvar A = 2\n")
	return r, base
}

// wideFixture changes six files.
func wideFixture(t *testing.T) (*repo, string) {
	t.Helper()
	r := newRepo(t)
	r.write("main.go", "package main\n")
	base := r.commit("feat: base")
	r.switchNew("feature")
	for _, name := range []string{"a.txt", "b.txt", "c.txt", "d.txt", "e.txt", "f.txt"} {
		r.write(name, "old\n")
	}
	r.commit("feat: add files")
	r.switchMain()
	r.merge("feature", "Merge pull request #5 from acme/feature")
	for _, name := range []string{"a.txt", "b.txt", "c.txt", "d.txt", "e.txt", "f.txt"} {
		r.write(name, "new\n")
	}
	return r, base
}

// docsFixture changes only a documentation file.
func docsFixture(t *testing.T) (*repo, string) {
	t.Helper()
	r := newRepo(t)
	r.write("main.go", "package main\n")
	base := r.commit("feat: base")
	r.write("docs/guide.md", "old\n")
	r.commit("docs: add guide")
	r.write("docs/guide.md", "new\n")
	return r, base
}

// addedOnlyFixture adds a line without deleting one.
func addedOnlyFixture(t *testing.T) (*repo, string) {
	t.Helper()
	r := newRepo(t)
	r.write("main.go", "package main\n\nfunc main() {\n\tx := 0\n}\n")
	base := r.commit("feat: base")
	r.write("main.go", "package main\n\nfunc main() {\n\tx := 0\n\t_ = x\n}\n")
	return r, base
}

// binaryFixture changes a binary file.
func binaryFixture(t *testing.T) (*repo, string) {
	t.Helper()
	r := newRepo(t)
	r.write("main.go", "package main\n")
	base := r.commit("feat: base")
	path := filepath.Join(r.dir, "blob.bin")
	if err := os.WriteFile(path, []byte{0, 1, 2}, 0o644); err != nil {
		t.Fatal(err)
	}
	r.commit("feat: add blob")
	if err := os.WriteFile(path, []byte{0, 3, 4}, 0o644); err != nil {
		t.Fatal(err)
	}
	return r, base
}

// TestMineTwoCandidates pins the published order and the human rendering.
//
//nolint:revive // ordering, human lines, and the rerun read as one leg
func TestMineTwoCandidates(t *testing.T) {
	r, base := twoCandidateRepo(t)
	rec := &recorder{}
	resp, ref := Run(t.Context(), &Request{
		Runtime:  &core.Runtime{Commander: coreexec.Runner{MissingTool: true}, Progress: rec, Env: userEnv(t, nil)},
		RepoRoot: r.dir, Mode: modeMine, Since: base, Until: "HEAD",
	})
	if ref != nil {
		t.Fatalf("refusal %s: %s", ref.Code, ref.Message)
	}
	got := resp.Data.Mine.Candidates
	if len(got) != 2 {
		t.Fatalf("candidates = %d; want 2", len(got))
	}
	if got[0].Head != "8eaf331ac028a7e36f1646ae7bd72f8236673406" || got[1].Head != "dc7e5b65ac0af10249b8fe2c905ffe7a94b18acc" {
		t.Errorf("candidate order = %s, %s; want oldest merge first",
			got[0].Head, got[1].Head)
	}
	if got[0].MergedAt != "2024-01-03T00:00:00Z" || got[1].MergedAt != "2024-01-06T00:00:00Z" {
		t.Errorf("merge dates = %s, %s", got[0].MergedAt, got[1].MergedAt)
	}
	want := []string{
		"backtest mine a9f9d70-7572ab2  (7572ab2..HEAD a9f9d70)",
		"  8eaf331  2024-01-03  #1  first-parent-merge  a.txt:1 unclassified",
		"  dc7e5b6  2024-01-06  #2  first-parent-merge  b.txt:1 unclassified",
		"skipped: none",
		"not citable — SZZ-derived: candidates are blame-derived guesses, not verified defects",
	}
	if !slices.Equal(rec.lines, want) {
		t.Errorf("human lines =\n%s\nwant\n%s", strings.Join(rec.lines, "\n"), strings.Join(want, "\n"))
	}
	first := readMine(t, r, resp.Data.Run)
	_, ref = Run(t.Context(), &Request{
		Runtime:  &core.Runtime{Commander: coreexec.Runner{MissingTool: true}, Env: userEnv(t, nil)},
		RepoRoot: r.dir, Mode: modeMine, Since: base, Until: "HEAD",
	})
	if ref != nil {
		t.Fatalf("rerun refusal %s: %s", ref.Code, ref.Message)
	}
	if second := readMine(t, r, resp.Data.Run); !bytes.Equal(first, second) {
		t.Error("mine.json is not byte-identical on rerun")
	}
}

// TestMineDuration resolves a calendar span against --until's commit date.
func TestMineDuration(t *testing.T) {
	r, base := twoCandidateRepo(t)
	data := runMine(t, r, "90d")
	if data.Since.Spec != "90d" || data.Since.Cutoff != "2023-10-09T00:00:00Z" {
		t.Errorf("since = %+v; want spec 90d and cutoff 2023-10-09T00:00:00Z", data.Since)
	}
	if data.Since.SHA != base || data.Until.SHA != "a9f9d708fbe1bc342e77a55c3b2cd8d39dc27734" {
		t.Errorf("window = %s..%s; want %s..a9f9d70", data.Since.SHA, data.Until.SHA, base)
	}
	if data.Run != "a9f9d70-90d" {
		t.Errorf("run = %q; want %q", data.Run, "a9f9d70-90d")
	}
	if len(data.Mine.Candidates) != 2 {
		t.Errorf("candidates = %d; want 2", len(data.Mine.Candidates))
	}
}

// TestMineArtifacts pins run.json's resolution and mine.json's timestamp-free
// contract.
//
//nolint:revive,cyclop // one artifact contract, asserted inline
func TestMineArtifacts(t *testing.T) {
	b, _ := newMergedFixRepo(t)
	clock := func() time.Time { return time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC) }
	resp, ref := Run(t.Context(), &Request{
		Runtime:  &core.Runtime{Commander: coreexec.Runner{MissingTool: true}, Now: clock, Env: userEnv(t, nil)},
		RepoRoot: b.r.dir, Mode: modeMine, Since: b.base, Until: "HEAD",
	})
	if ref != nil {
		t.Fatalf("refusal %s: %s", ref.Code, ref.Message)
	}
	dir := filepath.Join(b.r.dir, ".verdict", "backtest", resp.Data.Run)
	raw, err := os.ReadFile(filepath.Join(dir, "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc RunDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.CreatedAt != "2024-06-01T00:00:00Z" {
		t.Errorf("created_at = %q; want 2024-06-01T00:00:00Z", doc.CreatedAt)
	}
	if !strings.HasPrefix(doc.GitVersion, "git version ") {
		t.Errorf("git_version = %q; want a git version line", doc.GitVersion)
	}
	if doc.Since.SHA != b.base || doc.Until.SHA != "29320a6e9fac8488fb678921021d68af35843beb" {
		t.Errorf("run.json window = %s..%s", doc.Since.SHA, doc.Until.SHA)
	}
	if doc.Mode != modeMine || doc.Method != methodSZZ || doc.SchemaVersion != "1" {
		t.Errorf("run.json shape = %+v", doc)
	}
	mine, err := os.ReadFile(filepath.Join(dir, "mine.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(mine), "2024-06-01") || strings.Contains(string(mine), "created_at") {
		t.Errorf("mine.json carries a timestamp:\n%s", mine)
	}
	if resp.Data.Artifacts.Run != ".verdict/backtest/29320a6-84b3173/run.json" ||
		resp.Data.Artifacts.Mine != ".verdict/backtest/29320a6-84b3173/mine.json" {
		t.Errorf("artifacts = %+v; want the literal run paths", resp.Data.Artifacts)
	}
}

// TestMineMergeParent proves a merge introducer expands through its second
// parent, and that the conflict text then fails the anchor honestly.
func TestMineMergeParent(t *testing.T) {
	r, base, feature, merge, fix := conflictRepo(t)
	rt := &core.Runtime{Commander: coreexec.Runner{MissingTool: true}}
	ss := &seedState{
		st:    &state{req: &Request{Runtime: rt}, rt: rt, root: r.dir},
		since: Resolution{SHA: base}, until: Resolution{SHA: fix},
		units: map[string]prUnit{}, prCaps: map[string]bool{}, builders: map[string]*candidateBuilder{},
	}
	unit, ref := ss.resolvePR(t.Context(), merge)
	if ref != nil {
		t.Fatalf("refusal %s: %s", ref.Code, ref.Message)
	}
	if unit.head != feature || unit.base != base || unit.landed != merge {
		t.Errorf("unit = %+v; want head %s base %s landed %s", unit, feature, base, merge)
	}
	if unit.reconstruction != reconMergeParent {
		t.Errorf("reconstruction = %q; want %q", unit.reconstruction, reconMergeParent)
	}
	data := runMine(t, r, base)
	want := Skipped{Reason: "unaligned", Fix: fix, PR: feature, Detail: "a.txt:1"}
	if len(data.Mine.Skipped) != 1 || data.Mine.Skipped[0] != want {
		t.Errorf("skipped = %+v; want [%+v]", data.Mine.Skipped, want)
	}
}

// TestMineRefusals pins every window and run-id refusal.
//
//nolint:revive // the refusal matrix reads as one block
func TestMineRefusals(t *testing.T) {
	r, _ := twoCandidateRepo(t)
	outside := t.TempDir()
	tests := []struct { //nolint:govet // test table readability
		name string
		req  Request
		code string
	}{
		{"no since", Request{RepoRoot: r.dir, Mode: modeMine}, "bad-since"},
		{"bare m", Request{RepoRoot: r.dir, Mode: modeMine, Since: "18m"}, "bad-since"},
		{"unknown since", Request{RepoRoot: r.dir, Mode: modeMine, Since: "nope"}, "bad-since"},
		{"since after", Request{RepoRoot: r.dir, Mode: modeMine, Since: "HEAD", Until: "HEAD~1"}, "bad-since"},
		{"unknown until", Request{RepoRoot: r.dir, Mode: modeMine, Since: "HEAD~3", Until: "nope"}, "bad-until"},
		{"not a repo", Request{RepoRoot: outside, Mode: modeMine, Since: "HEAD~1"}, "not-a-repo"},
		{"bad run", Request{RepoRoot: r.dir, Mode: modeMine, Since: "HEAD~3", Run: "Bad Slug"}, "bad-slug"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.req.Runtime = &core.Runtime{Commander: coreexec.Runner{MissingTool: true}, Env: userEnv(t, nil)}
			_, ref := Run(t.Context(), &tc.req)
			if ref == nil {
				t.Fatal("no refusal")
			}
			if ref.Code != tc.code {
				t.Errorf("code = %s; want %s", ref.Code, tc.code)
			}
		})
	}
}

// TestMineRunMismatch proves an existing run with another resolution refuses.
func TestMineRunMismatch(t *testing.T) {
	r, _ := twoCandidateRepo(t)
	seed := Request{Runtime: &core.Runtime{Commander: coreexec.Runner{MissingTool: true}, Env: userEnv(t, nil)}, RepoRoot: r.dir, Mode: modeMine, Since: "HEAD~3", Run: "fixed"}
	if _, ref := Run(t.Context(), &seed); ref != nil {
		t.Fatalf("seed run: %s: %s", ref.Code, ref.Message)
	}
	seed.Until = "HEAD~1"
	_, ref := Run(t.Context(), &seed)
	if ref == nil || ref.Code != "run-mismatch" {
		t.Fatalf("refusal = %+v; want run-mismatch", ref)
	}
}

// TestMineCorruptRun proves an unreadable run.json is a typed store refusal.
func TestMineCorruptRun(t *testing.T) {
	r, _ := twoCandidateRepo(t)
	dir := filepath.Join(r.dir, ".verdict", "backtest", "fixed")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "run.json"), []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, ref := Run(t.Context(), &Request{
		Runtime: &core.Runtime{Commander: coreexec.Runner{MissingTool: true}, Env: userEnv(t, nil)}, RepoRoot: r.dir,
		Mode: modeMine, Since: "HEAD~3", Run: "fixed",
	})
	if ref == nil || ref.Code != "corrupt-store" {
		t.Fatalf("refusal = %+v; want corrupt-store", ref)
	}
}
