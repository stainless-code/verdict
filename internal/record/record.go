// Package record ports the `record` verb: the local-bundle hot path that
// validates anchors keep-biased, demotes fenced suggestions, derives stable
// sigs, merges findings with resolved/triage/verification claims and KB
// credit staging, and writes the record artifact plus the machine envelope.
//
// The body the shell curates (unanchored section, footer, diagram strips) is
// a post-time artifact: record validates the garnish for warnings and
// discards the body, persisting only findings.json, record.json, and the KB
// stores. Every store write goes through the shared atomic path (temp +
// fsync + rename + dir fsync).
//
// R5 exceptions: bundle paths come from the operator's own invocation
// (fixed filenames under the cwd), not from agents — so this package reads
// them without [os.Root], and the G304 exclusion in .golangci.yml covers those
// reads by design (same exposure as the shell port).
package record

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/hunks"
	"github.com/stainless-code/verdict/internal/jq"
)

// Request carries the resolved record invocation.
type Request struct {
	Env    map[string]string
	Forces *cli.GlobalForces
	Repo   string
	Pr     string
	Root   string
}

// Run executes `record <owner/repo> <local-slug> [--json]`, returning the
// process exit code. Human progress goes to the runner's human stream;
// --json mode prints exactly one envelope to stdout.
func Run(ctx context.Context, r *cli.Runner, args []string, req Request) int {
	setupRunner(r, req)
	if code, done := parseRecordFlags(r, args); done {
		return code
	}
	if code, done := checkCancelled(ctx, r); done {
		return code
	}
	code, err := runRecord(r, req)
	return finishRun(r, code, err)
}

// setupRunner records the invocation for envelopes and usage lines.
func setupRunner(r *cli.Runner, req Request) {
	r.Repo, r.Pr = req.Repo, req.Pr
	r.Rentry = "./scripts/review.sh record " + req.Repo + " " + req.Pr
	if r.JSONMode {
		r.Rentry += " --json"
	}
}

// parseRecordFlags strips record's flags, reporting unknown ones.
func parseRecordFlags(r *cli.Runner, args []string) (int, bool) {
	for _, a := range args {
		if a == "--json" {
			continue
		}
		return r.ReportUsage(&cli.UsageError{Message: fmt.Sprintf("unknown flag '%s' for record (want --json)", a)}), true
	}
	return 0, false
}

// checkCancelled refuses when the context is already done.
func checkCancelled(ctx context.Context, r *cli.Runner) (int, bool) {
	if err := ctx.Err(); err != nil {
		// GO-CLI-05: cancel kills the run with exit 3 and a JSON envelope.
		return r.RefuseExit(&cli.RefusalError{Code: "canceled", Message: "canceled before record completed"}, cli.ExitBroke), true
	}
	return 0, false
}

// finishRun maps a runRecord result to the process exit code.
func finishRun(r *cli.Runner, code int, err error) int {
	if err == nil {
		return code
	}
	if ref, _ := errors.AsType[*cli.RefusalError](err); ref != nil {
		return r.Refuse(ref)
	}
	return r.Refuse(&cli.RefusalError{Code: codeRecordFailed, Message: fmt.Sprintf("record failed: %v", err)})
}

// runRecord mirrors the record block: bundle checks, then validate_and_merge,
// then the record artifact and envelope.
func runRecord(r *cli.Runner, req Request) (int, error) {
	dir := resolveBundleDir(req.Pr)
	prov, ref := resolveProvider(dir)
	if ref != nil {
		return 0, ref
	}
	if ref := checkBundleFiles(dir, prov); ref != nil {
		return 0, ref
	}
	if err := ensureStore(dir); err != nil {
		return 0, err
	}
	headFull := resolveHead(dir, prov)
	now := cli.NowUTC(r.Now())
	if err := ensureHunksSnapshot(r, dir); err != nil {
		return 0, err
	}
	st, err := loadReview(r, req, dir, prov, headFull, now)
	if err != nil {
		return 0, err
	}
	st.findingsFile = dir + "/findings.json"
	if _, _, ref := st.validateAndMerge(reviewBody(st)); ref != nil {
		return 0, ref
	}
	if err := writeFindings(st); err != nil {
		return 0, err
	}
	return finishMerge(r, st, now)
}

// finishMerge commits credit, reports findings, and writes the artifacts.
func finishMerge(r *cli.Runner, st *state, now string) (int, error) {
	st.kb.CommitStaged(st.findingsFile, now)
	reportFindings(r, st)
	if st.hunkN == 0 {
		r.Warn("no hunks indexed — anchors unchecked, everything kept")
	}
	return finalizeClean(st)
}

