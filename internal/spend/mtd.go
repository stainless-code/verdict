package spend

import (
	"encoding/json"
	"errors"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/stainless-code/verdict/internal/jq"
)

// MTD is a month-to-date spend aggregate computed only from local cost
// receipts under the verdict state directory. Unknown cost stays unknown,
// never 0.
type MTD struct {
	Month    string
	KnownUSD string // exact decimal, "" when no receipt contributed a known amount
	// Warning is non-empty when the state directory scan itself failed
	// for a reason other than absence (unreadable directory). Callers
	// surface it as a warning; an absent directory stays silent.
	Warning      string
	KnownCount   int
	UnknownCount int
}

const (
	mtdDecimalBase = 10
	mtdFallbackDP  = 6
	mtdBigTwo      = 2
	mtdBigFive     = 5
	// chunksDirName is the per-chunk cost directory inside one bundle.
	chunksDirName = "chunks"
	// maxNumberLiteralLen bounds a JSON number literal before it reaches
	// [big.Rat.SetString], so a receipt cannot allocate an unbounded
	// rational; maxNumberExpAbs bounds the exponent likewise.
	maxNumberLiteralLen = 64
	maxNumberExpAbs     = 12
)

// MonthFor returns the UTC calendar month YYYY-MM for t.
func MonthFor(t time.Time) string {
	return t.UTC().Format("2006-01")
}

// AggregateMTD scans every bundle under stateDir (the per-repo state
// directory, e.g. readcommon.StateDir(repoRoot)) for cost receipts and
// sums those whose timestamp falls in the month of now. Receipts are the
// recorded cost files (cost.json plus chunk cost files) and, when a
// bundle carries none, its budget.json projected_usd ledger — one
// authoritative source per bundle. The scan never fails: unreadable
// files contribute nothing, prior-month receipts are excluded, and an
// unreadable state directory reports through MTD.Warning.
func AggregateMTD(stateDir string, now time.Time) MTD {
	month := MonthFor(now)
	m := MTD{Month: month}
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		m.Warning = stateDirWarning(stateDir, err)
		return m
	}
	sum := new(big.Rat)
	known := 0
	unknown := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		aggregateBundle(filepath.Join(stateDir, e.Name()), month, sum, &known, &unknown)
	}
	m.settle(sum, known, unknown)
	return m
}

// BundleMTD sums one bundle's cost receipts for the month of now under the
// same single-source rule [AggregateMTD] applies to every bundle, so a caller
// that budgets per bundle (the self-review loop's per-round cost) and the
// monthly aggregate can never disagree about the same receipts.
func BundleMTD(bundleDir string, now time.Time) MTD {
	month := MonthFor(now)
	m := MTD{Month: month}
	sum := new(big.Rat)
	known := 0
	unknown := 0
	aggregateBundle(bundleDir, month, sum, &known, &unknown)
	m.settle(sum, known, unknown)
	return m
}

// settle fills the aggregate's totals. A partially priced receipt contributes
// to the sum without being priced, so the exact amount is reported whenever
// any row contributed one, not only when a fully priced row did.
func (m *MTD) settle(sum *big.Rat, known, unknown int) {
	if known > 0 || sum.Sign() != 0 {
		m.KnownUSD = FormatRat(sum)
		m.KnownCount = known
	}
	m.UnknownCount = unknown
}

// stateDirWarning reports the MTD warning for a failed state directory
// scan — "" when the directory is genuinely absent. On Windows,
// [os.ReadDir] on a regular file returns ERROR_PATH_NOT_FOUND, which Go
// maps to [fs.ErrNotExist] — the same error a genuinely absent directory
// produces. [os.Stat] distinguishes the two so a state dir that is a
// regular file warns on every platform, not just non-Windows ones.
func stateDirWarning(stateDir string, err error) string {
	info, statErr := os.Stat(stateDir)
	switch {
	case statErr != nil && errors.Is(statErr, fs.ErrNotExist):
		return ""
	case statErr == nil && !info.IsDir():
		return "month-to-date scan unreadable — state directory is not a directory"
	default:
		return "month-to-date scan unreadable — state directory: " + err.Error()
	}
}

