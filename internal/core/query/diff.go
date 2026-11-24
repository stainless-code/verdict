package query

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/diff"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// DiffView is the composed diff read: the envelope [View] with a diff
// payload. The alias keeps the shipped Diff accessor name from the ticket.
type DiffView = View

// Risk boost weights for the optional risk signal.
const (
	riskBoostNone     = 0
	riskBoostLow      = 1
	riskBoostMedium   = 2
	riskBoostHigh     = 3
	riskBoostCritical = 4
	// itoaBufSize bounds one itoa scratch buffer: a 64-bit int needs 20.
	itoaBufSize = 21
)

// diffFinding is one findings.json entry anchored on path, line, and side.
type diffFinding struct {
	Path     string
	Side     string
	Sig      string
	Severity string
	Category string
	Body     string
	Status   string
	Line     int
}

type findingIndex map[string][]diffFinding

func (idx findingIndex) key(path string, line int, side string) string {
	return path + "\x00" + itoa(line) + "\x00" + strings.ToUpper(side)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [itoaBufSize]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%decimalBase)
		n /= decimalBase
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// Diff composes one bundle's review diff: the parsed full.diff with
// word-level spans, collapsed gaps, anchored findings, and lanes grouped
// in risk order. It reads strictly from the confined bundle directory.
func (l *Layer) Diff(ctx context.Context, rootID, bundle string) (*View, error) {
	root, dir, err := resolveDiffBundle(l, rootID, bundle)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	files := loadDiffFiles(dir)
	idx := loadDiffFindings(dir)
	return buildDiffView(l, root, bundle, dir, files, idx), nil
}

func resolveDiffBundle(l *Layer, rootID, bundle string) (Root, string, error) {
	root, ok := l.rootByID(rootID)
	if !ok {
		return Root{}, "", ErrNotFound
	}
	dir, ok, ref := l.resolve(root, bundle)
	if ref != nil {
		return Root{}, "", ref
	}
	if !ok {
		return Root{}, "", ErrNotFound
	}
	return root, dir, nil
}

func loadDiffFiles(dir string) []diff.FileDiff {
	raw := readDiffText(dir)
	files, perr := diff.Parse(raw)
	if perr != nil && !errors.Is(perr, diff.ErrTooLarge) {
		return nil
	}
	diff.ComputeGaps(files)
	return files
}

func readDiffText(dir string) string {
	raw, err := readcommon.ReadRegularCapped(dir+"/full.diff", diff.MaxDiffBytes)
	if err != nil {
		return ""
	}
	return string(raw)
}

func buildDiffView(l *Layer, root Root, bundle, dir string, files []diff.FileDiff, idx findingIndex) *View {
	boost := loadRiskBoost(dir)
	sources := inventoryDiffSources(root, bundle, dir)
	data := diffPayload(root, bundle, files, idx, boost, sources)
	return &View{
		At:      l.newView().At,
		Repo:    bundleRepo(dir),
		Pr:      bundle,
		Data:    data,
		Sources: sources,
	}
}

func diffPayload(root Root, bundle string, files []diff.FileDiff, idx findingIndex, boost int, sources []Source) *jq.Obj {
	return jq.NewObj(
		keyRoot, root.ID,
		keyBundle, bundle,
		"lanes", buildDiffLanes(files, idx, boost),
		"files", buildDiffFiles(files, idx),
		keyPartial, false,
		keySources, sourcesWire(sources),
		keyErrors, []any{},
	)
}

func loadDiffFindings(dir string) findingIndex {
	idx := findingIndex{}
	raw, err := readcommon.ReadRegularCapped(dir+"/findings.json", sourceCap)
	if err != nil {
		return idx
	}
	arr, derr := jq.DecodeOrdered(raw)
	if derr != nil {
		return idx
	}
	list, ok := arr.([]any)
	if !ok {
		return idx
	}
	indexFindings(idx, list)
	return idx
}

func indexFindings(idx findingIndex, list []any) {
	for i := range list {
		obj, ok := list[i].(*jq.Obj)
		if ok {
			addFinding(idx, obj)
		}
	}
}

