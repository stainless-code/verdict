package provenance

import (
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/overrides"
)

// FooterOpts carries everything render_footer needs after the record flow
// resolved knobs, fragments, and bundle values.
type FooterOpts struct {
	Overrides              overrides.Set
	Dir                    string
	PublishFooter          string
	PublishAttribution     string
	PublishAttributionText string
	PublishCopyright       string
	PublishLogo            string
	PublishLogoURL         string
	Root                   string
	ReviewModel            string
	AttributionTextSet     bool
}

const placeholderBase = "Reviewed with [**Verdict {version}**](" + core.DefaultProductURL + ")"

// baseAttribution renders the P1b default sentence before substitution.
func baseAttribution(root string) string {
	withVersion := strings.ReplaceAll(placeholderBase, "{version}", ToolVersion(root))
	return strings.ReplaceAll(withVersion, "{product_url}", core.DefaultProductURL)
}

// RenderFooter renders one merged block at the body end, or ""
// when publish.footer is off.
func RenderFooter(o *FooterOpts) (string, error) {
	if o.PublishFooter != core.FlagOn {
		return "", nil
	}
	vals := footerValues(o)
	attr := footerAttribution(o, &vals)
	order, err := o.Overrides.FooterOrder()
	if err != nil {
		return "", passRefusal(err)
	}
	receipt := footerReceipt(o, order, vals.Tier, vals.Cost)
	var sb strings.Builder
	sb.WriteString("---\n\n")
	sb.WriteString(core.FooterDivider + newline)
	sb.WriteString(attributionLine(o, attr, receipt) + newline)
	if o.PublishCopyright != "" {
		sb.WriteString("> " + o.PublishCopyright + newline)
	}
	out := sb.String()
	return strings.TrimSuffix(out, newline), nil
}

func footerValues(o *FooterOpts) overrides.Values {
	tier, cost := Spend(o.Dir)
	return overrides.Values{
		Version:         ToolVersion(o.Root),
		Tier:            tier,
		ProductURL:      core.DefaultProductURL,
		Cost:            cost,
		FilesReviewed:   FileCount(o.Dir),
		HeadShort:       HeadShort(o.Dir),
		Model:           OverrideModel(o.Dir, o.ReviewModel),
		BaseAttribution: baseAttribution(o.Root),
	}
}

// footerAttribution resolves the attribution sentence: per-run flag wins,
// then the fragment, then settings/env/default.
func footerAttribution(o *FooterOpts, vals *overrides.Values) string {
	attr := o.PublishAttributionText
	if attr == "" {
		attr = placeholderBase
	}
	if frag, ref := o.Overrides.AttributionText(); ref == nil && frag != "" && !o.AttributionTextSet {
		// A present fragment wins over settings/env/default — but a per-run
		// flag still wins over the fragment. (Refusals surface in
		// validate_overrides, not here.)
		attr = frag
	}
	return overrides.Substitute(attr, vals)
}

func footerReceipt(o *FooterOpts, order []string, tier, cost string) string {
	receipt := ""
	for _, seg := range order {
		segText := receiptSegment(o, seg, tier, cost)
		if segText == "" {
			continue
		}
		if receipt == "" {
			receipt = segText
		} else {
			receipt += " · " + segText
		}
	}
	if receipt == "" {
		receipt = unrecorded
	}
	return receipt
}

func receiptSegment(o *FooterOpts, seg, tier, cost string) string {
	switch seg {
	case "tier":
		return tier + " tier"
	case "cost":
		return "cost " + cost
	case "files":
		return FileCount(o.Dir)
	case "link":
		return ReviewLink(o.Dir)
	default:
		return ""
	}
}

func attributionLine(o *FooterOpts, attr, receipt string) string {
	if o.PublishAttribution == core.AttributionOff || attr == "" {
		return "> " + receipt
	}
	logo := ""
	if o.PublishLogo == core.FlagOn && o.PublishLogoURL != "" {
		logo = `<a href="` + core.DefaultProductURL + `"><img src="` + o.PublishLogoURL + `" alt="Verdict" width="18" height="18"></a> `
	}
	maker := ""
	if o.PublishAttribution == core.AttributionFull {
		maker = " by [" + core.DefaultMakerName + "](" + core.DefaultMakerURL + ")"
	}
	return "> " + logo + attr + maker + " · " + receipt
}

// passRefusal returns a sibling package's typed refusal unchanged: refusal
// context lives in Code/Message (rendered at the edge via [errors.As]), so the
// wrapcheck add-context rule is waived here by design.
func passRefusal(err error) error { return err }

// StripFooter removes a previously rendered machine
// footer (divider line through body end) is cut so re-rendering the same
// body is byte-identical. Only the machine marker is stripped.
func StripFooter(body string) string {
	lines := strings.Split(body, "\n")
	kept := stripRendered(lines)
	kept = trimTrailingBlanks(kept)
	if len(kept) > 0 && kept[len(kept)-1] == "---" {
		kept = kept[:len(kept)-1]
	}
	return strings.Join(trimTrailingBlanks(kept), "\n")
}

func stripRendered(lines []string) []string {
	var kept []string
	cut := false
	for _, ln := range lines {
		if ln == core.FooterDivider {
			cut = true
			continue
		}
		if cut {
			continue
		}
		kept = append(kept, ln)
	}
	return kept
}

func trimTrailingBlanks(lines []string) []string {
	for len(lines) > 0 && strings.Trim(lines[len(lines)-1], " \t\r\n\v\f") == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

const newline = "\n"
