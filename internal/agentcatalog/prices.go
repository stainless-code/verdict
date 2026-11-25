package agentcatalog

// Price is one model's standard-tier API price in USD per million tokens, as
// literals, with the page it was read from and when: a token-only CLI's usage
// is priced from it and labeled tokens, never as the CLI's own cost.
type Price struct {
	Model          string
	InputUSD       string
	CachedInputUSD string
	OutputUSD      string
	AsOf           string
	Citation       string
}

// The pages the price table cites. OpenAI's pricing page lists the current
// models; the older Codex models are priced on their own model pages.
const (
	docOpenAIPricing = "https://developers.openai.com/api/docs/pricing"
	docOpenAIModels  = "https://developers.openai.com/api/docs/models/"
	pricesAsOf       = "2026-11-25"
)

// tier is one price point several models share.
type tier struct{ input, cached, output string }

// The price points the table's rows share, per million tokens.
var (
	tierGPT5     = tier{input: "1.25", cached: "0.125", output: "10.00"}
	tierGPT5Mini = tier{input: "0.25", cached: "0.025", output: "2.00"}
)

// row is one cited table entry at a price point.
func row(model string, t tier, citation string) Price {
	return Price{
		Model: model, InputUSD: t.input, CachedInputUSD: t.cached, OutputUSD: t.output,
		AsOf: pricesAsOf, Citation: citation,
	}
}

// prices is the dated token price table for the models Codex CLI runs:
// standard tier, short-context rates. A model missing here is never priced.
var prices = []Price{
	row("gpt-6.2-sol", tier{input: "2.00", cached: "0.10", output: tierGPT5.output}, docOpenAIPricing),
	row("gpt-6.2-astra", tier{input: "8.00", cached: "0.40", output: "40.00"}, docOpenAIPricing),
	row("gpt-6.1-terra", tier{input: "1.00", cached: "0.05", output: "5.00"}, docOpenAIPricing),
	row("gpt-6-luna", tier{input: "0.10", cached: "0.01", output: "0.50"}, docOpenAIPricing),
	row("gpt-5", tierGPT5, docOpenAIPricing),
	row("gpt-5-mini", tierGPT5Mini, docOpenAIPricing),
	row("gpt-5-codex", tierGPT5, docOpenAIModels+"gpt-5-codex"),
	row("gpt-5.1-codex", tierGPT5, docOpenAIModels+"gpt-5.1-codex"),
	row("gpt-5.1-codex-mini", tierGPT5Mini, docOpenAIModels+"gpt-5.1-codex-mini"),
	row("codex-mini-latest", tier{input: "1.50", cached: "0.375", output: "6.00"}, docOpenAIModels+"codex-mini-latest"),
}

// PriceFor is the table's price for one model, exact name only.
func PriceFor(model string) (Price, bool) {
	for _, p := range prices {
		if p.Model == model {
			return p, true
		}
	}
	return Price{}, false
}