func addFinding(idx findingIndex, obj *jq.Obj) {
	f := parseDiffFinding(obj)
	if f.Path == "" || f.Line == 0 {
		return
	}
	if f.Side == "" {
		f.Side = "RIGHT"
	}
	k := idx.key(f.Path, f.Line, f.Side)
	idx[k] = append(idx[k], f)
}

func parseDiffFinding(obj *jq.Obj) diffFinding {
	return diffFinding{
		Path:     stringField(obj, "path"),
		Side:     stringField(obj, "side"),
		Sig:      stringField(obj, "sig"),
		Severity: stringField(obj, "severity"),
		Category: stringField(obj, "category"),
		Body:     findingBody(obj),
		Status:   stringField(obj, "status"),
		Line:     lineField(obj),
	}
}

func stringField(obj *jq.Obj, key string) string {
	v, ok := obj.Get(key)
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

func findingBody(obj *jq.Obj) string {
	if body := stringField(obj, "body"); body != "" {
		return body
	}
	return stringField(obj, "claim")
}

func lineField(obj *jq.Obj) int {
	v, ok := obj.Get("line")
	if !ok {
		return 0
	}
	return toInt(v)
}

func toInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case jq.Number:
		return numToInt(string(n))
	default:
		return 0
	}
}

func numToInt(s string) int {
	n, err := strconv.Atoi(s)
	if err == nil {
		return n
	}
	return 0
}

// loadRiskBoost reads the optional risk level from record or review JSON.
// It returns a small weight the lane ordering folds in; 0 when absent.
func loadRiskBoost(dir string) int {
	for _, name := range riskFiles {
		if boost := boostFromFile(dir, name); boost != riskBoostNone {
			return boost
		}
	}
	return riskBoostNone
}

// riskFiles are the bundle artifacts that may carry a risk level.
var riskFiles = []string{"record.json", "review.json", "risk.json"}

func boostFromFile(dir, name string) int {
	m, ok := decodeObj(dir + "/" + name)
	if !ok {
		return riskBoostNone
	}
	return boostForLevel(riskLevel(m))
}

func decodeObj(path string) (*jq.Obj, bool) {
	raw, err := readcommon.ReadRegularCapped(path, sourceCap)
	if err != nil {
		return nil, false
	}
	obj, derr := jq.DecodeOrdered(raw)
	if derr != nil {
		return nil, false
	}
	return objToObj(obj)
}

func objToObj(v any) (*jq.Obj, bool) {
	obj, ok := v.(*jq.Obj)
	if !ok {
		return nil, false
	}
	return obj, true
}

func boostForLevel(level string) int {
	switch strings.ToLower(level) {
	case "critical":
		return riskBoostCritical
	case "high":
		return riskBoostHigh
	case "medium":
		return riskBoostMedium
	case "low":
		return riskBoostLow
	default:
		return riskBoostNone
	}
}

func riskLevel(m *jq.Obj) string {
	if level := riskNested(m); level != "" {
		return level
	}
	return stringField(m, "risk_level")
}

