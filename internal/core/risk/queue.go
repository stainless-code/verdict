package risk

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	corpuscore "github.com/stainless-code/verdict/internal/core/corpus"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

const (
	// QueueStatusOpen is the default --status filter: bundles a host still shows
	// open, plus local bundles it never hosted.
	QueueStatusOpen = "open"
	// QueueStatusAll keeps every bundle, merged and closed included.
	QueueStatusAll = "all"
	// DefaultQueueLimit bounds the queue when the request names no limit.
	DefaultQueueLimit = 50
	// MaxQueueLimit is the largest limit the queue accepts.
	MaxQueueLimit = 200

	// queueStateOpen is a host-open bundle, and the reading for a state token
	// the queue does not know: an unrecognized value keeps its bundle in the
	// queue instead of hiding it.
	queueStateOpen = "open"
	// queueStateLocal marks a bundle with no host record, which is live.
	queueStateLocal  = "local"
	queueStateMerged = "merged"
	queueStateClosed = "closed"

	prFile = "/PR.json"
)

// skipWarn heads every warning the queue renders about an entry it passed over.
const skipWarn = "risk skips "

// noImpactWarn names the bundles whose reach went unmeasured and the two
// index runs that measure it: a provider PR re-fetches, a local bundle
// re-captures.
const noImpactWarn = "risk: %d bundle(s) carry no usable impact.json (reach unmeasured; backfill with " +
	"verdict fetch %s <pr> --index origin/main, or fetch-local for a local bundle)"

// triageOrder is the band order the queue sorts by, ascending: low, unknown,
// medium, high, critical. Unknown outranks low so an unassessed bundle reaches
// a human, and sits below medium, which already carries evidence of trouble.
var triageOrder = []string{LevelLow, LevelUnknown, LevelMedium, LevelHigh, LevelCritical}

// TriageRank returns a band's position in the queue's order: low=0 through
// critical=4. Rank and MaxLevel keep the assessed order the reported gate
// reads.
func TriageRank(band string) int {
	return max(0, slices.Index(triageOrder, band))
}

// QueueRequest carries the resolved risk invocation: the shared read runtime,
// the status filter, and the row bound.
type QueueRequest struct { //nolint:govet // field grouping documents the request surface
	core.ReadRequest
	Status   string `enum:"open|all" optional:"true"`
	Limit    int    `max:"200"       min:"1"         optional:"true"`
	Warnings []string
}

// QueueResponse carries the completed queue: the envelope fields, the human
// lines, and the typed data profile.
type QueueResponse struct {
	Data     *QueueData
	Repo     string
	At       string
	Next     string
	Warnings []string
	Human    []string
}

// QueueData is the risk verb's --json data profile: the ranked queue over the
// bundles under .verdict/state. Field order is the published property order.
type QueueData struct { //nolint:govet // field order is the published property order
	Receipt     *core.Truncation `json:"-"           receipt:"true"`
	GeneratedAt string           `format:"date-time" json:"generated_at"`
	Bundles     int              `json:"bundles"     min:"0"`
	NeedsHuman  int              `json:"needs_human" min:"0"`
	ByBand      QueueBands       `json:"by_band"`
	Rows        []QueueRow       `json:"rows"`
}

// QueueBands is the five-band tally of the queued bundles. Field order is the
// queue's own order (worst first), so the tally reads like the human summary.
type QueueBands struct {
	Critical int `json:"critical" min:"0"`
	High     int `json:"high"     min:"0"`
	Medium   int `json:"medium"   min:"0"`
	Unknown  int `json:"unknown"  min:"0"`
	Low      int `json:"low"      min:"0"`
}

// QueueRow is one ranked bundle: its recomputed band, its advisory score, and
// the artifacts each rests on. Field order is the published property order.
type QueueRow struct { //nolint:govet // field order is the published property order
	Rank           int             `json:"rank"                             min:"1"`
	Bundle         string          `json:"bundle"`
	Pr             *int            `json:"pr"                               min:"1"`
	State          string          `enum:"open|merged|closed|local"         json:"state"`
	Head           *string         `json:"head"`
	Claimed        string          `enum:"unknown|low|medium|high|critical" json:"claimed"`
	Floor          string          `enum:"low|medium|high|critical"         json:"floor"`
	Reported       string          `enum:"unknown|low|medium|high|critical" json:"reported"`
	ReceiptReasons []string        `json:"receipt_reasons"`
	Inputs         core.RiskInputs `json:"inputs"`
	Score          core.RiskScore  `json:"score"`
	NeedsHuman     bool            `json:"needs_human"`
}

