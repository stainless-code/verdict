package recordcore

import (
	"fmt"
	"unicode/utf8"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/kb"
	"github.com/stainless-code/verdict/internal/noblock"
	"github.com/stainless-code/verdict/internal/readcommon"
)

func (st *state) applyVerification() error {
	raw, err := readAgentFile(st.dir + "/verification.json")
	if err != nil {
		if ref := agentRefusal(err); ref != nil {
			return ref
		}
		return nil
	}
	for _, item := range decodeVerifyClaims(raw) {
		st.applyVerificationClaim(item)
	}
	return nil
}

func decodeVerifyClaims(raw []byte) []*jq.Obj {
	doc, err := jq.DecodeOrdered(raw)
	if err != nil {
		return nil
	}
	items, ok := doc.([]any)
	if !ok {
		return nil
	}
	var out []*jq.Obj
	for _, item := range items {
		if v, ok := item.(*jq.Obj); ok {
			out = append(out, v)
		}
	}
	return out
}

func (st *state) applyVerificationClaim(v *jq.Obj) {
	vsigRaw, _ := v.Get("sig")
	vsig := jq.R(jq.Alt(vsigRaw, ""))
	if vsig == "" {
		return
	}
	verdictRaw, _ := v.Get("verdict")
	vrhRaw, _ := v.Get("resultHead")
	vcmdRaw, _ := v.Get("verifyCmd")
	claim := verifyClaim{
		verdict: jq.R(jq.Alt(verdictRaw, "")),
		vrh:     jq.R(jq.Alt(vrhRaw, "")),
		vcmd:    jq.R(jq.Alt(vcmdRaw, "")),
	}
	full, code := resolveSig(st.entries, vsig)
	switch code {
	case sigNone:
		st.warn(fmt.Sprintf("verification claim %s ignored (not in store)", readcommon.Prefix(vsig)))
		return
	case sigMany:
		st.warn(fmt.Sprintf("verification claim %s ignored (ambiguous prefix)", readcommon.Prefix(vsig)))
		return
	}
	st.applyVerdict(findEntry(st.entries, full), full, claim)
}

type verifyClaim struct {
	verdict string
	vrh     string
	vcmd    string
}

func (st *state) applyVerdict(target *jq.Obj, full string, claim verifyClaim) {
	shaOK := len(claim.vrh) >= shortSHALen && len(st.headFull) >= shortSHALen &&
		(st.headFull == claim.vrh || hasPrefix(st.headFull, claim.vrh) || hasPrefix(claim.vrh, st.headFull))
	switch {
	case claim.verdict == "VERIFIED" && shaOK && claim.vcmd != "":
		// The claim is attestation; only the receipt verify wrote is proof.
		if gap := verifyReceiptGap(st.dir, st.headFull); gap != "" {
			st.warn(fmt.Sprintf(msgClaimUnbacked, readcommon.Prefix(full), gap, readcommon.ShortSHA(st.headFull)))
			st.recordRevalidation(target, claim)
			return
		}
		st.applyVerified(target, full, claim.vcmd)
	case claim.verdict == "NOT VERIFIED":
		st.applyNotVerified(target, claim.vcmd)
	default:
		st.recordRevalidation(target, claim)
	}
}

func (st *state) applyVerified(target *jq.Obj, full, vcmd string) {
	if stt, _ := target.Get(fieldStatus); stt != outcomeAddressed {
		target.Set(fieldStatus, outcomeAddressed)
		target.Set(fieldHistory, appendHistory(target, jq.NewObj(
			fieldAt, st.now, fieldEvent, outcomeAddressed, fieldActor, revalidatorForced,
			fieldNote, vcmd, fieldSHA, st.headShort,
		)))
	}
	if !st.dryRun {
		st.kb.ApplyCredit(st.entries, full, outcomeAddressed, revalidatorForced, st.cmd, st.now, st.controls.Has(kb.File))
		stampCredit(st, target)
	}
}

func (st *state) applyNotVerified(target *jq.Obj, vcmd string) {
	event := "revalidation"
	if stt, _ := target.Get(fieldStatus); stt == outcomeAddressed {
		target.Set(fieldStatus, statusOpen)
		event = "reopened"
	}
	target.Set(fieldHistory, appendHistory(target, jq.NewObj(
		fieldAt, st.now, fieldEvent, event, fieldActor, revalidatorForced,
		fieldNote, vcmd, fieldSHA, st.headShort,
	)))
}

func (st *state) recordRevalidation(target *jq.Obj, claim verifyClaim) {
	note := claim.verdict
	if claim.vcmd != "" {
		note = claim.verdict + ": " + claim.vcmd
	}
	target.Set(fieldHistory, appendHistory(target, jq.NewObj(
		fieldAt, st.now, fieldEvent, "revalidation", fieldActor, revalidatorForced,
		fieldNote, note, fieldSHA, st.headShort,
	)))
}

func (st *state) applyTriage() error {
	for _, v := range triageItems(st.review) {
		if err := st.applyTriageClaim(v); err != nil {
			return err
		}
	}
	return nil
}

func triageItems(review *jq.Obj) []*jq.Obj {
	tv, ok := review.Get(fieldTriage)
	if !ok {
		return nil
	}
	arr, isArr := tv.([]any)
	if !isArr {
		return nil
	}
	var out []*jq.Obj
	for _, item := range arr {
		if v, ok := item.(*jq.Obj); ok {
			out = append(out, v)
		}
	}
	return out
}

