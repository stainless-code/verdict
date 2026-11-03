// Package triagecore is the triage verb's domain service: the human override
// that flips one finding's status with a rationale (or --force / --dry-run),
// stages KB credit for addressed/false-positive flips, and rewrites
// findings.json. Refusals are typed; every store write goes through the
// shared atomic path. internal/triage owns argv parsing, progress routing,
// envelope rendering, and exit mapping.
package triagecore

import (
	"context"
	"fmt"
	"os"
	"unicode/utf8"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/bundle"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/kb"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// Request carries the resolved triage invocation.
type Request struct {
	core.ReadRequest
	Sig       string
	Status    string `enum:"open|addressed|wont-fix|false-positive|deferred"`
	Rationale string
	Force     bool
	DryRun    bool
}

// Response carries the flip summary: the envelope fields, the human line,
// and the typed data profile.
type Response struct {
	At       string
	Next     string
	Human    string
	Data     *Data
	Warnings []string
}

// Data is the triage envelope's data profile.
type Data struct {
	Sig    string `json:"sig"`
	Status string `json:"status"`
	DryRun bool   `json:"dry_run,omitempty"`
}

// Wire renders the profile in published wire order; the dry-run flag keeps
// its trailing position.
func (d *Data) Wire() *jq.Obj {
	o := jq.NewObj("sig", d.Sig, "status", d.Status)
	if d.DryRun {
		o.Set("dry_run", true)
	}
	return o
}

// state carries one triage run: the request, the accumulated warnings, and
// the completed flip summary.
type state struct { //nolint:govet // triage service state is allocated once per invocation
	req      *Request
	warnings []string
	human    string
	next     string
	data     *Data
}

// Run flips one finding's status in the bundle's findings store.
func Run(ctx context.Context, req *Request) (*Response, *core.RefusalError) {
	st := &state{req: req}
	if err := ctx.Err(); err != nil {
		return nil, st.refuse("canceled", "canceled before triage completed")
	}
	if ref := st.runTriage(); ref != nil {
		return nil, ref
	}
	return st.response(), nil
}

// warn records and streams one warning.
func (st *state) warn(msg string) {
	st.warnings = append(st.warnings, msg)
	st.req.Warn(msg)
}

// refuse builds a typed refusal carrying the accumulated warnings.
func (st *state) refuse(code, msg string) *core.RefusalError {
	return &core.RefusalError{Code: code, Message: msg, Warnings: st.warnings}
}

// response assembles the typed response after the flip (or dry run).
func (st *state) response() *Response {
	return &Response{
		At:       st.req.TimeUTC(),
		Next:     st.next,
		Human:    st.human,
		Data:     st.data,
		Warnings: st.warnings,
	}
}

// runTriage mirrors the triage block: bundle and status gates, sig
// resolution, the rationale rule, then the flip (or the dry-run echo).
func (st *state) runTriage() *core.RefusalError {
	dir := bundle.Dir(st.req.Pr)
	if ref := requireFindingsFile(dir); ref != nil {
		return ref
	}
	if ref := checkStatus(st.req.Status); ref != nil {
		return ref
	}
	entries, ref := st.readFindings(dir)
	if ref != nil {
		return ref
	}
	full, ref := resolveSig(entries, st.req.Sig)
	if ref != nil {
		return ref
	}
	if ref := st.requireRationale(); ref != nil {
		return ref
	}
	now := st.req.TimeUTC()
	if st.req.DryRun {
		st.dryRun(full)
		return nil
	}
	return st.applyFlip(dir, entries, full, now)
}

// requireFindingsFile refuses when the bundle has no findings store.
func requireFindingsFile(dir string) *core.RefusalError {
	if !bundle.IsFile(dir + findingsSuffix) {
		return &core.RefusalError{
			Code:    "missing-findings",
			Message: fmt.Sprintf("missing %s%s (run post or record first)", dir, findingsSuffix),
		}
	}
	return nil
}

// checkStatus refuses unknown triage statuses.
func checkStatus(status string) *core.RefusalError {
	switch status {
	case "open", "addressed", "wont-fix", "false-positive", "deferred":
		return nil
	default:
		return &core.RefusalError{
			Code: "bad-triage-status",
			Message: fmt.Sprintf("refusing triage: unknown status '%s' "+
				"(want open|addressed|wont-fix|false-positive|deferred)", status),
		}
	}
}

// readFindings reads and decodes the findings store.
func (st *state) readFindings(dir string) ([]any, *core.RefusalError) {
	raw, err := os.ReadFile(dir + findingsSuffix)
	if err != nil {
		return nil, corruptStore(dir)
	}
	entries, err := decodeEntries(raw)
	if err != nil {
		return nil, corruptStore(dir)
	}
	return entries, nil
}

// corruptStore builds the corrupt-store refusal for dir.
func corruptStore(dir string) *core.RefusalError {
	return &core.RefusalError{
		Code: "corrupt-store",
		Message: fmt.Sprintf("findings store %s%s is unreadable or not a findings array "+
			"(re-run record to rebuild, or restore the file)", dir, findingsSuffix),
	}
}

// resolveSig resolves the sig prefix or refuses when unknown/ambiguous.
func resolveSig(entries []any, prefix string) (string, *core.RefusalError) {
	switch full := readcommon.ResolveSig(entries, prefix); full {
	case "NONE":
		return "", &core.RefusalError{Code: "unknown-sig", Message: "refusing triage: sig not in store"}
	case "MANY":
		return "", &core.RefusalError{Code: "ambiguous-sig", Message: "refusing triage: sig prefix ambiguous"}
	default:
		return full, nil
	}
}

// requireRationale enforces the rationale floor unless forced.
func (st *state) requireRationale() *core.RefusalError {
	if utf8.RuneCountInString(st.req.Rationale) < rationaleMin && !st.req.Force {
		return st.refuse("rationale-required",
			"refusing triage: rationale required (>=30 chars) unless --force")
	}
	return nil
}

// rationaleFor returns the stored rationale, stamping forced flips.
func (st *state) rationaleFor() string {
	if st.req.Rationale == "" && st.req.Force {
		return forcedNote
	}
	return st.req.Rationale
}

// applyFlip stages credit, flips the entry, persists, and records the human
// and JSON reports.
func (st *state) applyFlip(dir string, entries []any, full, now string) *core.RefusalError {
	ratOut := st.rationaleFor()
	k := kb.New(st.warn)
	credited := st.stageCredit(k, entries, full, now)
	flipEntry(entries, full, st.req.Status, ratOut, now, credited)
	if err := writeFindings(dir, entries); err != nil {
		return st.refuse("triage-failed", fmt.Sprintf("could not write %s%s: %v", dir, findingsSuffix, err))
	}
	k.CommitStaged(dir+findingsSuffix, now)
	st.human = fmt.Sprintf("triaged %s -> %s", prefix8(st.req.Sig), st.req.Status)
	st.next = "none — triage recorded"
	st.data = &Data{Sig: full, Status: st.req.Status}
	return nil
}

// stageCredit computes KB credit for the flip, warning on broken stores.
func (st *state) stageCredit(k *kb.Ctx, entries []any, full, now string) []any {
	k.ResolvePolicy(st.req.Env, bundle.Exists(kb.File))
	if bundle.IsFile(kb.File) {
		if _, derr := jq.DecodeJSONLOrdered(mustRead(kb.File)); derr != nil {
			st.warn(fmt.Sprintf("kb store %s unreadable — no credit for this flip (fix the bad line)", kb.File))
			k.Broken = true
		}
	}
	k.ApplyCredit(entries, full, st.req.Status, "human", "triage", now, bundle.Exists(kb.File))
	if k.Credited == nil {
		return []any{}
	}
	return k.Credited
}

// writeFindings persists the flipped entries atomically.
func writeFindings(dir string, entries []any) error {
	return atomic.WriteFile(dir+findingsSuffix, []byte(jq.PrettyOrdered(entries)))
}

// dryRun records the --dry-run echo: no store is touched, no credit staged.
func (st *state) dryRun(full string) {
	st.human = fmt.Sprintf("triage --dry-run: would set %s -> %s (rationale %d chars, actor human)",
		prefix8(full), st.req.Status, utf8.RuneCountInString(st.req.Rationale))
	st.next = "none — dry run, nothing written"
	st.data = &Data{Sig: full, Status: st.req.Status, DryRun: true}
}

// decodeEntries parses findings.json as a JSON array, order-preserving.
func decodeEntries(raw []byte) ([]any, error) {
	doc, err := jq.DecodeOrdered(raw)
	if err != nil {
		//nolint:wrapcheck,errorlint // %v seals the boundary by design (GO-ERRORS-03)
		return nil, fmt.Errorf("decode findings: %v", err)
	}
	arr, ok := doc.([]any)
	if !ok {
		return nil, errNotArray
	}
	return arr, nil
}

// mustRead reads a file known to exist (checked by the caller).
func mustRead(path string) []byte {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return raw
}

// flipEntry applies the status flip plus the triage history event in place,
// mirroring the shell's jq map (updated keys keep position, triagedAt/By
// append after history).
func flipEntry(entries []any, sig, status, rationale, now string, credited []any) {
	for _, e := range entries {
		flipOne(e, sig, status, rationale, now, credited)
	}
}

// flipOne flips a single entry when its sig matches.
func flipOne(e any, sig, status, rationale, now string, credited []any) {
	m, ok := e.(*jq.Obj)
	if !ok || m == nil {
		return
	}
	sv, ok := m.Get("sig")
	if !ok {
		return
	}
	s, ok := sv.(string)
	if !ok || s != sig {
		return
	}
	m.Set("status", status)
	m.Set("rationale", rationale)
	event := jq.NewObj(
		"at", now,
		"event", "triaged",
		"actor", "human",
		"note", rationale,
	)
	if len(credited) > 0 {
		event.Set("kb_credit", credited)
	}
	hist := historyOf(m)
	hist = append(hist, event)
	m.Set("history", hist)
	m.Set("triagedAt", now)
	m.Set("triagedBy", "human")
}

// historyOf reads the entry's history array (missing reads empty).
func historyOf(m *jq.Obj) []any {
	raw, _ := m.Get("history")
	if arr, ok := raw.([]any); ok {
		return arr
	}
	return []any{}
}

// prefix8 renders the 8-char sig prefix for the human summary.
func prefix8(sig string) string {
	runes := []rune(sig)
	if len(runes) > sigPrefixLen {
		return string(runes[:sigPrefixLen])
	}
	return sig
}

// Refusal and message constants.
const (
	// rationaleMin is the human-rationale floor (characters, like ${#}).
	rationaleMin = 30
	// forcedNote is the rationale stamped on --force flips without one.
	forcedNote = "forced without rationale"
	// sigPrefixLen is the human sig-prefix width.
	sigPrefixLen = 8
	// findingsSuffix is the store filename joined to the bundle dir.
	findingsSuffix = "/findings.json"
)

// errNotArray is the corrupt-store signal for non-array findings.
var errNotArray = storeError("not a findings array")

// storeError is a plain error value for store-shape failures.
type storeError string

// Error implements error.
func (e storeError) Error() string { return string(e) }
