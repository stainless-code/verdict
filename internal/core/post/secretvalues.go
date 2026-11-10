package postcore

import (
	"fmt"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/envsecret"
	"github.com/stainless-code/verdict/internal/jq"
)

// codeSecretShaped is the typed refusal for a review whose text carries a
// credential value. A post cannot be unsent, so the refusal names the override
// instead of offering a repair.
const codeSecretShaped = "post-secret-shaped"

// AllowSecretValuesFlag is the override the refusal, the dry-run warning, the
// re-entry line, and the usage text name. It lives here because the refusal
// spells it, and a second spelling in the adapter would let the two drift.
const AllowSecretValuesFlag = "--allow-secret-values"

// maxSecretSites bounds how many located matches one line names; the rest ride
// a count, so a review full of keys still produces a readable warning.
const maxSecretSites = 5

// secretSite is one located secret-shaped value: where in the review the text
// sits, and which shapes it carries. It holds no matched bytes, so a warning or
// a refusal can say where the value is without republishing it.
type secretSite struct {
	where  string
	shapes []string
}

// reviewSecretSites walks the text post is about to send — the summary body and
// each finding's body and suggestion — and returns one entry per located value,
// in review order. It reads the review input rather than the rendered payload
// because it runs before the first host read, and every byte it reads is
// operator or agent text that a render would only copy: the marker, the footer,
// the diagram, and the split notes are verdict's own and carry no credential.
func (st *state) reviewSecretSites() []secretSite {
	var sites []secretSite
	if shapes := envsecret.SecretValueShapes(st.body); len(shapes) > 0 {
		sites = append(sites, secretSite{where: "summary body", shapes: shapes})
	}
	for _, c := range reviewComments(st.review) {
		sites = append(sites, commentSecretSites(c.index, c.obj)...)
	}
	return sites
}

// reviewComment is one comment object with the review-array index an operator
// counts in review.json.
type reviewComment struct {
	obj   *jq.Obj
	index int
}

// commentSecretSites locates the values one review comment carries. Its body
// and its suggestion are both posted verbatim, so both are scanned; the
// location is the finding's 1-based index, because review input carries no sig
// (sigs are minted by the merge, which runs after the live-head read this scan
// deliberately precedes).
func commentSecretSites(index int, c *jq.Obj) []secretSite {
	where := fmt.Sprintf("finding %d", index)
	var sites []secretSite
	for _, field := range []string{fieldBody, fieldSuggestion} {
		raw, ok := c.Get(field)
		if !ok {
			continue
		}
		if shapes := envsecret.SecretValueShapes(jq.R(jq.Alt(raw, ""))); len(shapes) > 0 {
			sites = append(sites, secretSite{where: where, shapes: shapes})
		}
	}
	return sites
}

// reviewComments reads review.json's comments array as objects, skipping
// entries that are not objects while keeping the array index: the review-input
// checker already refused or warned about those, and a scan that invented one
// would report a site the merge never posts.
func reviewComments(review *jq.Obj) []reviewComment {
	if review == nil {
		return nil
	}
	raw, _ := review.Get(fieldComments)
	var out []reviewComment
	for i, c := range jq.Elements(raw) {
		if obj, ok := c.(*jq.Obj); ok && obj != nil {
			out = append(out, reviewComment{obj: obj, index: i + 1})
		}
	}
	return out
}

// secretScanGate decides what to do with the values a review carries: refuse
// before the first host call, warn and continue on a dry run, or record the
// operator's override and post. The scan runs before the live-head read, so a
// refusal makes zero host calls — nothing a typo in review.json would otherwise
// have asked the host for.
func (st *state) secretScanGate() *core.RefusalError {
	sites := st.reviewSecretSites()
	if len(sites) == 0 {
		return nil
	}
	located := secretSiteList(sites)
	if st.flags.AllowSecretValues {
		st.secretShapes = secretShapeNames(sites)
		st.warn(fmt.Sprintf(msgSecretAllowed, AllowSecretValuesFlag, located))
		return nil
	}
	if st.flags.DryRun {
		st.warn(fmt.Sprintf(msgSecretDryRun, located, AllowSecretValuesFlag))
		return nil
	}
	return &core.RefusalError{
		Code:    codeSecretShaped,
		Message: fmt.Sprintf(msgSecretRefusal, located, AllowSecretValuesFlag),
	}
}

// secretSiteList renders the located matches, capped so one line stays legible.
func secretSiteList(sites []secretSite) string {
	shown := sites
	more := ""
	if len(sites) > maxSecretSites {
		shown = sites[:maxSecretSites]
		more = fmt.Sprintf(", and %d more", len(sites)-maxSecretSites)
	}
	parts := make([]string, 0, len(shown))
	for _, site := range shown {
		parts = append(parts, site.where+" ("+strings.Join(site.shapes, ", ")+")")
	}
	return strings.Join(parts, ", ") + more
}

// secretShapeNames folds the sites into the unique shape names a receipt
// records, in first-seen order: the sites are walked in review order and each
// site's shapes arrive in table order, so two reviews carrying the same shapes
// record the same list.
func secretShapeNames(sites []secretSite) []string {
	var out []string
	for _, site := range sites {
		for _, name := range site.shapes {
			if !slices.Contains(out, name) {
				out = append(out, name)
			}
		}
	}
	return out
}