type triageClaim struct {
	sig    string
	status string
	reason string
}

func (st *state) applyTriageClaim(v *jq.Obj) error {
	tsigRaw, _ := v.Get("sig")
	tstRaw, _ := v.Get(fieldStatus)
	tratRaw, _ := v.Get("rationale")
	claim := triageClaim{
		sig:    jq.R(jq.Alt(tsigRaw, "")),
		status: jq.R(jq.Alt(tstRaw, "")),
		reason: jq.R(jq.Alt(tratRaw, "")),
	}
	if claim.sig == "" {
		return nil
	}
	if !isTriageStatus(claim.status) {
		return &core.RefusalError{Code: codeBadTriageStatus, Message: fmt.Sprintf(msgBadTriage, claim.status)}
	}
	full, code := resolveSig(st.entries, claim.sig)
	switch code {
	case sigNone:
		st.warn(fmt.Sprintf("triage claim %s ignored (not in store)", readcommon.Prefix(claim.sig)))
		return nil
	case sigMany:
		st.warn("triage claim " + readcommon.Prefix(claim.sig) + " ignored (ambiguous prefix)")
		return nil
	}
	if utf8.RuneCountInString(claim.reason) < minRationaleRunes {
		st.warn("triage claim without rationale, kept prior status: " + readcommon.Prefix(claim.sig))
		return nil
	}
	st.applyTriageFlip(findEntry(st.entries, full), full, claim)
	return nil
}

func isTriageStatus(s string) bool {
	switch s {
	case statusOpen, outcomeAddressed, exampleWontFix, exampleFalsePositive, "deferred":
		return true
	default:
		return false
	}
}

// applyTriageFlip flips one resolved triage claim and credits addressed ones.
func (st *state) applyTriageFlip(target *jq.Obj, full string, claim triageClaim) {
	target.Set(fieldStatus, claim.status)
	target.Set("rationale", claim.reason)
	target.Set("triagedAt", st.now)
	target.Set("triagedBy", "agent")
	target.Set(fieldHistory, appendHistory(target, jq.NewObj(
		fieldAt, st.now, fieldEvent, "triaged", fieldActor, "agent",
		fieldNote, claim.reason, fieldSHA, st.headShort,
	)))
	switch claim.status {
	case outcomeAddressed, "false-positive":
		if st.dryRun {
			return
		}
		st.kb.ApplyCredit(st.entries, full, claim.status, "agent", st.cmd, st.now, st.controls.Has(kb.File))
		stampCredit(st, target)
	}
}

func stampCredit(st *state, target *jq.Obj) {
	if len(st.kb.Credited) == 0 {
		return
	}
	histRaw, _ := target.Get(fieldHistory)
	hist, ok := histRaw.([]any)
	if !ok || len(hist) == 0 {
		return
	}
	last, ok := hist[len(hist)-1].(*jq.Obj)
	if !ok {
		return
	}
	credited := make([]any, 0, len(st.kb.Credited))
	for _, cr := range st.kb.Credited {
		if m, ok := cr.(map[string]any); ok {
			r := jq.StrOf(m["rule"])
			to := jq.StrOf(m["to"])
			credited = append(credited, jq.NewObj("rule", r, "to", to))
		} else {
			credited = append(credited, cr)
		}
	}
	last.Set("kb_credit", credited)
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

// verifyReceiptGap names why patch.json's verify receipt does not prove the
// gated head, or returns "" when it does: an exit of 0, no timeout, and the
// verifiedHead verify probed equal to head. The bundle owns patch.json, so a
// receipt read here was written for this bundle and its root; the head is the
// one binding a receipt can go stale on.
func verifyReceiptGap(dir, head string) string {
	receipt, gap := readVerifyReceipt(dir)
	if gap != "" {
		return gap
	}
	if gap := receiptOutcomeGap(receipt); gap != "" {
		return gap
	}
	recorded, ok := getField(receipt, "verifiedHead").(string)
	if !ok || recorded == "" {
		return "the verify receipt records no HEAD"
	}
	if recorded != head {
		return "the verify receipt covers HEAD " + readcommon.ShortSHA(recorded)
	}
	return ""
}

// readVerifyReceipt reads patch.json and proves verify wrote an outcome into
// it; an agent-written patch.json without verifyExit is no receipt at all.
func readVerifyReceipt(dir string) (*jq.Obj, string) {
	raw, err := noblock.ReadFile(dir + "/patch.json")
	if err != nil {
		return nil, "no verify receipt: patch.json is missing or unreadable"
	}
	doc, err := jq.DecodeOrdered(raw)
	receipt, ok := doc.(*jq.Obj)
	if err != nil || !ok {
		return nil, "no verify receipt: patch.json is not a JSON object"
	}
	if getField(receipt, "verifyExit") == nil {
		return nil, "no verify receipt in patch.json"
	}
	return receipt, ""
}

// receiptOutcomeGap rejects a receipt whose run failed or timed out.
func receiptOutcomeGap(receipt *jq.Obj) string {
	if exit := jq.R(getField(receipt, "verifyExit")); exit != "0" {
		return "the verify receipt failed with exit=" + exit
	}
	if jq.R(jq.Alt(getField(receipt, "timedOut"), "")) == core.FlagOn {
		return "the verify receipt timed out"
	}
	return ""
}

// getField reads one key, nil when absent.
func getField(m *jq.Obj, key string) any {
	v, _ := m.Get(key)
	return v
}
