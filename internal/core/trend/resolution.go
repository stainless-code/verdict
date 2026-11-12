package trendcore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

type resolutionCell struct{ reviews, fixed, judged int }

func resolutionAudit(st *state, rows []row) any {
	models, panels := map[string]*resolutionCell{}, map[string]*resolutionCell{}
	measured, unmeasured := 0, 0
	for i := range rows {
		r := &rows[i]
		dir := filepath.Join(readcommon.StateDir(st.req.RepoRootPath()), r.bundle)

		fixed, judged, ok := readResolution(dir)
		if !ok {
			unmeasured++
			st.warn("resolution unmeasured: " + r.bundle + " (missing or malformed calibration.json)")
			continue
		}

		measured++
		if judged == 0 {
			continue
		}
		addModelResolution(models, r, fixed, judged)
		panel := resolutionPanel(dir)
		addResolution(panels, panel, fixed, judged)
	}
	if measured == 0 {
		st.human("resolution: unmeasured (no calibration.json yet — run calibrate)")
		return nil
	}
	st.human(fmt.Sprintf("unmeasured: %d bundles (run calibrate)", unmeasured))
	return jq.NewObj("by_model", resolutionCells(st, "model", models),
		"by_panel", resolutionCells(st, "panel", panels), "unmeasured", unmeasured)
}

func addResolution(cells map[string]*resolutionCell, name string, fixed, judged int) {
	if cells[name] == nil {
		cells[name] = &resolutionCell{}
	}
	c := cells[name]
	c.reviews++
	c.fixed += fixed
	c.judged += judged
}

func resolutionCells(st *state, kind string, cells map[string]*resolutionCell) *jq.Obj {
	keys := make([]string, 0, len(cells))
	for key := range cells {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	doc := jq.NewObj()
	for _, key := range keys {
		c := cells[key]
		doc.Set(key, jq.NewObj("reviews", c.reviews, "judged", c.judged, "fixed", c.fixed,
			"resolution_rate", rateOrNil(c.fixed, c.judged), "n", c.judged))
		st.human(fmt.Sprintf("resolution %s %s: %.1f%% (n=%d)", kind, key, percentScale*float64(c.fixed)/float64(c.judged), c.judged))
	}
	return doc
}

func readResolution(dir string) (int, int, bool) {
	raw, err := jq.CappedFile(filepath.Join(dir, "calibration.json"), jq.MaxFindingsBytes)
	if err != nil {
		return 0, 0, false
	}
	var receipt struct {
		Precision *struct {
			Fixed  *int `json:"fixed"`
			Judged *int `json:"judged"`
		} `json:"precision"`
		Version string `json:"schema_version"`
	}
	if json.Unmarshal(raw, &receipt) != nil || receipt.Version != "1" || receipt.Precision == nil {
		return 0, 0, false
	}
	p := receipt.Precision
	if p.Fixed == nil || p.Judged == nil {
		return 0, 0, false
	}
	return *p.Fixed, *p.Judged, *p.Fixed >= 0 && *p.Judged >= *p.Fixed
}

func resolutionPanel(dir string) string {
	raw, err := os.ReadFile(filepath.Join(dir, "panel"))
	if err != nil {
		return "unpinned"
	}
	names := strings.Fields(string(raw))
	if len(names) == 0 {
		return "unpinned"
	}
	return strings.Join(names, ",")
}

func addModelResolution(models map[string]*resolutionCell, r *row, fixed, judged int) {
	names := r.models
	if len(names) == 0 {
		names = []any{"unmarked"}
	}
	for _, name := range names {
		addResolution(models, jq.Str(name), fixed, judged)
	}
}