// aggregateBundle counts one bundle's spend for month from two sources with
// execution-level single-source within that month: recorded cost files are
// always counted for their own month, and the budget.json ledger is counted
// only for rows that are not the pre-run projection of already-counted
// recorded cost in the same month. A recorded file from another month never
// suppresses the requested month's ledger rows.
func aggregateBundle(bundleDir, month string, sum *big.Rat, known, unknown *int) {
	meta := aggregateRecorded(bundleDir, month, sum, known, unknown)
	aggregateLedger(bundleDir, month, sum, known, unknown, meta)
}

// unidentifiedExecID is the execution identity of a receipt that named no
// execution. cost.json is agent-written and may omit the execution_id its
// budget.json row carries, so an unidentified receipt and an identified row
// can still be two halves of one authorization.
const unidentifiedExecID = ""

// recordedMeta carries the in-month recorded-cost identity that decides
// which ledger rows are pre-run projections of already-counted spend. The map
// is keyed by execution identity and holds the latest in-month recorded
// timestamp that identity carries: presence is the execution-level match,
// and the timestamp is how far that identity's recorded cost already covers
// the ledger. Records from other months are excluded from meta, so they
// cannot suppress the requested month's ledger.
type recordedMeta struct {
	maxAt map[string]time.Time
}

// note records one in-month receipt's identity and timestamp, keeping the
// latest timestamp each identity carries.
func (m *recordedMeta) note(exec string, at time.Time) {
	if prev, ok := m.maxAt[exec]; ok && !at.After(prev) {
		return
	}
	m.maxAt[exec] = at
}

// aggregateRecorded counts the bundle's recorded cost files — cost.json
// plus the chunk cost files — mirroring the report rollup's selection:
// manifest chunk names plus the chunk-*.cost.json glob, deduplicated so
// a chunk named by both is counted once. It counts only receipts whose
// timestamp falls in month and returns the meta describing those in-month
// recorded receipts; records outside month, and records without a parseable
// timestamp, name no month's spend and never suppress the ledger.
func aggregateRecorded(bundleDir, month string, sum *big.Rat, known, unknown *int) *recordedMeta {
	meta := &recordedMeta{maxAt: map[string]time.Time{}}
	for _, name := range recordedCostNames(bundleDir) {
		countRecordedFile(bundleDir, name, month, sum, known, unknown, meta)
	}
	return meta
}

func recordedCostNames(bundleDir string) []string {
	names := []string{"cost.json"}
	names = append(names, chunkCostNames(bundleDir)...)
	slices.Sort(names)
	return slices.Compact(names)
}

func chunkCostNames(bundleDir string) []string {
	names := manifestChunkNames(bundleDir)
	return append(names, globChunkNames(bundleDir)...)
}

func manifestChunkNames(bundleDir string) []string {
	raw := openBundleFile(bundleDir, filepath.Join(chunksDirName, "manifest.json"))
	if raw == nil {
		return nil
	}
	list, ok := manifestChunkList(raw)
	if !ok {
		return nil
	}
	var names []string
	for _, c := range list {
		name, ok := manifestChunkName(c)
		if !ok {
			return nil
		}
		names = append(names, filepath.Join(chunksDirName, name+".cost.json"))
	}
	return names
}

// manifestChunkList reads the chunks manifest's chunk entries. A manifest
// that decodes to any other shape invalidates the whole manifest: a chunk
// named by a half-readable manifest would be counted by nobody.
func manifestChunkList(raw []byte) ([]any, bool) {
	doc, err := jq.DecodeOrdered(raw)
	if err != nil {
		return nil, false
	}
	obj, ok := doc.(*jq.Obj)
	if !ok {
		return nil, false
	}
	chunks, _ := obj.Get("chunks")
	list, ok := chunks.([]any)
	if !ok {
		return nil, false
	}
	return list, true
}

