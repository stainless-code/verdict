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
	"path/filepath"
	"unicode/utf8"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/config"
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

type state struct { //nolint:govet // triage service state is allocated once per invocation
	req      *Request
	run      *config.RunState
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

func (st *state) warn(msg string) {
	st.warnings = append(st.warnings, msg)
	st.req.Warn(msg)
}

func (st *state) refuse(code, msg string) *core.RefusalError {
	return &core.RefusalError{Code: code, Message: msg, Warnings: st.warnings}
}

func (st *state) response() *Response {
	return &Response{
		At:       st.req.TimeUTC(),
		Next:     st.next,
		Human:    st.human,
		Data:     st.data,
		Warnings: st.warnings,
	}
}

func (st *state) runTriage() *core.RefusalError {
	dir := readcommon.ResolveBundleDir(st.req.RepoRootPath(), st.req.Pr)
	if ref := requireFindingsFile(dir); ref != nil {
		return ref
	}
	run, ref := config.VerifyRunState(dir)
	if ref != nil {
		return ref
	}
	st.run = run
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

func requireFindingsFile(dir string) *core.RefusalError {
	if !readcommon.IsFile(dir + findingsSuffix) {
		return &core.RefusalError{
			Code:    "missing-findings",
			Message: fmt.Sprintf("missing %s%s (run post or record first)", dir, findingsSuffix),
		}
	}
	return nil
}

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

func corruptStore(dir string) *core.RefusalError {
	return &core.RefusalError{
		Code: "corrupt-store",
		Message: fmt.Sprintf("findings store %s%s is unreadable or not a findings array "+
			"(re-run record to rebuild, or restore the file)", dir, findingsSuffix),
	}
}

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

func (st *state) requireRationale() *core.RefusalError {
	if utf8.RuneCountInString(st.req.Rationale) < rationaleMin && !st.req.Force {
		return st.refuse("rationale-required",
			"refusing triage: rationale required (>=30 chars) unless --force")
	}
	return nil
}

func (st *state) rationaleFor() string {
	if st.req.Rationale == "" && st.req.Force {
		return forcedNote
	}
	return st.req.Rationale
}

// kbThresholds reads the retirement policy: the bundle's verified typed
// defaults when it carries them, else the consumed environment names over
// the built-in floor.
func (st *state) kbThresholds() (int, string) {
	if st.run != nil && st.run.Resolved != nil {
		return st.run.Resolved.Defaults.Values.KBPolicy()
	}
	defaults := config.EnvDefaults(st.req.Env, st.warn)
	return defaults.KBPolicy()
}

func (st *state) applyFlip(dir string, entries []any, full, now string) *core.RefusalError {
	ratOut := st.rationaleFor()
	k := kb.New(st.warn)
	credited := st.stageCredit(k, entries, full, now)
	flipEntry(entries, full, st.req.Status, ratOut, now, credited)
	if err := writeFindings(dir, entries); err != nil {
		return st.refuse("triage-failed", fmt.Sprintf("could not write %s%s: %v", dir, findingsSuffix, err))
	}
	k.CommitStaged(dir+findingsSuffix, now)
	st.human = fmt.Sprintf("triaged %s -> %s", readcommon.Prefix(st.req.Sig), st.req.Status)
	st.next = "none — triage recorded"
	st.data = &Data{Sig: full, Status: st.req.Status}
	return nil
}

func (st *state) stageCredit(k *kb.Ctx, entries []any, full, now string) []any {
	root := st.req.RepoRootPath()
	store := filepath.Join(root, kb.File)
	k.Root = root
	k.Policy = kb.PolicyFrom(st.kbThresholds())
	exists := readcommon.Exists(store)
	if readcommon.IsFile(store) {
		if _, err := kb.ParseRows(mustRead(store)); err != nil {
			st.warn(fmt.Sprintf("kb store %s unreadable — no credit for this flip (fix the bad line)", store))
			k.Broken = true
		}
	}
	k.ApplyCredit(entries, full, st.req.Status, "human", "triage", now, exists)
	if k.Credited == nil {
		return []any{}
	}
	return k.Credited
}

func writeFindings(dir string, entries []any) error {
	return atomic.WriteFile(dir+findingsSuffix, []byte(jq.PrettyOrdered(entries)))
}

func (st *state) dryRun(full string) {
	st.human = fmt.Sprintf("triage --dry-run: would set %s -> %s (rationale %d chars, actor human)",
		readcommon.Prefix(full), st.req.Status, utf8.RuneCountInString(st.req.Rationale))
	st.next = "none — dry run, nothing written"
	st.data = &Data{Sig: full, Status: st.req.Status, DryRun: true}
}

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

func mustRead(path string) []byte {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return raw
}

func flipEntry(entries []any, sig, status, rationale, now string, credited []any) {
	for _, e := range entries {
		flipOne(e, sig, status, rationale, now, credited)
	}
}

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

func historyOf(m *jq.Obj) []any {
	raw, _ := m.Get("history")
	if arr, ok := raw.([]any); ok {
		return arr
	}
	return []any{}
}

const (
	// rationaleMin is the human-rationale floor (characters, like ${#}).
	rationaleMin = 30
	// forcedNote is the rationale stamped on --force flips without one.
	forcedNote = "forced without rationale"
	// findingsSuffix is the store filename joined to the bundle dir.
	findingsSuffix = "/findings.json"
)

var errNotArray = storeError("not a findings array")

type storeError string

func (e storeError) Error() string { return string(e) }
