// Package triagecore is the triage verb's domain service: the human override
// that flips one finding's status with a rationale (or --force / --dry-run),
// stages KB credit for addressed/false-positive flips, and rewrites
// findings.json. Refusals are typed; every store write goes through the
// shared atomic path. internal/triage owns argv parsing, progress routing,
// envelope rendering, and exit mapping.
package triagecore

import (
	"context"
	"errors"
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
	"github.com/stainless-code/verdict/internal/storelock"
)

// Request carries the resolved triage invocation.
type Request struct {
	core.ReadRequest
	Sig       string
	Status    string `enum:"open|addressed|wont-fix|false-positive|deferred"`
	Rationale string
	Force     bool
	DryRun    bool
	// RefuseWithheld, when true, refuses a policy-withheld finding with the
	// unknown-sig refusal, so a withheld finding reads as absent — the read
	// surface's 404. The interactive transport sets it; the CLI leaves it
	// false and keeps flipping withheld findings as before.
	RefuseWithheld bool
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
	if ref := st.runTriage(ctx); ref != nil {
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

// openBundle resolves the request's bundle under the shared name guard and
// proves its findings store exists before the pass reads anything.
func (st *state) openBundle() (string, *core.RefusalError) {
	dir, ref := readcommon.ResolveBundleDir(st.req.RepoRootPath(), st.req.Pr)
	if ref != nil {
		return "", st.refuse(ref.Code, ref.Message)
	}
	return dir, requireFindingsFile(dir)
}

func (st *state) runTriage(ctx context.Context) *core.RefusalError {
	dir, ref := st.openBundle()
	if ref != nil {
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
	full, ref := st.preflight(dir)
	if ref != nil {
		return ref
	}
	now := st.req.TimeUTC()
	if st.req.DryRun {
		st.dryRun(full)
		return nil
	}
	unlock, ref := st.lockBundle(ctx, dir)
	if ref != nil {
		return ref
	}
	defer unlock()
	return st.applyLocked(dir, now)
}

// preflight runs the lock-free checks: one findings read, sig resolution,
// the withholding posture, and the rationale floor. A refusal here writes
// nothing, lock file included, and a dry run answers from it too. The checks
// are snapshot-independent (sig identity and withheld receipts do not move
// under a triage); every mutation re-runs resolve and withholding on the
// locked snapshot, which is the one the flip mutates.
func (st *state) preflight(dir string) (string, *core.RefusalError) {
	entries, ref := st.readFindings(dir)
	if ref != nil {
		return "", ref
	}
	full, ref := st.checkEntries(entries)
	if ref != nil {
		return "", ref
	}
	if ref := st.requireRationale(); ref != nil {
		return "", ref
	}
	return full, nil
}

// applyLocked re-reads and re-validates a mutation under the bundle lock:
// the snapshot the flip stages and writes is the one it validated there.
func (st *state) applyLocked(dir, now string) *core.RefusalError {
	entries, ref := st.readFindings(dir)
	if ref != nil {
		return ref
	}
	full, ref := st.checkEntries(entries)
	if ref != nil {
		return ref
	}
	return st.applyFlip(dir, entries, full, now)
}

// checkEntries resolves the request's sig against one snapshot and, when
// asked, refuses a policy-withheld entry. It runs before the lock (so a
// refusal writes nothing, lock file included) and again under it (on the
// exact snapshot the flip mutates).
func (st *state) checkEntries(entries []any) (string, *core.RefusalError) {
	full, ref := resolveSig(entries, st.req.Sig)
	if ref != nil {
		return "", ref
	}
	if st.req.RefuseWithheld && isWithheld(entries, full) {
		return "", &core.RefusalError{
			Code:    "unknown-sig",
			Message: "refusing triage: finding not in store",
		}
	}
	return full, nil
}

// lockBundle takes the findings store's OS advisory lock, so the whole
// read-modify-write below — findings read, credit staging, findings write,
// KB commit — is one writer at a time however the invocations arrive
// (concurrent HTTP triage, CLI beside server). It is a different lock file
// from the knowledge-base store's, so holding it across CommitStaged never
// nests the KB lock inside itself; the ordering is always bundle then KB,
// and no path takes them the other way. A busy store is retryable; any
// other lock failure fails the flip rather than writing unlocked.
func (st *state) lockBundle(ctx context.Context, dir string) (func(), *core.RefusalError) {
	unlock, err := storelock.AcquireContext(ctx, dir+findingsSuffix, storelock.DefaultWait)
	if err == nil {
		return unlock, nil
	}
	if ctx.Err() != nil {
		return nil, st.refuse("canceled", "canceled before triage completed")
	}
	if errors.Is(err, storelock.ErrBusy) {
		return nil, st.refuse("store-busy",
			"another write holds the store lock on "+dir+findingsSuffix+" (retry in a moment)")
	}
	return nil, st.refuse("triage-failed",
		fmt.Sprintf("could not lock %s%s: %v", dir, findingsSuffix, err))
}

// isWithheld reports whether this snapshot's resolved entry is a
// policy-withheld finding. It reads the same entries the flip mutates, so
// no sweep between the check and the write can slip a withheld flip past it.
func isWithheld(entries []any, full string) bool {
	for _, entry := range entries {
		if jq.StrOf(readcommon.FieldOf(entry, "sig")) == full {
			return readcommon.WithheldSource(entry) != ""
		}
	}
	return false
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
