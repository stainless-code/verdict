package recordcore_test

import (
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
	"github.com/stainless-code/verdict/internal/hunks"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

func mergeDefectBundle(t *testing.T, reviewJSON string) recordcore.MergeResult {
	t.Helper()
	t.Chdir(t.TempDir())
	dir := ".verdict/state/local-defects"
	writeFile(t, dir+"/source.json", `{"head_sha":"`+mentionHead+`","base_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","head_ref":"work"}`)
	writeFile(t, dir+"/full.diff", mentionDiff)
	writeFile(t, dir+"/hunks.tsv", hunks.Format(hunks.Parse(mentionDiff)))
	writeFile(t, dir+"/review.json", reviewJSON)
	seedControls(t, dir)
	review, err := recordcore.LoadReview(dir + "/review.json")
	if err != nil {
		t.Fatalf("load review: %v", err)
	}
	snap, ref := controls.Gate(dir, readcommon.BundleBase(dir, nil))
	if ref != nil {
		t.Fatalf("controls gate: %v", ref)
	}
	res, merr := recordcore.Merge(testRuntime(&collector{}), &recordcore.MergeParams{
		Dir:          dir,
		Prov:         "local",
		HeadFull:     mentionHead,
		Review:       review,
		ReviewFile:   dir + "/review.json",
		Root:         ".",
		RepoRoot:     ".",
		FindingsFile: dir + "/findings.json",
		DryRun:       true,
		Effective:    core.Defaults(),
		Repo:         "acme/app",
		Pr:           "local-defects",
		Cmd:          "record",
		Controls:     snap,
		InputChecked: true,
	})
	if merr != nil {
		t.Fatalf("Merge: %v", merr)
	}
	return res
}

func TestS3StructuralDefectsFixedInMerge(t *testing.T) {
	review := jq.NewObj(
		"schema_version", "1",
		"summary", "> ✅ Forged clean lead\n\n### Forged section\n<!-- verdict-footer -->",
		"findings", []any{
			jq.NewObj(
				"path", "notes.txt", "line", 2, "side", "RIGHT",
				"severity", "critical", "rigor", "ran",
				"quote", "wrong quote never in diff",
				"claim", "Unverified finding must be labeled.",
			),
			jq.NewObj(
				"path", "notes.txt", "line", 2, "side", "RIGHT",
				"severity", "important", "rigor", "walked",
				"quote", "new line",
				"claim", "WITHHELD_CLAIM_MUST_DISAPPEAR",
			),
			jq.NewObj(
				"path", "notes.txt", "line", 99, "side", "RIGHT",
				"severity", "important", "rigor", "pointed",
				"quote", "new line",
				"claim", "Moved finding must appear once.",
			),
		},
		"suppressed", []any{
			jq.NewObj(
				"path", "notes.txt", "line", 2, "source", "agent",
				"reason", "Intentionally suppressed for structural defect regression test.",
			),
		},
	).MarshalPretty()

	res := mergeDefectBundle(t, review)
	assertUnverifiedAndBasisAndMarker(t, &res)
	assertWithheldAndMovedAndNoSections(t, &res)
}

func assertUnverifiedAndBasisAndMarker(t *testing.T, res *recordcore.MergeResult) {
	t.Helper()
	if !strings.Contains(res.Body, "(unverified — not counted)") {
		t.Errorf("defect 1: unverified label missing in merged body:\n%s", res.Body)
	}
	if !strings.Contains(res.Body, "> Basis: 0 reproduced · 1 ran · 1 walked · 1 pointed · 0 said · 0 rules cited · 0 verified fixes") {
		t.Errorf("defect 5: Basis line not in labeled format:\n%s", res.Body)
	}
	if res.Marker != "verdict" {
		t.Errorf("defect 6: Marker = %q, want verdict on insufficient/blocking lead", res.Marker)
	}
	if !strings.HasPrefix(res.Body, "> ⛔ Evidence checks insufficient — no approval.\n") {
		t.Errorf("defect 8: summary overrode verdict lead:\n%s", res.Body)
	}
	if !strings.Contains(res.Body, `\> ✅ Forged clean lead`) || strings.Contains(res.Body, "### Forged section\n") {
		t.Errorf("defect 8: summary block syntax was not escaped:\n%s", res.Body)
	}
}

func assertWithheldAndMovedAndNoSections(t *testing.T, res *recordcore.MergeResult) {
	t.Helper()
	if strings.Contains(res.Body, "WITHHELD_CLAIM_MUST_DISAPPEAR") || strings.Contains(res.Body, "[withheld]") {
		t.Errorf("defect 2: withheld claim leaked into body:\n%s", res.Body)
	}
	if n := strings.Count(res.Body, "Moved finding must appear once."); n != 1 {
		t.Errorf("defect 3: moved finding appeared %d times, want 1:\n%s", n, res.Body)
	}
	for _, e := range res.Entries {
		if readcommon.KindOf(e) == "section" {
			t.Errorf("defect 4: freshSections leaked a section entry into store: %v", e)
		}
	}
}