// Wire renders the profile in published wire order.
func (d *QueueData) Wire() *jq.Obj {
	rows := make([]any, 0, len(d.Rows))
	for i := range d.Rows {
		rows = append(rows, core.Wire(d.Rows[i]))
	}
	return d.Receipt.Apply(jq.NewObj(
		"generated_at", d.GeneratedAt,
		"bundles", d.Bundles,
		"needs_human", d.NeedsHuman,
		"by_band", core.Wire(d.ByBand),
		"rows", rows,
	))
}

// add counts one bundle into its band.
func (b *QueueBands) add(band string) {
	switch band {
	case LevelCritical:
		b.Critical++
	case LevelHigh:
		b.High++
	case LevelMedium:
		b.Medium++
	case LevelUnknown:
		b.Unknown++
	default:
		b.Low++
	}
}

// bandCountFormat renders one band tally, e.g. "critical=1".
const bandCountFormat = "%s=%d"

// summary renders the tally worst-first, e.g. "critical=1 high=0 low=2".
func (b *QueueBands) summary() []string {
	return []string{
		fmt.Sprintf(bandCountFormat, LevelCritical, b.Critical),
		fmt.Sprintf(bandCountFormat, LevelHigh, b.High),
		fmt.Sprintf(bandCountFormat, LevelMedium, b.Medium),
		fmt.Sprintf(bandCountFormat, LevelUnknown, b.Unknown),
		fmt.Sprintf(bandCountFormat, LevelLow, b.Low),
	}
}

// Run ranks every bundle under .verdict/state into the triage queue. It is a
// pure read of the store: no host call, no index build, nothing written.
func Run(ctx context.Context, req *QueueRequest) (*QueueResponse, *core.RefusalError) {
	if err := ctx.Err(); err != nil {
		return nil, &core.RefusalError{Code: "canceled", Message: "canceled before risk completed"}
	}
	status, limit, ref := queueOptions(req)
	if ref != nil {
		return nil, ref
	}
	st := &scan{req: req, status: status, warnings: append([]string{}, req.Warnings...)}
	now := req.TimeUTC()
	rows, ref := st.collect()
	if ref != nil {
		return nil, ref
	}
	for i := range rows {
		rows[i].Rank = i + 1
	}
	total := len(rows)
	shown := rows
	if total > limit {
		shown = rows[:limit]
	}
	// A cut store outranks the row cap: the queue cannot count what it could not
	// read, so the scan signal stays in the receipt even when --limit also bit.
	receipt := core.ScanCut(st.cut, len(shown))
	if receipt == nil && total > limit {
		receipt = core.ResultCut("response_cap", len(shown), &total, nil)
	}
	bands, needsHuman := tally(rows)
	next := st.next(req.Repo, shown, st.skipped)
	st.emitHuman(req.Repo, shown, bands, needsHuman, total, total-len(shown))
	return &QueueResponse{
		Repo:     req.Repo,
		At:       now,
		Next:     next,
		Warnings: st.warnings,
		Human:    st.lines,
		Data: &QueueData{
			Receipt:     receipt,
			GeneratedAt: now,
			Bundles:     total,
			NeedsHuman:  needsHuman,
			ByBand:      bands,
			Rows:        shown,
		},
	}, nil
}

