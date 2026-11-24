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
	KnownUSD string // exact decimal, "" when no known receipts in month
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
		if !errors.Is(err, fs.ErrNotExist) {
			m.Warning = "month-to-date scan unreadable — state directory: " + err.Error()
		}
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
	if known > 0 {
		m.KnownUSD = FormatRat(sum)
		m.KnownCount = known
	}
	m.UnknownCount = unknown
	return m
}

// aggregateBundle counts one bundle from a single authoritative source:
// recorded cost when the bundle carries month-identified recorded cost,
// else the budget.json ledger. Both sources never add together — one
// run's spend must not be counted twice.
func aggregateBundle(bundleDir, month string, sum *big.Rat, known, unknown *int) {
	if aggregateRecorded(bundleDir, month, sum, known, unknown) {
		return
	}
	aggregateLedger(bundleDir, month, sum, known, unknown)
}

// aggregateRecorded counts the bundle's recorded cost files — cost.json
// plus the chunk cost files — mirroring the report rollup's selection:
// manifest chunk names plus the chunk-*.cost.json glob, deduplicated so
// a chunk named by both is counted once. It reports whether any recorded
// record is month-identified (a parseable timestamp in any month): an
// unbucketable record names no month's spend, so it never suppresses
// the ledger fallback, and a bundle whose recorded cost is all in other
// months still counts this month's ledger rows.
func aggregateRecorded(bundleDir, month string, sum *big.Rat, known, unknown *int) bool {
	bucketable := false
	for _, name := range recordedCostNames(bundleDir) {
		if countRecordedFile(bundleDir, name, month, sum, known, unknown) {
			bucketable = true
		}
	}
	return bucketable
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

// countRecordedFile counts one recorded cost file and reports whether
// any of its records is month-identified.
func countRecordedFile(bundleDir, name, month string, sum *big.Rat, known, unknown *int) bool {
	raw := openBundleFile(bundleDir, name)
	if raw == nil {
		return false
	}
	doc, err := jq.DecodeOrdered(raw)
	if err != nil {
		return false
	}
	bucketable := false
	for _, rec := range sliceDoc(doc) {
		if processCostRecord(rec, month, sum, known, unknown) {
			bucketable = true
		}
	}
	return bucketable
}

// processCostRecord counts one cost record when its timestamp falls in
// month, and reports whether the record is month-identified at all.
// Legacy records without "at" name no month and are excluded.
func processCostRecord(rec any, month string, sum *big.Rat, known, unknown *int) bool {
	obj, ok := rec.(*jq.Obj)
	if !ok {
		return false
	}
	at, _ := obj.Get("at")
	recordMonth, ok := monthOf(at)
	if !ok {
		return false
	}
	if recordMonth != month {
		return true
	}
	est, _ := obj.Get("est_usd")
	if rat, ok := ratOfJSONNumber(est); ok {
		sum.Add(sum, rat)
		*known++
		return true
	}
	*unknown++
	return true
}

// aggregateLedger counts a bundle's budget.json receipt ledger.
// Repeated authorizations of one run (a --print plus a real launch, a
// re-run) share an execution_id — or a projection_sha256 when no
// execution_id was recorded — and count once, so the ledger cannot
// double-count a retried authorization.
func aggregateLedger(bundleDir, month string, sum *big.Rat, known, unknown *int) {
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
		countLedgerReceipt(receipt, month, seen, sum, known, unknown)
	}
}

func countLedgerReceipt(receipt any, month string, seen map[string]bool, sum *big.Rat, known, unknown *int) {
	obj, ok := receipt.(*jq.Obj)
	if !ok {
		return
	}
	at, _ := obj.Get("at")
	if !sameMonth(at, month) {
		return
	}
	if key := budgetDedupeKey(obj); key != "" {
		if seen[key] {
			return
		}
		seen[key] = true
	}
	countLedgerValue(obj, sum, known, unknown)
}

func countLedgerValue(obj *jq.Obj, sum *big.Rat, known, unknown *int) {
	projected, _ := obj.Get("projected_usd")
	if rat, ok := ratOfJSONNumber(projected); ok {
		sum.Add(sum, rat)
		*known++
		return
	}
	*unknown++
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