func riskNested(m *jq.Obj) string {
	v, ok := m.Get("risk")
	if !ok {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return riskObjLevel(v)
}

func riskObjLevel(v any) string {
	obj, ok := v.(*jq.Obj)
	if !ok {
		return ""
	}
	return stringField(obj, "level")
}

func inventoryDiffSources(root Root, bundle, dir string) []Source {
	var out []Source
	for _, name := range diffArtifacts {
		if source, ok := diffSource(root, bundle, dir, name); ok {
			out = append(out, source)
		}
	}
	return out
}

// diffArtifacts are the traceability inputs a diff row cites.
var diffArtifacts = []string{"full.diff", "PR.json", "source.json", "findings.json"}

func diffSource(root Root, bundle, dir, name string) (Source, bool) {
	raw, err := readcommon.ReadRegularCapped(filepath.Join(dir, name), sourceCap)
	if err != nil {
		return Source{}, false
	}
	rel := filepath.ToSlash(filepath.Join(bundleDir(bundle), name))
	return Source{Root: root.ID, Bundle: bundle, Path: rel, SHA256: digestBytes(raw), Bytes: int64(len(raw))}, true
}

func buildDiffFiles(files []diff.FileDiff, idx findingIndex) []any {
	var out []any
	for i := range files {
		out = append(out, diffFileWire(&files[i], idx))
	}
	return out
}

func diffFileWire(f *diff.FileDiff, idx findingIndex) any {
	path := f.NewPath
	if path == "" {
		path = f.OldPath
	}
	obj := fileHead(f, path)
	obj.Set("hunks", hunkWires(f, path, idx))
	return obj
}

func fileHead(f *diff.FileDiff, path string) *jq.Obj {
	obj := jq.NewObj(
		"path", path,
		"old_path", f.OldPath,
		"new_path", f.NewPath,
		"lane", string(diff.Classify(f)),
		"is_binary", f.IsBinary,
		"is_new", f.IsNew,
		"is_deleted", f.IsDeleted,
		"is_rename", f.IsRename,
		"is_mode_only", f.IsModeOnly,
		"generated", f.Generated,
		"additions", f.Additions,
		"deletions", f.Deletions,
	)
	if f.PreviousPath != "" {
		obj.Set("previous_path", f.PreviousPath)
	}
	if f.Index != "" {
		obj.Set("index", f.Index)
	}
	return obj
}

func hunkWires(f *diff.FileDiff, path string, idx findingIndex) []any {
	hunks := make([]any, 0, len(f.Hunks))
	for i := range f.Hunks {
		hunks = append(hunks, hunkWire(&f.Hunks[i], path, idx))
	}
	return hunks
}

func hunkWire(h *diff.Hunk, path string, idx findingIndex) *jq.Obj {
	oldM, newM := diff.PairWordSpans(h.Lines)
	lines := lineWires(h, path, idx, oldM, newM)
	obj := jq.NewObj(
		"header", h.Header,
		"old_start", h.OldStart,
		"old_count", h.OldCount,
		"new_start", h.NewStart,
		"new_count", h.NewCount,
		"lines", lines,
	)
	if h.GapBefore > 0 {
		obj.Set("gap_before", h.GapBefore)
	}
	return obj
}

func lineWires(h *diff.Hunk, path string, idx findingIndex, oldM, newM map[int][]diff.WordSpan) []any {
	lines := make([]any, 0, len(h.Lines))
	for j := range h.Lines {
		lines = append(lines, lineWire(&h.Lines[j], j, path, idx, oldM, newM))
	}
	return lines
}

func lineWire(l *diff.Line, pos int, path string, idx findingIndex, oldM, newM map[int][]diff.WordSpan) *jq.Obj {
	row := jq.NewObj("kind", kindString(l.Kind), "text", l.Text)
	if l.OldNo != 0 {
		row.Set("old_no", l.OldNo)
	}
	if l.NewNo != 0 {
		row.Set("new_no", l.NewNo)
	}
	attachSpans(row, l, pos, oldM, newM)
	attachFindings(row, l, path, idx)
	return row
}

func attachSpans(row *jq.Obj, l *diff.Line, pos int, oldM, newM map[int][]diff.WordSpan) {
	spans := spansFor(l, pos, oldM, newM)
	if len(spans) > 0 {
		row.Set("spans", spans)
	}
}

func spansFor(l *diff.Line, pos int, oldM, newM map[int][]diff.WordSpan) []any {
	spanMap := spanMapFor(l, oldM, newM)
	if spanMap == nil {
		return nil
	}
	ws, ok := spanMap[pos]
	if !ok {
		return nil
	}
	return spanWires(ws)
}

func spanMapFor(l *diff.Line, oldM, newM map[int][]diff.WordSpan) map[int][]diff.WordSpan {
	switch l.Kind {
	case diff.KindDel:
		return oldM
	case diff.KindAdd:
		return newM
	default:
		return nil
	}
}

func spanWires(ws []diff.WordSpan) []any {
	var spans []any
	for i := range ws {
		s := ws[i]
		spans = append(spans, jq.NewObj("text", s.Text, "start", s.Start, "end", s.End, "changed", s.Changed))
	}
	return spans
}

func attachFindings(row *jq.Obj, l *diff.Line, path string, idx findingIndex) {
	lineNo, side := anchorPoint(l)
	if lineNo == 0 {
		return
	}
	fs, ok := idx[idx.key(path, lineNo, side)]
	if ok {
		row.Set("findings", findingWires(fs))
	}
}

func anchorPoint(l *diff.Line) (int, string) {
	if l.Kind == diff.KindDel {
		return l.OldNo, "LEFT"
	}
	return l.NewNo, "RIGHT"
}

func findingWires(fs []diffFinding) []any {
	var out []any
	for i := range fs {
		f := fs[i]
		out = append(out, jq.NewObj(
			"sig", f.Sig, "severity", f.Severity, "category", f.Category,
			"body", f.Body, "status", f.Status, "path", f.Path, "line", f.Line, "side", f.Side,
		))
	}
	return out
}

func kindString(k diff.LineKind) string {
	switch k {
	case diff.KindAdd:
		return "add"
	case diff.KindDel:
		return "del"
	default:
		return "context"
	}
}

type laneCounts struct {
	filesByPath map[string]int
	lensByLane  map[diff.Lane]map[string]int
}

// countLaneFindings tallies findings per file and lenses per lane.
func countLaneFindings(files []diff.FileDiff, idx findingIndex) laneCounts {
	counts := laneCounts{
		filesByPath: map[string]int{},
		lensByLane:  map[diff.Lane]map[string]int{},
	}
	for i := range files {
		countFileFindings(&files[i], idx, &counts)
	}
	return counts
}

func countFileFindings(f *diff.FileDiff, idx findingIndex, counts *laneCounts) {
	path := effectiveDiffPath(f)
	lane := diff.Classify(f)
	lenses := lanesFor(counts, lane)
	n := 0
	for _, fs := range idx {
		for j := range fs {
			if fs[j].Path == path {
				n++
				lenses[lensKey(&fs[j])]++
			}
		}
	}
	counts.filesByPath[path] = n
}

func lanesFor(counts *laneCounts, lane diff.Lane) map[string]int {
	lenses, ok := counts.lensByLane[lane]
	if !ok {
		lenses = map[string]int{}
		counts.lensByLane[lane] = lenses
	}
	return lenses
}

func lensKey(f *diffFinding) string {
	if f.Category == "" {
		return "other"
	}
	return f.Category
}

func effectiveDiffPath(f *diff.FileDiff) string {
	if f.NewPath != "" {
		return f.NewPath
	}
	return f.OldPath
}

func buildDiffLanes(files []diff.FileDiff, idx findingIndex, boost int) []any {
	counts := countLaneFindings(files, idx)
	groups := diff.GroupByLane(files)
	out := make([]any, 0, len(groups))
	for i := range groups {
		out = append(out, laneWire(&groups[i], counts.filesByPath, counts.lensByLane, boost))
	}
	return out
}

func laneWire(g *diff.LaneSummary, filesByPath map[string]int, lensByLane map[diff.Lane]map[string]int, boost int) *jq.Obj {
	findings := sumFindings(g.Files, filesByPath)
	obj := jq.NewObj(
		"lane", string(g.Lane),
		"file_count", g.FileCount,
		"additions", g.Additions,
		"deletions", g.Deletions,
		"findings", findings,
		"files", g.Files,
	)
	addLens(obj, lensByLane[g.Lane])
	if boost != riskBoostNone && g.Lane == diff.LaneSource {
		obj.Set("risk_boost", boost)
	}
	return obj
}

func sumFindings(paths []string, filesByPath map[string]int) int {
	findings := 0
	for i := range paths {
		findings += filesByPath[paths[i]]
	}
	return findings
}

func addLens(obj *jq.Obj, lenses map[string]int) {
	if len(lenses) == 0 {
		return
	}
	inner := jq.NewObj()
	for k, v := range lenses {
		inner.Set(k, v)
	}
	obj.Set("by_lens", inner)
}
