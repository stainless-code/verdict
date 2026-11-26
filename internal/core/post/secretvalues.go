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

// CodeSecretShaped is the code drain reports for a reply body carrying a
// credential. Both verbs read the same scan and the same refusal code.
const CodeSecretShaped = codeSecretShaped

// BodySecretShapes runs the post secret scan over one outbound reply body.
func BodySecretShapes(body string) []string {
	return envsecret.SecretValueShapes(body)
}

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

func (st *state) reviewSecretSites() []secretSite {
	if st.review == nil {
		return nil
	}
	var sites []secretSite
	sites = appendSecretSite(sites, "summary body", reviewStringField(st.review, "summary"))
	sites = appendSecretSite(sites, "security_summary", reviewStringField(st.review, "security_summary"))
	sites = appendSecretSite(sites, "risk.rationale", reviewRiskRationale(st.review))
	for _, c := range reviewComments(st.review) {
		sites = append(sites, commentSecretSites(c.index, c.obj)...)
	}
	sites = append(sites, specCheckSecretSites(st.review)...)
	sites = append(sites, receiptSecretSites(st.review)...)
	return sites
}

func appendSecretSite(sites []secretSite, where, text string) []secretSite {
	if text == "" {
		return sites
	}
	if shapes := envsecret.SecretValueShapes(text); len(shapes) > 0 {
		return append(sites, secretSite{where: where, shapes: shapes})
	}
	return sites
}

func reviewStringField(obj *jq.Obj, key string) string {
	raw, ok := obj.Get(key)
	if !ok || raw == nil {
		return ""
	}
	return jq.R(raw)
}

func reviewRiskRationale(review *jq.Obj) string {
	raw, ok := review.Get("risk")
	if !ok {
		return ""
	}
	riskObj, ok := raw.(*jq.Obj)
	if !ok || riskObj == nil {
		return ""
	}
	return reviewStringField(riskObj, "rationale")
}

func specCheckSecretSites(review *jq.Obj) []secretSite {
	raw, _ := review.Get("spec_checks")
	var sites []secretSite
	for i, item := range jq.Elements(raw) {
		if obj, ok := item.(*jq.Obj); ok && obj != nil {
			sites = appendSpecCheckSites(sites, i+1, obj)
		}
	}
	return sites
}

func appendSpecCheckSites(sites []secretSite, index int, obj *jq.Obj) []secretSite {
	where := fmt.Sprintf("spec_check %d", index)
	sites = appendSecretSite(sites, where, reviewStringField(obj, "item"))
	// note is free text on the same row: an operator pasting a credential into
	// a justification must hit the same refusal the item does.
	sites = appendSecretSite(sites, where, reviewStringField(obj, "note"))
	if ev, ok := obj.Get("evidence"); ok {
		if evObj, ok := ev.(*jq.Obj); ok && evObj != nil {
			sites = appendSecretSite(sites, where, reviewStringField(evObj, fieldPath))
			sites = appendSecretSite(sites, where, reviewStringField(evObj, "quote"))
		}
	}
	return sites
}

func receiptSecretSites(review *jq.Obj) []secretSite {
	raw, _ := review.Get("receipts")
	var sites []secretSite
	for i, item := range jq.Elements(raw) {
		obj, ok := item.(*jq.Obj)
		if !ok || obj == nil {
			continue
		}
		where := fmt.Sprintf("receipt %d", i+1)
		sites = appendSecretSite(sites, where, reviewStringField(obj, "hunk"))
		sites = appendSecretSite(sites, where, reviewStringField(obj, "quote"))
	}
	return sites
}

// reviewComment is one comment object with the review-array index an operator
// counts in review.json.
type reviewComment struct {
	obj   *jq.Obj
	index int
}

func commentSecretSites(index int, c *jq.Obj) []secretSite {
	where := fmt.Sprintf("finding %d", index)
	var sites []secretSite
	for _, field := range []string{fieldPath, fieldBody, "claim", "quote", fieldSuggestion} {
		sites = appendSecretSite(sites, where, reviewStringField(c, field))
	}
	if rawBrief, ok := c.Get("brief"); ok {
		if bObj, ok := rawBrief.(*jq.Obj); ok && bObj != nil {
			sites = appendBriefSecretSites(sites, where, bObj)
		}
	}
	return sites
}

func appendBriefSecretSites(sites []secretSite, where string, bObj *jq.Obj) []secretSite {
	sites = appendBriefSitePathSecrets(sites, where, bObj)
	sites = appendSecretSite(sites, where, reviewStringField(bObj, "outcome"))
	sites = appendSecretSite(sites, where, reviewStringField(bObj, "approach"))
	if qs, ok := bObj.Get("questions"); ok {
		for _, q := range jq.Elements(qs) {
			sites = appendSecretSite(sites, where, jq.R(jq.Alt(q, "")))
		}
	}
	return sites
}

func appendBriefSitePathSecrets(sites []secretSite, where string, bObj *jq.Obj) []secretSite {
	rawSites, ok := bObj.Get("sites")
	if !ok {
		return sites
	}
	for _, item := range jq.Elements(rawSites) {
		if sObj, ok := item.(*jq.Obj); ok && sObj != nil {
			sites = appendSecretSite(sites, where, reviewStringField(sObj, fieldPath))
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
	raw, _ := review.Get("findings")
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
	return st.secretGate(st.reviewSecretSites())
}

// settledSecretGate scans the body the merge settled, which carries text the
// raw review never held: a stored brief survives a same-signature review that
// omits it, and the renderer publishes it. Only shapes the raw scan did not
// already judge count, so one value is never warned about twice.
func (st *state) settledSecretGate() *core.RefusalError {
	body := appendSecretSite(nil, "review body", st.body)
	return st.secretGate(unjudgedSecretSites(body, st.judgedShapes))
}

func unjudgedSecretSites(sites []secretSite, judged []string) []secretSite {
	var out []secretSite
	for _, site := range sites {
		fresh := unjudgedShapes(site.shapes, judged)
		if len(fresh) > 0 {
			out = append(out, secretSite{where: site.where, shapes: fresh})
		}
	}
	return out
}

func (st *state) secretGate(sites []secretSite) *core.RefusalError {
	if len(sites) == 0 {
		return nil
	}
	located := secretSiteList(sites)
	names := secretShapeNames(sites)
	st.judgedShapes = append(st.judgedShapes, unjudgedShapes(names, st.judgedShapes)...)
	if st.flags.AllowSecretValues {
		st.secretShapes = append(st.secretShapes, unjudgedShapes(names, st.secretShapes)...)
		st.warn(fmt.Sprintf(msgSecretAllowed, AllowSecretValuesFlag, located))
		return nil
	}
	if st.flags.DryRun {
		st.warn(fmt.Sprintf(msgSecretDryRun, located, AllowSecretValuesFlag))
		return nil
	}
	return &core.RefusalError{
		Code:    codeSecretShaped,
		Message: core.Msgf(msgSecretRefusal, located, AllowSecretValuesFlag),
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

func unjudgedShapes(names, judged []string) []string {
	var fresh []string
	for _, name := range names {
		if !slices.Contains(judged, name) {
			fresh = append(fresh, name)
		}
	}
	return fresh
}