func manifestChunkName(c any) (string, bool) {
	chunkObj, ok := c.(*jq.Obj)
	if !ok {
		return "", false
	}
	name, _ := chunkObj.Get("name")
	chunkName, ok := chunkNameOf(name)
	if !ok || chunkName == "" || strings.Contains(chunkName, "/") {
		return "", false
	}
	return chunkName, true
}

func chunkNameOf(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case jq.Number:
		return t.String(), true
	default:
		return "", false
	}
}

func globChunkNames(bundleDir string) []string {
	ents, err := os.ReadDir(filepath.Join(bundleDir, chunksDirName))
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, "chunk-") || !strings.HasSuffix(name, ".cost.json") {
			continue
		}
		names = append(names, filepath.Join(chunksDirName, name))
	}
	return names
}

// countRecordedFile counts one recorded cost file for month into sum and
// records its in-month identity in meta. Files without a parseable record
// contribute nothing.
func countRecordedFile(bundleDir, name, month string, sum *big.Rat, known, unknown *int, meta *recordedMeta) {
	raw := openBundleFile(bundleDir, name)
	if raw == nil {
		return
	}
	doc, err := jq.DecodeOrdered(raw)
	if err != nil {
		return
	}
	for _, rec := range sliceDoc(doc) {
		processCostRecord(rec, month, sum, known, unknown, meta)
	}
}

// processCostRecord counts one cost record when its timestamp falls in
// month and updates meta for that in-month record. Legacy records without
// "at" name no month and are excluded.
func processCostRecord(rec any, month string, sum *big.Rat, known, unknown *int, meta *recordedMeta) {
	obj, ok := rec.(*jq.Obj)
	if !ok {
		return
	}
	at, _ := obj.Get("at")
	recordMonth, ok := monthOf(at)
	if !ok {
		return
	}
	if recordMonth != month {
		return
	}
	atTime, _ := atTimeOf(at)
	meta.note(stringFieldOf(obj, "execution_id"), atTime)
	est, _ := obj.Get("est_usd")
	if rat, ok := ratOfJSONNumber(est); ok {
		sum.Add(sum, rat)
		*known++
		return
	}
	*unknown++
}

// aggregateLedger counts a bundle's budget.json receipt ledger for month.
// Repeated authorizations of one run (a --print plus a real launch, a
// re-run) share an execution_id — or a projection_sha256 when no
// execution_id was recorded — and count once, so the ledger cannot
// double-count a retried authorization. Rows already represented by
// in-month recorded cost are suppressed via meta.
func aggregateLedger(bundleDir, month string, sum *big.Rat, known, unknown *int, meta *recordedMeta) {
	raw := openBundleFile(bundleDir, "budget.json")
	if raw == nil {
		return
	}
	doc, err := jq.DecodeOrdered(raw)
	if err != nil {
		return
	}
	seen := map[string]bool{}
	for _, receipt := range sliceDoc(doc) {
		countLedgerReceipt(receipt, month, seen, sum, known, unknown, meta)
	}
}

func countLedgerReceipt(receipt any, month string, seen map[string]bool, sum *big.Rat, known, unknown *int, meta *recordedMeta) {
	obj, ok := receipt.(*jq.Obj)
	if !ok {
		return
	}
	at, _ := obj.Get("at")
	if !isInMonth(at, month) {
		return
	}
	if isLedgerSuppressed(obj, at, meta) {
		return
	}
	if isDuplicate(obj, seen) {
		return
	}
	countLedgerValue(obj, sum, known, unknown)
}

func isInMonth(at any, month string) bool { return sameMonth(at, month) }

func isDuplicate(obj *jq.Obj, seen map[string]bool) bool {
	key := budgetDedupeKey(obj)
	if key == "" {
		return false
	}
	if seen[key] {
		return true
	}
	seen[key] = true
	return false
}

