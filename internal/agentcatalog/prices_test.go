package agentcatalog

import (
	"math/big"
	"strings"
	"testing"
	"time"
)

// TestPricesAreCitedPositiveRates proves every price row is usable as
// written: three positive rates, cached no dearer than fresh input, a parsed
// date, an https citation, and a unique model that PriceFor finds.
func TestPricesAreCitedPositiveRates(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for i := range prices {
		p := prices[i]
		checkRates(t, &p)
		checkProvenance(t, &p)
		if seen[p.Model] {
			t.Errorf("%s is priced twice", p.Model)
		}
		seen[p.Model] = true
		if got, ok := PriceFor(p.Model); !ok || got != p {
			t.Errorf("PriceFor(%s) = %+v, %v", p.Model, got, ok)
		}
	}
	if _, ok := PriceFor("gpt-99"); ok {
		t.Error("an unlisted model was priced")
	}
}

// checkRates proves one row's three rates are positive amounts, cached input
// no dearer than fresh input.
func checkRates(t *testing.T, p *Price) {
	t.Helper()
	input, okIn := positive(p.InputUSD)
	cached, okCached := positive(p.CachedInputUSD)
	_, okOut := positive(p.OutputUSD)
	if !okIn || !okCached || !okOut {
		t.Errorf("%s rates %q/%q/%q are not all positive amounts", p.Model, p.InputUSD, p.CachedInputUSD, p.OutputUSD)
		return
	}
	if cached.Cmp(input) > 0 {
		t.Errorf("%s cached input %s costs more than input %s", p.Model, p.CachedInputUSD, p.InputUSD)
	}
}

// checkProvenance proves one row names when and where it was read.
func checkProvenance(t *testing.T, p *Price) {
	t.Helper()
	if _, err := time.Parse(time.DateOnly, p.AsOf); err != nil {
		t.Errorf("%s AsOf %q is not a date", p.Model, p.AsOf)
	}
	if !strings.HasPrefix(p.Citation, "https://") {
		t.Errorf("%s citation %q is not a page", p.Model, p.Citation)
	}
}

func positive(lit string) (*big.Rat, bool) {
	rat, ok := new(big.Rat).SetString(lit)
	return rat, ok && rat.Sign() > 0
}