// resolveProvider maps the bundle dir to its provider, refusing non-bundles.
func resolveProvider(dir string) (string, error) {
	if strings.HasPrefix(dir, ".review/local-") {
		return providerLocal, nil
	}
	switch prov := providerOf(dir); prov {
	case "":
		return "", &cli.RefusalError{Code: "not-local", Message: msgNotLocal}
	case "github":
		return "", &cli.RefusalError{Code: "not-local", Message: msgNotGH}
	default:
		return prov, nil
	}
}

// checkBundleFiles refuses when the bundle's required files are absent.
func checkBundleFiles(dir, prov string) error {
	files := []struct {
		path string
		code string
		hint string
	}{
		{dir + "/review.json", codeMissingReviewFile, "agent must write {body, approved, comments[]}"},
		{dir + "/full.diff", codeMissingDiff, "run fetch-local / fetch --provider first"},
	}
	for _, f := range files {
		if !exists(f.path) {
			return &cli.RefusalError{Code: f.code, Message: fmt.Sprintf("missing %s (%s)", f.path, f.hint)}
		}
	}
	source := dir + prSuffix
	hint := "run fetch --provider first"
	if prov == providerLocal {
		source = dir + "/source.json"
		hint = "run fetch-local first"
	}
	if !exists(source) {
		return &cli.RefusalError{Code: "missing-source", Message: fmt.Sprintf("missing %s (%s)", source, hint)}
	}
	return nil
}

// resolveHead reads the reviewed SHA, defaulting per provider.
func resolveHead(dir, prov string) string {
	if prov == providerLocal {
		if headFull := jqField(dir+"/source.json", "head_sha"); headFull != "" {
			return headFull
		}
		return "dirty"
	}
	if headFull := provHead(dir); headFull != "" {
		return headFull
	}
	return "unknown"
}

// loadReview reads and gates the review.json, returning the merge state.
func loadReview(r *cli.Runner, req Request, dir, prov, headFull, now string) (*state, error) {
	reviewFile := dir + "/review.json"
	reviewRaw, err := readAgentFile(reviewFile)
	if err != nil {
		if ref := agentRefusal(err); ref != nil {
			return nil, ref
		}
		return nil, &cli.RefusalError{Code: codeMissingReviewFile, Message: fmt.Sprintf(msgReviewMissing, reviewFile)}
	}
	reviewDoc, err := jq.DecodeOrdered(reviewRaw)
	if err != nil {
		return nil, &cli.RefusalError{Code: codeBadReviewInput, Message: msgBadReviewJSON}
	}
	review, ok := reviewDoc.(*jq.Obj)
	if !ok {
		return nil, &cli.RefusalError{Code: codeBadReviewSchema, Message: msgReviewNotObject}
	}
	bodyRaw, _ := review.Get("body")
	body := jq.Str(jq.Alt(bodyRaw, ""))
	if err := checkDegenerate(body); err != nil {
		return nil, err
	}
	return &state{
		runner: r, dir: dir, prov: prov, headFull: headFull,
		headShort: shortSHA(headFull), now: now, review: review,
		reviewFile: reviewFile, root: req.Root, env: req.Env, forces: req.Forces,
	}, nil
}

// reviewBody reads the review body for the merge, mirroring the shell's
// `jq -r '.body // ""'` (containers pretty-print via jq.R).
func reviewBody(st *state) string {
	bodyRaw, _ := st.review.Get("body")
	return jq.R(jq.Alt(bodyRaw, ""))
}

// reportFindings emits the human findings summary table.
func reportFindings(r *cli.Runner, st *state) {
	fmt.Fprintf(r.Human, "» findings: %d sigs -> %s\n", len(st.entries), st.findingsFile)
	for _, e := range st.entries {
		m, ok := e.(*jq.Obj)
		if !ok || m == nil {
			continue
		}
		sv, _ := m.Get("sig")
		tv, _ := m.Get("status")
		av, _ := m.Get("anchored")
		pv, _ := m.Get("path")
		hv, _ := m.Get("heading")
		fmt.Fprintf(r.Human, "  %s  %s  %s  %s\n",
			sigPrefix(jq.Interp(sv)),
			jq.Interp(tv),
			jq.Interp(av),
			jq.Interp(jq.Alt(jq.Alt(pv, hv), "?")),
		)
	}
}

// finalizeClean removes the derived chunk snapshot and writes the artifacts.
func finalizeClean(st *state) (int, error) {
	removeDerivedSnapshot(st)
	return finalizeRecord(st)
}

// removeDerivedSnapshot deletes the per-chunk pinned hunks: a derived
// snapshot for one run only, while the fetch-time artifacts stay
// authoritative. The post verb calls this right after the merge (like the
// shell's validate_and_merge tail), so refusals never leave it behind.
func removeDerivedSnapshot(st *state) {
	// The per-chunk pinned hunks are a derived snapshot for this run only;
	// the fetch-time artifacts stay authoritative.
	if st.anchorHunks != st.dir+"/hunks.tsv" {
		removeBestEffort(st.anchorHunks)
	}
}

