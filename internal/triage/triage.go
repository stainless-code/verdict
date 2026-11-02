// Package triage ports the `triage` verb: the human override that flips one
// finding's status with a rationale (or --force / --dry-run), stages KB
// credit for addressed/false-positive flips, and rewrites findings.json.
// Refusals are typed (unknown-sig, ambiguous-sig, bad-triage-status,
// rationale-required, missing-findings); every store write goes through the
// shared atomic path.
package triage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"unicode/utf8"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/bundle"
	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/kb"
)

// Request carries the resolved triage invocation.
type Request struct {
	Env  map[string]string
	Repo string
	Pr   string
	Root string
}

// Run executes `triage <owner/repo> <slug> --sig <s> --status <st>
// --rationale <r> [--force] [--dry-run] [--json]`, returning the process exit
// code. Human progress goes to the runner's human stream; --json mode prints
// exactly one envelope to stdout.
func Run(ctx context.Context, r *cli.Runner, args []string, req Request) int {
	r.Repo, r.Pr = req.Repo, req.Pr
	r.Rentry = ""
	flags, code, done := parseFlags(r, args)
	if done {
		return code
	}
	if err := ctx.Err(); err != nil {
		return r.RefuseExit(&cli.RefusalError{Code: "canceled", Message: "canceled before triage completed"}, cli.ExitBroke)
	}
	code, err := runTriage(r, req, flags)
	if err != nil {
		if ref, ok := errors.AsType[*cli.RefusalError](err); ok {
			return r.Refuse(ref)
		}
		return r.Refuse(&cli.RefusalError{Code: "triage-failed", Message: fmt.Sprintf("triage failed: %v", err)})
	}
	return code
}

// flags carries the parsed triage options.
type flags struct {
	sig       string
	status    string
	rationale string
	force     bool
	dryRun    bool
}

// parseFlags strips triage's flags, reporting unknown ones.
func parseFlags(r *cli.Runner, args []string) (flags, int, bool) {
	var f flags
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--sig", "--status", "--rationale":
			if i+1 < len(args) {
				i++
				setFlag(&f, a, args[i])
			} else {
				setFlag(&f, a, "")
			}
		case "--force":
			f.force = true
		case "--dry-run":
			f.dryRun = true
		case "--json":
		default:
			msg := fmt.Sprintf("unknown flag '%s' for triage "+
				"(want --sig, --status, --rationale, --force, --dry-run, --json)", a)
			return f, r.ReportUsage(&cli.UsageError{Message: msg}), true
		}
	}
	return f, 0, false
}

// setFlag stores one value-taking flag.
func setFlag(f *flags, name, val string) {
	switch name {
	case "--sig":
		f.sig = val
	case "--status":
		f.status = val
	case "--rationale":
		f.rationale = val
	}
}

// runTriage mirrors the triage block: bundle and status gates, sig
// resolution, the rationale rule, then the flip (or the dry-run echo).
func runTriage(r *cli.Runner, req Request, f flags) (int, error) {
	dir := bundle.Dir(req.Pr)
	if err := requireFindingsFile(dir); err != nil {
		return 0, err
	}
	if err := checkStatus(f.status); err != nil {
		return 0, err
	}
	entries, err := readFindings(dir)
	if err != nil {
		return 0, err
	}
	full, err := resolveSig(entries, f.sig)
	if err != nil {
		return 0, err
	}
	if err := requireRationale(f); err != nil {
		return 0, err
	}
	now := cli.NowUTC(r.Now())
	if f.dryRun {
		return dryRun(r, req, full, f, now)
	}
	return applyFlip(r, req, dir, entries, full, f, now)
}

// requireFindingsFile refuses when the bundle has no findings store.
func requireFindingsFile(dir string) error {
	if !bundle.IsFile(dir + findingsSuffix) {
		return &cli.RefusalError{
			Code:    "missing-findings",
			Message: fmt.Sprintf("missing %s%s (run post or record first)", dir, findingsSuffix),
		}
	}
	return nil
}

// checkStatus refuses unknown triage statuses.
func checkStatus(status string) error {
	switch status {
	case "open", "addressed", "wont-fix", "false-positive", "deferred":
		return nil
	default:
		return &cli.RefusalError{
			Code: "bad-triage-status",
			Message: fmt.Sprintf("refusing triage: unknown status '%s' "+
				"(want open|addressed|wont-fix|false-positive|deferred)", status),
		}
	}
}