// queueOptions resolves and validates the invocation's filter and bound. A
// caller bypassing the CLI (the facade, MCP, or a direct Run) gets the same
// refusal the CLI's usage error reports.
func queueOptions(req *QueueRequest) (string, int, *core.RefusalError) {
	status := req.Status
	if status == "" {
		status = QueueStatusOpen
	}
	if status != QueueStatusOpen && status != QueueStatusAll {
		return "", 0, &core.RefusalError{
			Code:    "bad-flags",
			Message: fmt.Sprintf("risk --status wants open or all, not %q", req.Status),
		}
	}
	limit := req.Limit
	if limit == 0 {
		return status, DefaultQueueLimit, nil
	}
	if limit < 1 || limit > MaxQueueLimit {
		return "", 0, &core.RefusalError{
			Code:    "bad-flags",
			Message: fmt.Sprintf("risk --limit wants 1 to %d, not %d", MaxQueueLimit, req.Limit),
		}
	}
	return status, limit, nil
}

type scan struct {
	req      *QueueRequest
	status   string
	lines    []string
	warnings []string
	skipped  int
	seen     int
	noImpact int
	replays  int
	corpora  int
	cut      bool
}

// readState lists the bundle home. A store that is not there is an empty
// queue; one that is there and will not list is a refusal naming it, so a file
// where the directory belongs never reads as "no bundles".
func (st *scan) readState() ([]os.DirEntry, *core.RefusalError) {
	state := readcommon.StateDir(st.req.RepoRootPath())
	entries, err := os.ReadDir(state)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, &core.RefusalError{
			Code: "corrupt-store",
			Message: fmt.Sprintf("bundle store %s is unreadable (restore the directory or fix its permissions)",
				state),
		}
	}
	return entries, nil
}

// collect reads the bundle home and returns the kept rows in queue order.
func (st *scan) collect() ([]QueueRow, *core.RefusalError) {
	entries, ref := st.readState()
	if ref != nil {
		return nil, ref
	}
	rows := make([]QueueRow, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		st.seen++
		if row, ok := st.walk(entry); ok {
			rows = append(rows, row)
		}
	}
	st.warnSkipped()
	if st.noImpact > 0 {
		st.warn(fmt.Sprintf(noImpactWarn, st.noImpact, st.req.Repo))
	}
	sortRows(rows)
	return rows, nil
}

// walk folds one state-directory entry: a corpus or replay bundle is counted
// as skipped, anything else becomes a row when its state and store are
// readable.
func (st *scan) walk(entry os.DirEntry) (QueueRow, bool) {
	name := entry.Name()
	dir, ok := st.listedBundleDir(name)
	if !ok {
		return QueueRow{}, false
	}
	if corpuscore.IsDir(dir) {
		st.corpora++
		return QueueRow{}, false
	}
	if isRegular(dir + "/" + readcommon.BacktestMarkerFile) {
		st.replays++
		return QueueRow{}, false
	}
	state := bundleState(dir)
	if st.status == QueueStatusOpen && state != queueStateOpen && state != queueStateLocal {
		st.skipped++
		return QueueRow{}, false
	}
	entries, ok := st.readFindings(name, dir)
	if !ok {
		return QueueRow{}, false
	}
	return st.row(name, dir, state, entries), true
}

// listedBundleDir resolves one state-directory entry through the shared guard:
// a name it refuses is skipped with a warning, never read.
func (st *scan) listedBundleDir(name string) (string, bool) {
	dir, ref := config.BundleDir(st.req.RepoRootPath(), name)
	if ref != nil {
		st.warn(skipWarn + name + " (" + ref.Message + ")")
		return "", false
	}
	return filepath.ToSlash(dir), true
}

// readFindings reads one bundle's findings store. A bundle with no
// findings.json is included with an empty store — the unreviewed PR the queue
// exists for — while an unreadable or oversized store is skipped with the
// scan-cut receipt, so it never reads as a clean bundle.
func (st *scan) readFindings(name, dir string) ([]any, bool) {
	raw, err := jq.CappedFile(dir+findingsSuffix, jq.MaxFindingsBytes)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, true
		}
		st.drop(name, "findings.json is over the store cap or unreadable")
		return nil, false
	}
	doc, err := jq.DecodeOrdered(raw)
	if err != nil {
		st.drop(name, "findings.json is not a findings array")
		return nil, false
	}
	entries, ok := doc.([]any)
	if !ok || !readcommon.ValidEntries(entries) {
		st.drop(name, "findings.json is not a findings array")
		return nil, false
	}
	return entries, true
}

