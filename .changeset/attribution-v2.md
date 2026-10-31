---
"@stainless-code/verdict": minor
---

Attribution v2: the footer default is now a linked, leveled, logo-bearing line — a product-linked 18px logo (defaulting to the tracked `apps/docs/public/logo.svg` raw asset) plus `Reviewed with [**Verdict {version}**](https://stainless-code.com/verdict)` with the version inline space-separated, followed by the receipt fields. `publish.attribution` migrates from the day-old P1 bool to an `off`/`product`/`full` enum (file bools refuse with the mapping named: `"off"` for false, `"product"` for true; `full` appends `by [Stainless Code](https://stainless-code.com)`). New `publish.logo` (default true) and `publish.logo_url` knobs (empty reads text-only, never a broken image); a `<!-- verdict-footer -->` machine divider separates content from footer with strip-and-replace re-rendering; `attribution_text` gains the `{version}`/`{tier}`/`{product_url}` inventory with unknown placeholders refused.