// readFindings reads and decodes the findings store.
func readFindings(dir string) ([]any, error) {
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
func corruptStore(dir string) error {
	return &cli.RefusalError{
		Code: "corrupt-store",
		Message: fmt.Sprintf("findings store %s%s is unreadable or not a findings array "+
			"(re-run record to rebuild, or restore the file)", dir, findingsSuffix),
	}
}

// resolveSig resolves the sig prefix or refuses when unknown/ambiguous.
func resolveSig(entries []any, prefix string) (string, error) {
	switch full := bundle.ResolveSig(entries, prefix); full {
	case "NONE":
		return "", &cli.RefusalError{Code: "unknown-sig", Message: "refusing triage: sig not in store"}
	case "MANY":
		return "", &cli.RefusalError{Code: "ambiguous-sig", Message: "refusing triage: sig prefix ambiguous"}
	default:
		return full, nil
	}
}

// requireRationale enforces the rationale floor unless forced.
func requireRationale(f flags) error {
	if utf8.RuneCountInString(f.rationale) < rationaleMin && !f.force {
		return &cli.RefusalError{
			Code:    "rationale-required",
			Message: "refusing triage: rationale required (>=30 chars) unless --force",
		}
	}
	return nil
}

// rationaleFor returns the stored rationale, stamping forced flips.
func rationaleFor(f flags) string {
	if f.rationale == "" && f.force {
		return forcedNote
	}
	return f.rationale
}

// applyFlip stages credit, flips the entry, persists, and reports.
func applyFlip(r *cli.Runner, req Request, dir string, entries []any, full string, f flags, now string) (int, error) {
	ratOut := rationaleFor(f)
	k := kb.New(r.Warn)
	credited := stageCredit(r, k, req, entries, full, f.status, now)
	flipEntry(entries, full, f.status, ratOut, now, credited)
	if err := writeFindings(dir, entries); err != nil {
		return 0, err
	}
	k.CommitStaged(dir+findingsSuffix, now)
	emitTriageOutput(r, f.sig, full, f.status, now)
	return 0, nil
}

// stageCredit computes KB credit for the flip, warning on broken stores.
func stageCredit(r *cli.Runner, k *kb.Ctx, req Request, entries []any, full, status, now string) []any {
	k.ResolvePolicy(req.Env, bundle.Exists(kb.File))
	if bundle.IsFile(kb.File) {
		if _, derr := jq.DecodeJSONLOrdered(mustRead(kb.File)); derr != nil {
			r.Warn(fmt.Sprintf("kb store %s unreadable — no credit for this flip (fix the bad line)", kb.File))
			k.Broken = true
		}
	}
	k.ApplyCredit(entries, full, status, "human", "triage", now, bundle.Exists(kb.File))
	if k.Credited == nil {
		return []any{}
	}
	return k.Credited
}

// writeFindings persists the flipped entries atomically.
func writeFindings(dir string, entries []any) error {
	if err := atomic.WriteFile(dir+findingsSuffix, []byte(jq.PrettyOrdered(entries))); err != nil {
		return &cli.RefusalError{
			Code:    "triage-failed",
			Message: fmt.Sprintf("could not write %s%s: %v", dir, findingsSuffix, err),
		}
	}
	return nil
}

// emitTriageOutput prints the human line and the JSON envelope when asked.
func emitTriageOutput(r *cli.Runner, inputSig, full, status, now string) {
	fmt.Fprintf(r.Human, "triaged %s -> %s\n", prefix8(inputSig), status)
	if r.JSONMode {
		env := jq.NewObj(
			"schema_version", "1",
			"verb", "triage",
			"repo", r.Repo,
			"pr", cli.PrJSON(r.Pr),
			"at", now,
			"next", "none — triage recorded",
			"warnings", cliStrings(r.Warnings),
			"data", jq.NewObj("sig", full, "status", status),
			"error", nil,
		)
		fmt.Fprintln(r.Out, env.MarshalPretty())
	}
}

// dryRun ports the --dry-run echo: no store is touched, no credit staged.
func dryRun(r *cli.Runner, _ Request, full string, f flags, now string) (int, error) {
	fmt.Fprintf(r.Human, "triage --dry-run: would set %s -> %s (rationale %d chars, actor human)\n",
		prefix8(full), f.status, utf8.RuneCountInString(f.rationale))
	if r.JSONMode {
		env := jq.NewObj(
			"schema_version", "1",
			"verb", "triage",
			"repo", r.Repo,
			"pr", cli.PrJSON(r.Pr),
			"at", now,
			"next", "none — dry run, nothing written",
			"warnings", cliStrings(r.Warnings),
			"data", jq.NewObj("sig", full, "status", f.status, "dry_run", true),
			"error", nil,
		)
		fmt.Fprintln(r.Out, env.MarshalPretty())
	}
	return 0, nil
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

// cliStrings converts warnings to JSON string values.
func cliStrings(in []string) []any {
	out := make([]any, 0, len(in))
	for _, s := range in {
		out = append(out, s)
	}
	return out
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