// drop records one bundle skipped for a cut store: the receipt and the warning
// both name it, so an unreadable store never reads as a clean bundle.
func (st *scan) drop(name, reason string) {
	st.cut = true
	st.warn(skipWarn + name + " (" + reason + ")")
}

// row builds one queue row from the bundle's artifacts. Reach moves the
// missing-impact count: an unmeasured component always warns, never reads as a
// clean zero.
func (st *scan) row(name, dir, state string, entries []any) QueueRow {
	head := readcommon.OpenBundle(dir).Head()
	receipt := EvaluateBundle(dir, head, CoverageIdentity(dir, head), entries, "")
	score := Score(dir, entries)
	if !score.Reach.Measured {
		st.noImpact++
	}
	reasons := receipt.ReceiptReasons
	if reasons == nil {
		reasons = []string{}
	}
	return QueueRow{
		Bundle:         name,
		Pr:             bundlePR(name),
		State:          state,
		Head:           shortHead(head),
		Claimed:        receipt.Claimed,
		Floor:          receipt.Floor,
		Reported:       receipt.Reported,
		ReceiptReasons: reasons,
		Inputs:         receipt.Inputs,
		Score:          *score,
		NeedsHuman:     receipt.Reported != LevelLow,
	}
}

func shortHead(head string) *string {
	short := readcommon.ShortSHA(head)
	if short == "" {
		return nil
	}
	return &short
}

// bundlePR reads the PR number a bundle directory names, nil for a bundle the
// queue's own spelling does not number (a local review).
func bundlePR(name string) *int {
	n, err := strconv.Atoi(name)
	if err != nil || n < 1 || strconv.Itoa(n) != name {
		return nil
	}
	return &n
}

// bundleState maps a bundle's host state onto the four published values: a
// bundle with no provider record is local (live), and any state token the
// queue does not know reads open.
func bundleState(dir string) string {
	if !readcommon.IsFile(dir + prFile) {
		return queueStateLocal
	}
	switch state := strings.ToLower(readcommon.OpenBundle(dir).State()); state {
	case queueStateMerged, queueStateClosed:
		return state
	default:
		return queueStateOpen
	}
}

// sortRows orders the queue: triage rank first, then score, then the bundle
// name byte-ascending, which makes the order total and every run identical.
func sortRows(rows []QueueRow) {
	slices.SortStableFunc(rows, func(a, b QueueRow) int {
		if c := cmp.Compare(TriageRank(b.Reported), TriageRank(a.Reported)); c != 0 {
			return c
		}
		if c := cmp.Compare(b.Score.Value, a.Score.Value); c != 0 {
			return c
		}
		return strings.Compare(a.Bundle, b.Bundle)
	})
}

// tally counts the bands and the bundles that need a human over the whole
// queue, hidden rows included, so the header and by_band agree.
func tally(rows []QueueRow) (QueueBands, int) {
	var bands QueueBands
	needsHuman := 0
	for i := range rows {
		bands.add(rows[i].Reported)
		if rows[i].NeedsHuman {
			needsHuman++
		}
	}
	return bands, needsHuman
}

func (st *scan) warnSkipped() {
	if st.replays > 0 {
		st.warn(fmt.Sprintf(skipWarn+"%d backtest bundle(s) (replay input, not your review history)", st.replays))
	}
	if st.corpora > 0 {
		st.warn(fmt.Sprintf(skipWarn+"%d corpus bundle(s) (benchmark fixture, not your review history)", st.corpora))
	}
}

func (st *scan) warn(msg string) {
	st.warnings = append(st.warnings, msg)
	st.req.Warn(msg)
}

// next names the one command that moves the queue forward: the backfill when
// the top row's reach is unmeasured, the report for the top row otherwise, and
// the wider filter when nothing open is left.
func (st *scan) next(repo string, shown []QueueRow, skipped int) string {
	switch {
	case len(shown) > 0 && !shown[0].Score.Reach.Measured:
		return backfill(repo, shown[0].Bundle)
	case len(shown) > 0:
		return fmt.Sprintf("verdict report %s %s --risk", repo, shown[0].Bundle)
	case skipped > 0:
		return fmt.Sprintf("verdict risk %s --status all", repo)
	default:
		return fmt.Sprintf("verdict fetch %s <pr>", repo)
	}
}

