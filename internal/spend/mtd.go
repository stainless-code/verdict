package spend

import (
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/stainless-code/verdict/internal/jq"
)

// MTD is a month-to-date spend aggregate computed only from local cost
// receipts under .verdict/state. Unknown cost stays unknown, never 0.
type MTD struct {
	Month        string
	KnownUSD     string // exact decimal, "" when no known receipts in month
	KnownCount   int
	UnknownCount int
}

const (
	mtdFloatBits   = 64
	mtdDecimalBase = 10
	mtdMonthLen    = 7
	mtdFallbackDP  = 6
	mtdBigTwo      = 2
	mtdBigFive     = 5
)

// MonthFor returns the UTC calendar month YYYY-MM for t.
func MonthFor(t time.Time) string {
	return t.UTC().Format("2006-01")
}

// AggregateMTD scans every bundle under <repoRoot>/.verdict/state for
// cost receipts and sums those whose timestamp falls in the month of now.
// Receipts are budget.json projected_usd and cost.json est_usd, the files
// report --cost reads. The scan never fails: unreadable files contribute
// nothing, and prior-month receipts are excluded.
func AggregateMTD(repoRoot string, now time.Time) MTD {
	month := MonthFor(now)
	m := MTD{Month: month}
	stateDir := filepath.Join(repoRoot, ".verdict", "state")
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		return m
	}
	sum := new(big.Rat)
	known := 0
	unknown := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		bundle := filepath.Join(stateDir, e.Name())
		readBudgetReceipts(bundle, month, sum, &known, &unknown)
		readCostReceipts(bundle, month, sum, &known, &unknown)
	}
	if known > 0 {
		m.KnownUSD = formatRat(sum)
		m.KnownCount = known
	}
	m.UnknownCount = unknown
	return m
}

func readBudgetReceipts(bundle, month string, sum *big.Rat, known, unknown *int) {
	raw := readBundleFile(bundle, "budget.json")
	if raw == nil {
		return
	}
	doc, err := jq.DecodeOrdered(raw)
	if err != nil {
		return
	}
	for _, receipt := range sliceDoc(doc) {
		processBudgetReceipt(receipt, month, sum, known, unknown)
	}
}

func processBudgetReceipt(receipt any, month string, sum *big.Rat, known, unknown *int) {
	obj, ok := receipt.(*jq.Obj)
	if !ok {
		return
	}
	at, _ := obj.Get("at")
	if !sameMonth(at, month) {
		return
	}
	projected, _ := obj.Get("projected_usd")
	if rat, ok := ratOfJSONNumber(projected); ok {
		sum.Add(sum, rat)
		*known++
		return
	}
	*unknown++
}

func readCostReceipts(bundle, month string, sum *big.Rat, known, unknown *int) {
	for _, path := range costPaths(bundle) {
		processCostFile(path, month, sum, known, unknown)
	}
}

func costPaths(bundle string) []string {
	paths := []string{filepath.Join(bundle, "cost.json")}
	return append(paths, chunkCostPaths(bundle)...)
}

func chunkCostPaths(bundle string) []string {
	chunksDir := filepath.Join(bundle, "chunks")
	ents, err := os.ReadDir(chunksDir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".cost.json") {
			continue
		}
		out = append(out, filepath.Join(chunksDir, e.Name()))
	}
	return out
}

func processCostFile(path, month string, sum *big.Rat, known, unknown *int) {
	raw := readBundleFilePath(path)
	if raw == nil {
		return
	}
	doc, err := jq.DecodeOrdered(raw)
	if err != nil {
		return
	}
	for _, rec := range sliceDoc(doc) {
		processCostRecord(rec, month, sum, known, unknown)
	}
}

// processCostRecord counts one cost.json record when its timestamp falls in
// month. Legacy records without "at" have no month and are excluded.
func processCostRecord(rec any, month string, sum *big.Rat, known, unknown *int) {
	obj, ok := rec.(*jq.Obj)
	if !ok {
		return
	}
	at, _ := obj.Get("at")
	if !sameMonth(at, month) {
		return
	}
	est, _ := obj.Get("est_usd")
	if rat, ok := ratOfJSONNumber(est); ok {
		sum.Add(sum, rat)
		*known++
		return
	}
	*unknown++
}

func sliceDoc(doc any) []any {
	if arr, ok := doc.([]any); ok {
		return arr
	}
	return []any{doc}
}

func readBundleFile(bundle, name string) []byte {
	return readBundleFilePath(filepath.Join(bundle, name))
}

func readBundleFilePath(path string) []byte {
	raw, err := jq.CappedFile(path, jq.MaxCostBytes)
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

func sameMonth(at any, month string) bool {
	atStr := atString(at)
	if atStr == "" || month == "" {
		return false
	}
	if monthPrefix(atStr) == month {
		return true
	}
	if t, err := time.Parse(time.RFC3339, atStr); err == nil {
		return MonthFor(t) == month
	}
	if t, err := time.Parse("2006-01-02", atStr); err == nil {
		return MonthFor(t) == month
	}
	return false
}

func monthPrefix(at string) string {
	if len(at) >= mtdMonthLen {
		return at[:mtdMonthLen]
	}
	return ""
}

func ratOfJSONNumber(v any) (*big.Rat, bool) {
	lit := literalOf(v)
	if lit == "" || lit == "null" {
		return nil, false
	}
	rat, ok := new(big.Rat).SetString(lit)
	if !ok || rat.Sign() < 0 {
		return nil, false
	}
	return rat, true
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
		return strconv.FormatFloat(t, 'f', -1, mtdFloatBits)
	case float32:
		return strconv.FormatFloat(float64(t), 'f', -1, mtdFloatBits)
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, mtdDecimalBase)
	case string:
		return t
	default:
		return ""
	}
}

func formatRat(rat *big.Rat) string {
	if rat.Sign() == 0 {
		return "0"
	}
	if rat.IsInt() {
		return rat.Num().String()
	}
	if scale, ok := decimalScaleForMTD(rat.Denom()); ok {
		return rat.FloatString(scale)
	}
	return rat.FloatString(mtdFallbackDP)
}

func decimalScaleForMTD(den *big.Int) (int, bool) {
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