// removeBestEffort deletes a derived snapshot, ignoring failures: the file
// is advisory (fetch-time artifacts stay authoritative) and a stale copy
// only costs a rebuild.
func removeBestEffort(path string) {
	// Best-effort derived-file cleanup; stale copies rebuild.
	_ = os.Remove(path)
}

// resolveBundleDir ports resolve_bundle_dir: local-* slugs and bare slugs
// with a matching .review/local-* dir resolve local.
func resolveBundleDir(pr string) string {
	if strings.HasPrefix(pr, localPrefix) {
		return ".review/" + pr
	}
	if isDir(".review/local-"+pr) && !exists(".review/"+pr+prSuffix) {
		return ".review/local-" + pr
	}
	return ".review/" + pr
}

// providerOf reads .provider from PR.json ("" when missing/unreadable).
func providerOf(dir string) string {
	raw, err := os.ReadFile(dir + prSuffix)
	if err != nil {
		return ""
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		return ""
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		return ""
	}
	prov := jq.StrOf(m["provider"])
	return prov
}

// provHead reads the provider head SHA via the normalized field rules.
func provHead(dir string) string {
	raw, err := os.ReadFile(dir + prSuffix)
	if err != nil {
		return ""
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		return ""
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		return ""
	}
	prov := jq.StrOf(m["provider"])
	if prov == "" {
		hs := jq.StrOf(m["headRefOid"])
		return hs
	}
	hs := jq.StrOf(m["head_sha"])
	return hs
}

// ensureStore ports ensure_store: backfill the store marker when absent.
func ensureStore(dir string) error {
	if exists(dir + "/store.json") {
		return nil
	}
	return atomic.WriteFile(dir+"/store.json", []byte("{\n  \"schema_version\": \"1\"\n}\n"))
}

// ensureHunksSnapshot ports ensure_hunks_snapshot: rebuild from full.diff
// when the fetch-time snapshot is missing, warning that validation is
// best-effort.
func ensureHunksSnapshot(r *cli.Runner, dir string) error {
	if exists(dir + "/hunks.tsv") {
		return nil
	}
	r.Warn(fmt.Sprintf(msgHunksRebuild, dir))
	raw, err := os.ReadFile(dir + "/full.diff")
	if err != nil {
		return &cli.RefusalError{Code: "missing-diff", Message: fmt.Sprintf(msgMissingDiff, dir)}
	}
	return atomic.WriteFile(dir+"/hunks.tsv", []byte(hunks.Format(hunks.Parse(string(raw)))))
}

var (
	markerRe      = regexp.MustCompile(`(?m)^> (✅|ℹ️|\[!IMPORTANT\]|\[!CAUTION\])`)
	placeholderRe = regexp.MustCompile(`test|testing|placeholder|temp|foo|bar|asdf|dummy|sample`)
)

// checkDegenerate ports the degenerate-body guard: a verdict marker exempts
// any body; otherwise short bodies refuse, and short placeholder-looking
// bodies refuse too.
func checkDegenerate(body string) error {
	if markerRe.MatchString(body) {
		return nil
	}
	if len(body) < degenerateFloor {
		return &cli.RefusalError{Code: "degenerate-body", Message: "refusing degenerate body (<20 chars, no verdict marker)."}
	}
	if len(body) < placeholderFloor && asciiContainsLower(body, placeholderRe) {
		return &cli.RefusalError{Code: "degenerate-body", Message: "refusing placeholder-looking short body."}
	}
	return nil
}

// asciiContainsLower reports a case-insensitive ASCII match, mirroring grep
// -i under the C locale.
func asciiContainsLower(s string, re *regexp.Regexp) bool {
	return re.MatchString(asciiLower(s))
}

// asciiLower lowercases ASCII letters only.
func asciiLower(s string) string {
	var sb strings.Builder
	sb.Grow(len(s))
	for i := range len(s) {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		sb.WriteByte(c)
	}
	return sb.String()
}

// shortSHA ports ${head_full:0:7}.
func shortSHA(head string) string {
	if len(head) > shortSHALen {
		return head[:shortSHALen]
	}
	return head
}

// sigPrefix renders the 8-char sig prefix for the human table.
func sigPrefix(sig string) string {
	if len(sig) > sigPrefixLen {
		return sig[:sigPrefixLen]
	}
	return sig
}

// countStatus counts entries with status.
func countStatus(entries []any, status string) int {
	n := 0
	for _, e := range entries {
		if m, ok := e.(*jq.Obj); ok {
			if s, _ := m.Get("status"); s == status {
				n++
			}
		}
	}
	return n
}

// sha256File streams full.diff into sha256, like shasum -a 256.
func sha256File(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// jqField reads one string field from a JSON file ("" when absent).
func jqField(path, field string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		return ""
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		return ""
	}
	s := jq.StrOf(m[field])
	return s
}

// exists reports whether path exists.
func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// isDir reports whether path is a directory.
func isDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}