// backfill names the index run that measures a bundle's reach: a local bundle
// re-captures through fetch-local, a provider PR through fetch.
func backfill(repo, bundle string) string {
	verb, name := "fetch", bundle
	if bundlePR(bundle) == nil {
		verb, name = "fetch-local", readcommon.LocalName(bundle)
	}
	return fmt.Sprintf("verdict %s %s %s --index origin/main", verb, repo, name)
}

// emitHuman renders the queue: one header line, one line per row, then the
// skipped, limit, and next lines. Every line lands in Human so it prints after
// the warnings the scan streamed.
func (st *scan) emitHuman(repo string, rows []QueueRow, bands QueueBands, needsHuman, total, hidden int) {
	if total == 0 && st.seen == 0 {
		st.lines = append(st.lines, fmt.Sprintf("risk: %s — no bundles under %s (fetch a PR first)",
			repo, ".verdict/state"))
		return
	}
	st.lines = append(st.lines, fmt.Sprintf("risk: %s — %d bundle(s), %d need a human (%s)",
		repo, total, needsHuman, strings.Join(bands.summary(), " ")))
	cells := make([][]string, 0, len(rows))
	for i := range rows {
		cells = append(cells, rowCells(&rows[i]))
	}
	st.lines = append(st.lines, renderTable(cells)...)
	if st.skipped > 0 {
		st.lines = append(st.lines, fmt.Sprintf("skipped: %d bundle(s) not open (--status all includes them)", st.skipped))
	}
	if hidden > 0 {
		st.lines = append(st.lines, fmt.Sprintf("limit: %d shown, %d hidden", len(rows), hidden))
	}
	st.lines = append(st.lines, "next: "+st.next(repo, rows, st.skipped))
}

// rowCells renders one row's table cells: names, numbers, and enum bands only,
// so nothing a bundle stores can carry text into the terminal.
func rowCells(r *QueueRow) []string {
	return []string{
		strconv.Itoa(r.Rank),
		r.Reported,
		fmt.Sprintf("score=%d", r.Score.Value),
		r.Bundle,
		fmt.Sprintf("open=%d", r.Score.Category.Open),
		reachCell(r.Score.Reach),
		churnCell(r.Score.Churn),
		labelCell("cat", r.Score.Category.Worst),
		labelCell("rung", r.Score.Rung.Weakest),
	}
}

func reachCell(reach core.RiskReach) string {
	if !reach.Measured || reach.Symbols == nil || reach.Callers == nil {
		return "reach=" + cellMissing
	}
	return fmt.Sprintf("reach=%d/%d", *reach.Symbols, *reach.Callers)
}

func churnCell(churn core.RiskChurn) string {
	if !churn.Measured {
		return "churn=" + cellMissing
	}
	return fmt.Sprintf("churn=+%d/-%d", churn.Additions, churn.Deletions)
}

func labelCell(name string, value *string) string {
	if value == nil {
		return name + "=" + cellMissing
	}
	return name + "=" + *value
}

const cellMissing = "-"

// renderTable pads every cell to its column's width so the queue reads as one
// table; the last column carries no trailing spaces.
func renderTable(rows [][]string) []string {
	widths := columnWidths(rows)
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, renderRow(row, widths))
	}
	return out
}

// columnWidths measures each column's widest cell; the last column needs no
// width because nothing follows it.
func columnWidths(rows [][]string) []int {
	widths := make([]int, 0, len(rows))
	for _, row := range rows {
		for i := range len(row) - 1 {
			if i >= len(widths) {
				widths = append(widths, 0)
			}
			widths[i] = max(widths[i], len(row[i]))
		}
	}
	return widths
}

// renderRow pads one row's cells to the measured widths.
func renderRow(row []string, widths []int) string {
	var line strings.Builder
	for i, cell := range row {
		if i == len(row)-1 {
			line.WriteString(cell)
			break
		}
		fmt.Fprintf(&line, "%-*s  ", widths[i], cell)
	}
	return line.String()
}