// isLedgerSuppressed reports whether one in-month ledger row is the pre-run
// projection of already-counted in-month recorded cost: a row at or before
// the latest recorded timestamp its own execution identity carries is
// covered. A strictly later row is not, so a resumed run's new projection
// after its earlier recorded cost stays visible to every concurrent scan.
// Unidentified recorded cost covers any row, whichever identity the row
// carries, because legacy cost.json omits the id its budget.json row holds.
// A ledger timestamp that cannot be ordered against the recorded cost is
// suppressed: never double-count what cannot be compared.
func isLedgerSuppressed(obj *jq.Obj, at any, meta *recordedMeta) bool {
	ledgerAt, ok := atTimeOf(at)
	if !ok {
		return true
	}
	if recMaxAt, ok := meta.maxAt[stringFieldOf(obj, "execution_id")]; ok {
		if !ledgerAt.After(recMaxAt) {
			return true
		}
	}
	unidentified, ok := meta.maxAt[unidentifiedExecID]
	return ok && !ledgerAt.After(unidentified)
}

// countLedgerValue counts one in-month ledger row. A fully priced projection
// writes projected_usd. A partially priced one writes projected_usd null and
// keeps its known lower bound in projection.known_subtotal_usd — the priced
// phases are committed spend, so that subtotal is summed, while the row still
// counts as unknown because its unpriced phases leave the true cost unknown.
// Dropping the subtotal would hide committed spend from the monthly ceiling:
// a row carrying $2 known plus an unpriced phase let a second bundle
// authorize against the same month-to-date. A row with neither adds nothing
// and stays unknown.
func countLedgerValue(obj *jq.Obj, sum *big.Rat, known, unknown *int) {
	projected, _ := obj.Get("projected_usd")
	if rat, ok := ratOfJSONNumber(projected); ok {
		sum.Add(sum, rat)
		*known++
		return
	}
	if rat, ok := knownSubtotalOf(obj); ok {
		sum.Add(sum, rat)
	}
	*unknown++
}

// knownSubtotalOf reads the known lower bound a partially priced ledger row
// keeps in its projection object. The projection is written by the run layer,
// so a non-object or malformed subtotal reads as absent: unknown, never a
// guessed amount.
func knownSubtotalOf(obj *jq.Obj) (*big.Rat, bool) {
	projection, _ := obj.Get("projection")
	projObj, ok := projection.(*jq.Obj)
	if !ok {
		return nil, false
	}
	subtotal, _ := projObj.Get("known_subtotal_usd")
	return ratOfJSONNumber(subtotal)
}

// budgetDedupeKey is the authorization identity one ledger row carries:
// its execution_id, falling back to its projection_sha256 when the row
// recorded no execution. Rows with neither have no identity and every
// one of them counts.
func budgetDedupeKey(obj *jq.Obj) string {
	if exec := stringFieldOf(obj, "execution_id"); exec != "" {
		return exec
	}
	return stringFieldOf(obj, "projection_sha256")
}

func stringFieldOf(obj *jq.Obj, key string) string {
	value, _ := obj.Get(key)
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}

func sliceDoc(doc any) []any {
	if arr, ok := doc.([]any); ok {
		return arr
	}
	return []any{doc}
}

// openBundleFile reads one bundle file bounded to jq.MaxCostBytes,
// refusing anything that is not a regular file after a non-blocking open
// (openBundleFile dispatches to the platform open below): a FIFO in a
// bundle can never hang the scan. An unreadable or non-regular file reads
// nil — it contributes nothing.
func openBundleFile(bundleDir, name string) []byte {
	root, err := os.OpenRoot(bundleDir)
	if err != nil {
		return nil
	}
	defer func() { _ = root.Close() }()
	file, err := openNoBlock(root, name)
	if err != nil {
		return nil
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil
	}
	raw, err := jq.Capped(file, jq.MaxCostBytes)
	if err != nil {
		return nil
	}
	return raw
}

