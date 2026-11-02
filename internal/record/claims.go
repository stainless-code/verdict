package record

import (
	"fmt"
	"os"
	"unicode/utf8"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/kb"
)

// applyVerification ports the verification-gated flip: addressed requires
// verification.json evidence with verdict VERIFIED and a matching resultHead.
// A missing or unparsable verification.json means "no claims" (matching the
// shell's silent skip); shapes are validated upfront.
func (st *state) applyVerification() error {
	raw, err := readAgentFile(st.dir + "/verification.json")
	if err != nil {
		if ref := agentRefusal(err); ref != nil {
			return ref
		}
		// A missing verification.json means "no claims", not a failure.
		return nil
	}
	for _, item := range decodeVerifyClaims(raw) {
		st.applyVerificationClaim(item)
	}
	return nil
}

// decodeVerifyClaims decodes verification.json claims: an unparsable or
// non-list document reads as no claims (matching the shell's silent skip);
// shapes are validated upfront, so non-objects here are unreachable.
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

// applyVerificationClaim ports one verification.json claim: VERIFIED with a
// matching resultHead flips to addressed (with KB credit); NOT VERIFIED
// reopens addressed entries; anything else only logs a revalidation event.
// Field rendering mirrors `jq -r` (pretty containers) via jq.R.
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
		st.runner.Warn(fmt.Sprintf("verification claim %s ignored (not in store)", prefix8(vsig)))
		return
	case sigMany:
		st.runner.Warn(fmt.Sprintf("verification claim %s ignored (ambiguous prefix)", prefix8(vsig)))
		return
	}
	st.applyVerdict(findEntry(st.entries, full), full, claim)
}

// verifyClaim carries one verification.json item's normalized fields.
type verifyClaim struct {
	verdict string
	vrh     string
	vcmd    string
}

// applyVerdict applies one resolved verification claim by its verdict.
func (st *state) applyVerdict(target *jq.Obj, full string, claim verifyClaim) {
	shaOK := len(claim.vrh) >= shortSHALen && len(st.headFull) >= shortSHALen &&
		(st.headFull == claim.vrh || hasPrefix(st.headFull, claim.vrh) || hasPrefix(claim.vrh, st.headFull))
	switch {
	case claim.verdict == "VERIFIED" && shaOK && claim.vcmd != "":
		st.applyVerified(target, full, claim.vcmd)
	case claim.verdict == "NOT VERIFIED":
		st.applyNotVerified(target, claim.vcmd)
	default:
		st.recordRevalidation(target, claim)
	}
}

// applyVerified flips the entry addressed with credit and contradiction check.
func (st *state) applyVerified(target *jq.Obj, full, vcmd string) {
	if stt, _ := target.Get(fieldStatus); stt != outcomeAddressed {
		target.Set(fieldStatus, outcomeAddressed)
		target.Set(fieldHistory, appendHistory(target, jq.NewObj(
			fieldAt, st.now, fieldEvent, outcomeAddressed, fieldActor, revalidatorForced,
			fieldNote, vcmd, fieldSHA, st.headShort,
		)))
	}
	// The joins verb is the record verb itself ($cmd in the shell), never the
	// verify command (C5).
	st.kb.ApplyCredit(st.entries, full, outcomeAddressed, revalidatorForced, st.runner.Cmd, st.now, exists(kb.File))
	stampCredit(st, target)
	st.checkVerifyContradiction(full)
}

// applyNotVerified reopens an addressed entry or records the revalidation.
func (st *state) applyNotVerified(target *jq.Obj, vcmd string) {
	event := "revalidation"
	if stt, _ := target.Get(fieldStatus); stt == outcomeAddressed {
		target.Set(fieldStatus, "open")
		event = "reopened"
	}
	target.Set(fieldHistory, appendHistory(target, jq.NewObj(
		fieldAt, st.now, fieldEvent, event, fieldActor, revalidatorForced,
		fieldNote, vcmd, fieldSHA, st.headShort,
	)))
}

// recordRevalidation records a non-terminal verdict as a revalidation note.
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

// checkVerifyContradiction ports the machine-verify contradiction warns: a
// VERIFIED claim that contradicts patch.json's exit lands loudly but the
// flip stands.
func (st *state) checkVerifyContradiction(full string) {
	vexit := jq.R(jq.Alt(patchField(st.dir, "verifyExit"), ""))
	if vexit != "" && vexit != "0" && vexit != "null" {
		st.runner.Warn(fmt.Sprintf(msgContradictExit, prefix8(full), vexit))
		return
	}
	if jq.R(jq.Alt(patchField(st.dir, "timedOut"), "")) == cli.FlagOn {
		st.runner.Warn(fmt.Sprintf(msgContradictTimeout, prefix8(full), vexit))
	}
}

// applyTriage ports the triage[] claims: the unified {sig, status, rationale}
// shape. Unknown statuses refuse the run; unknown sigs and short rationales
// warn and keep the prior status. One bad claim never fails. Shapes are
// validated upfront; non-objects here are unreachable and skipped.
func (st *state) applyTriage() error {
	for _, v := range triageItems(st.review) {
		if err := st.applyTriageClaim(v); err != nil {
			return err
		}
	}
	return nil
}

// triageItems reads the triage claim objects. Shapes are validated upfront,
// so non-arrays and non-objects here are unreachable and skipped.
func triageItems(review *jq.Obj) []*jq.Obj {
	tv, ok := review.Get("triage")
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

// triageClaim carries one triage[] item's normalized fields.
type triageClaim struct {
	sig    string
	status string
	reason string
}

// applyTriageClaim applies one triage claim; an unknown status refuses.
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
		return &cli.RefusalError{Code: codeBadTriageStatus, Message: fmt.Sprintf(msgBadTriage, claim.status)}
	}
	full, code := resolveSig(st.entries, claim.sig)
	switch code {
	case sigNone:
		st.runner.Warn(fmt.Sprintf("triage claim %s ignored (not in store)", prefix8(claim.sig)))
		return nil
	case sigMany:
		st.runner.Warn("triage claim " + prefix8(claim.sig) + " ignored (ambiguous prefix)")
		return nil
	}
	if utf8.RuneCountInString(claim.reason) < minRationaleRunes {
		st.runner.Warn("triage claim without rationale, kept prior status: " + prefix8(claim.sig))
		return nil
	}
	st.applyTriageFlip(findEntry(st.entries, full), full, claim)
	return nil
}

// isTriageStatus reports whether s is a known triage status.
func isTriageStatus(s string) bool {
	switch s {
	case "open", outcomeAddressed, "wont-fix", "false-positive", "deferred":
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
		st.kb.ApplyCredit(st.entries, full, claim.status, "agent", st.runner.Cmd, st.now, exists(kb.File))
		stampCredit(st, target)
	}
}

// stampCredit stamps the current flip's credited rules into the latest
// history event, mirroring `history[-1] += {kb_credit: ...}`.
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

// hasPrefix reports whether s starts with prefix (byte-based, like the
// shell's case pattern).
func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

// patchField reads one raw field from dir/patch.json (nil when absent).
func patchField(dir, field string) any {
	raw, err := os.ReadFile(dir + "/patch.json")
	if err != nil {
		return nil
	}
	doc, err := jq.DecodeOrdered(raw)
	if err != nil {
		return nil
	}
	m, ok := doc.(*jq.Obj)
	if !ok {
		return nil
	}
	v, _ := m.Get(field)
	return v
}
