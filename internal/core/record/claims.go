package recordcore

import (
	"fmt"
	"unicode/utf8"

	"github.com/stainless-code/verdict/internal/core"
	verifycore "github.com/stainless-code/verdict/internal/core/verify"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/kb"
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
		if gap := verifyReceiptGap(st.dir, st.headFull, full, claim.vcmd); gap != "" {
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
	// addressed is the one status a rationale cannot buy: it comes only from a
	// VERIFIED claim backed by the verify receipt (applyVerdict).
	if claim.status == outcomeAddressed {
		st.warn(fmt.Sprintf(msgTriageAddressed, readcommon.Prefix(claim.sig)))
		return nil
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
	if claim.status == exampleFalsePositive && !st.dryRun {
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

// verifyReceiptGap names why the bundle's verify receipt does not prove this
// claim, or returns "" when it does. It reads only verify-receipt.json, which
// verify alone writes — never patch.json, the agent's own proposal. The
// receipt must show a passing run of the claim's exact command, for the
// claim's finding when it names one, on the clean gated head. The bundle owns
// the receipt, so it was written for this bundle and its root; the head is the
// binding it can go stale on.
func verifyReceiptGap(dir, head, full, vcmd string) string {
	receipt, found, problem := verifycore.ReadVerifyReceipt(dir)
	switch {
	case problem != "":
		return "the verify receipt is unreadable: " + problem
	case !found:
		return "no verify receipt: " + verifycore.ReceiptFile + " is missing"
	case receipt.Exit != 0:
		return fmt.Sprintf("the verify receipt failed with exit=%d", receipt.Exit)
	case receipt.TimedOut:
		return "the verify receipt timed out"
	}
	return receiptBindingGap(&receipt, head, full, vcmd)
}

// receiptBindingGap checks what the passing run was bound to: the command,
// the finding, and the head.
func receiptBindingGap(receipt *core.VerifyReceipt, head, full, vcmd string) string {
	switch {
	case receipt.Command != vcmd:
		return fmt.Sprintf("the verify receipt ran %q, not the claim's %q", receipt.Command, vcmd)
	case receipt.Sig != "" && !sigCovers(receipt.Sig, full):
		return "the verify receipt is for finding " + readcommon.Prefix(receipt.Sig)
	case receipt.Head == "":
		return "the verify receipt records no clean HEAD"
	case receipt.Head != head:
		return "the verify receipt covers HEAD " + readcommon.ShortSHA(receipt.Head)
	}
	return ""
}

// minSigPrefixLen is the shortest sig prefix a receipt may name a finding by.
const minSigPrefixLen = 8

// sigCovers reports whether a receipt's sig (full or an 8+ prefix) names full.
func sigCovers(sig, full string) bool {
	return sig == full || (len(sig) >= minSigPrefixLen && hasPrefix(full, sig))
}