func atString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	// jq may have decoded "at" as a string already; non-string means no timestamp.
	return ""
}

// monthOf parses a timestamp and returns its UTC calendar month. The
// parse comes first — never a string-prefix match — so a timezone-offset
// timestamp is bucketed by its UTC instant: 2026-06-01T02:00:00+05:00
// is May in UTC, not June. Unparseable timestamps read false: unknown,
// never mis-bucketed.
func monthOf(at any) (string, bool) {
	atStr := atString(at)
	if atStr == "" {
		return "", false
	}
	if t, err := time.Parse(time.RFC3339Nano, atStr); err == nil {
		return MonthFor(t), true
	}
	if t, err := time.Parse("2006-01-02", atStr); err == nil {
		return MonthFor(t), true
	}
	return "", false
}

// atTimeOf parses a timestamp to its UTC instant. It reports false for
// unparseable timestamps, so suppression comparisons never guess.
func atTimeOf(at any) (time.Time, bool) {
	atStr := atString(at)
	if atStr == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339Nano, atStr); err == nil {
		return t.UTC(), true
	}
	if t, err := time.Parse("2006-01-02", atStr); err == nil {
		return t.UTC(), true
	}
	return time.Time{}, false
}

// sameMonth reports whether the timestamp's UTC calendar month is month.
func sameMonth(at any, month string) bool {
	recordMonth, ok := monthOf(at)
	return ok && month != "" && recordMonth == month
}

// ratOfJSONNumber reads an exact nonnegative JSON number. String-encoded
// numbers ("7") are unknown — strict JSON numbers only — and any
// magnitude or exponent beyond the budget is unknown rather than
// allocating an unbounded rational.
func ratOfJSONNumber(v any) (*big.Rat, bool) {
	lit := literalOf(v)
	if !validNumberLiteral(lit) {
		return nil, false
	}
	rat, ok := new(big.Rat).SetString(lit)
	if !ok || rat.Sign() < 0 {
		return nil, false
	}
	return rat, true
}

func validNumberLiteral(lit string) bool {
	if lit == "" || lit == "null" || len(lit) > maxNumberLiteralLen {
		return false
	}
	for i := range len(lit) {
		if lit[i] != 'e' && lit[i] != 'E' {
			continue
		}
		exp, err := strconv.Atoi(lit[i+1:])
		return err == nil && exp >= -maxNumberExpAbs && exp <= maxNumberExpAbs
	}
	return true
}

func literalOf(v any) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case json.Number:
		return t.String()
	case jq.Number:
		return t.String()
	case float64:
		return strconv.FormatFloat(t, 'f', -1, numberBits)
	case float32:
		return strconv.FormatFloat(float64(t), 'f', -1, numberBits)
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, mtdDecimalBase)
	default:
		return ""
	}
}

// FormatRat renders an exact decimal: the full integer when whole,
// otherwise the shortest scale the denominator admits (falling back to
// six places). Zero renders "0".
func FormatRat(rat *big.Rat) string {
	if rat.Sign() == 0 {
		return "0"
	}
	if rat.IsInt() {
		return rat.Num().String()
	}
	if scale, ok := decimalScale(rat.Denom()); ok {
		return rat.FloatString(scale)
	}
	return rat.FloatString(mtdFallbackDP)
}

func decimalScale(den *big.Int) (int, bool) {
	rest := new(big.Int).Set(den)
	twos, fives := 0, 0
	two, five := big.NewInt(mtdBigTwo), big.NewInt(mtdBigFive)
	zero := big.NewInt(0)
	one := big.NewInt(1)
	for new(big.Int).Rem(rest, two).Cmp(zero) == 0 {
		rest.Div(rest, two)
		twos++
	}
	for new(big.Int).Rem(rest, five).Cmp(zero) == 0 {
		rest.Div(rest, five)
		fives++
	}
	if rest.Cmp(one) != 0 {
		return 0, false
	}
	if twos > fives {
		return twos, true
	}
	return fives, true
}
