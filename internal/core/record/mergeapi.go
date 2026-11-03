// Package recordcore is the record domain service: `record` as a typed
// (ctx, request) → (response, refusal) call plus the validate_and_merge
// engine the post verb shares. It owns the merge, the typed
// findings/record stores, and the atomic writes; internal/record owns argv
// parsing, stream writes, and exit mapping.
package recordcore

import (
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/diagram"
	"github.com/stainless-code/verdict/internal/hunks"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// MergeParams carries one shared merge invocation: the record and post
// verbs run the same validate_and_merge core over different bodies (the
// review body, or the drift-annotated post body with the hosting split
// hook), persisting to different findings files (dry runs stage to
// findings.dryrun.json).
type MergeParams struct { //nolint:govet // field grouping documents the merge contract
	Dir          string
	Prov         string
	HeadFull     string
	Review       *jq.Obj
	ReviewFile   string
	Root         string
	Body         string
	DryRun       bool
	FindingsFile string
	PreFooter    func(string) string
	Effective    core.Effective
	Forces       *core.GlobalForces
	Repo         string
	Pr           string
	Cmd          string
}

// MergeResult carries the shared merge outputs both verbs need.
type MergeResult struct { //nolint:govet // field grouping documents the merge contract
	Body         string
	Diagram      diagram.Status
	Entries      []any
	Kept         []any
	NKept        int
	NMoved       int
	HunkN        int
	Marker       string
	Dir          string
	FindingsFile string
	Confidence   any
	Security     any
	Warnings     []string
}

// Merge runs the shared validate_and_merge core: the merge engine, the
// findings write, KB credit staging, and the anchor/findings report. The
// record verb discards the curated body and finalizes the record artifact;
// the post verb carries the body into the payload. Errors are typed
// refusals matched with [errors.As] at the edge.
func Merge(rt *core.Runtime, in *MergeParams) (MergeResult, error) {
	st := &state{
		review:       in.Review,
		forces:       in.Forces,
		preFooter:    in.PreFooter,
		progress:     rt.Progress,
		effective:    in.Effective,
		cmd:          in.Cmd,
		repo:         in.Repo,
		pr:           in.Pr,
		env:          rt.Env,
		now:          rt.TimeUTC(),
		findingsFile: in.FindingsFile,
		dir:          in.Dir,
		reviewFile:   in.ReviewFile,
		root:         in.Root,
		headFull:     in.HeadFull,
		headShort:    shortSHA(in.HeadFull),
		prov:         in.Prov,
		dryRun:       in.DryRun,
	}
	curated, dstat, err := st.validateAndMerge(in.Body)
	if err != nil {
		return MergeResult{}, err
	}
	if err := writeFindings(st); err != nil {
		return MergeResult{}, err
	}
	st.kb.CommitStaged(st.findingsFile, st.now)
	reportFindings(st)
	if st.hunkN == 0 {
		st.warn("no hunks indexed — anchors unchecked, everything kept")
	}
	return MergeResult{
		Body:         curated,
		Diagram:      dstat,
		Entries:      st.entries,
		Kept:         st.kept,
		NKept:        st.nKept,
		NMoved:       st.nMoved,
		HunkN:        st.hunkN,
		Marker:       st.marker,
		Dir:          st.dir,
		FindingsFile: st.findingsFile,
		Confidence:   st.confidence,
		Security:     st.security,
		Warnings:     st.warnings,
	}, nil
}

// reportFindings emits the human findings summary table.
func reportFindings(st *state) {
	st.human(fmt.Sprintf("» findings: %d sigs -> %s", len(st.entries), st.findingsFile))
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
		st.human(fmt.Sprintf("  %s  %s  %s  %s",
			sigPrefix(jq.Interp(sv)),
			jq.Interp(tv),
			jq.Interp(av),
			jq.Interp(jq.Alt(jq.Alt(pv, hv), "?")),
		))
	}
}

// LoadReview reads and gates a review.json file: the size cap, the JSON
// shape, and the degenerate-body bar. Both verbs share it; only the bundle
// gates around it differ.
func LoadReview(path string) (*jq.Obj, error) {
	reviewRaw, err := readAgentFile(path)
	if err != nil {
		if ref := agentRefusal(err); ref != nil {
			return nil, ref
		}
		return nil, &core.RefusalError{Code: codeMissingReviewFile, Message: fmt.Sprintf(msgReviewMissing, path)}
	}
	reviewDoc, err := jq.DecodeOrdered(reviewRaw)
	if err != nil {
		return nil, &core.RefusalError{Code: codeBadReviewInput, Message: msgBadReviewJSON}
	}
	review, ok := reviewDoc.(*jq.Obj)
	if !ok {
		return nil, &core.RefusalError{Code: codeBadReviewSchema, Message: msgReviewNotObject}
	}
	if err := checkDegenerate(reviewBodyOf(review)); err != nil {
		return nil, err
	}
	return review, nil
}

// reviewBodyOf reads the review body for the merge, mirroring the shell's
// `jq -r '.body // ""'` (containers pretty-print via jq.R).
func reviewBodyOf(review *jq.Obj) string {
	bodyRaw, _ := review.Get("body")
	return jq.R(jq.Alt(bodyRaw, ""))
}

// EnsureStore ports ensure_store: backfill the store marker when absent.
func EnsureStore(dir string) error {
	if exists(dir + "/store.json") {
		return nil
	}
	return atomic.WriteFile(dir+"/store.json", []byte("{\n  \"schema_version\": \"1\"\n}\n"))
}

// EnsureHunksSnapshot ports ensure_hunks_snapshot: rebuild from full.diff
// when the fetch-time snapshot is missing, warning that validation is
// best-effort.
func EnsureHunksSnapshot(rt core.Runtime, dir string) error {
	if exists(dir + "/hunks.tsv") {
		return nil
	}
	rt.Warn(fmt.Sprintf(msgHunksRebuild, dir))
	raw, err := os.ReadFile(dir + "/full.diff")
	if err != nil {
		return &core.RefusalError{Code: "missing-diff", Message: fmt.Sprintf(msgMissingDiff, dir)}
	}
	return atomic.WriteFile(dir+"/hunks.tsv", []byte(hunks.Format(hunks.Parse(string(raw)))))
}

// ProvHead reads the provider head SHA via the normalized field rules.
func ProvHead(dir string) string {
	raw, ok := ReadOptional(dir + prSuffix)
	if !ok {
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
		return jq.StrOf(m["headRefOid"])
	}
	return jq.StrOf(m["head_sha"])
}

// CountStatus counts entries with status.
func CountStatus(entries []any, status string) int {
	n := 0
	for _, e := range entries {
		if m, ok := e.(*jq.Obj); ok {
			if s, _ := m.Get(fieldStatus); s == status {
				n++
			}
		}
	}
	return n
}

// ReadOptional reads an optional input file; ok is false when missing or
// unreadable (absent inputs read as absent claims/priors, never failures).
func ReadOptional(path string) ([]byte, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	return raw, true
}

// maxAgentBytes caps one agent-written input (review.json,
// verification.json, chunk manifest) at 32 MiB. Go-only defense-in-depth
// (R3): the Go binary is the only reader, so a bounded read keeps a hostile
// bundle from exhausting memory. Unknown keys are never rejected — decoding
// stays open.
const maxAgentBytes = 32 << 20

// readAgentFile reads one agent-written input through a limiting reader, so
// at most maxAgentBytes+1 bytes are ever allocated. Stat refuses an
// obviously oversized file without reading it, but the limiter is the
// contract: a file that grows after the stat still stops at the cap.
// Missing/unreadable files report the sealed read error; over-cap files
// report the same typed refusal as before (callers distinguish it with
// [errors.As]).
func readAgentFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		// Sealed at the layer boundary (GO-ERRORS-03, R4); callers only
		// distinguish over-cap refusals via errors.As.
		return nil, jq.Sealed("read agent file", err)
	}
	defer func() { _ = f.Close() }() // read-only handle; the read error rules
	if fi, statErr := f.Stat(); statErr == nil && fi.Size() > maxAgentBytes {
		return nil, agentCapRefusal(path)
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxAgentBytes+1))
	if err != nil {
		return nil, jq.Sealed("read agent file", err)
	}
	if len(raw) > maxAgentBytes {
		return nil, agentCapRefusal(path)
	}
	return raw, nil
}

// agentCapRefusal builds the frozen over-cap refusal for an agent input.
func agentCapRefusal(path string) error {
	return &core.RefusalError{Code: codeBadReviewInput, Message: path +
		" exceeds the 32 MiB agent-input cap (shrink the review, then re-run)"}
}

// agentRefusal reports whether err is an over-cap refusal from readAgentFile.
func agentRefusal(err error) error {
	if ref, ok := errors.AsType[*core.RefusalError](err); ok {
		return ref
	}
	return nil
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

// exists reports whether path exists.
func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
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

// checkDegenerate ports the degenerate-body guard: a verdict marker exempts
// any body; otherwise short bodies refuse, and short placeholder-looking
// bodies refuse too.
func checkDegenerate(body string) error {
	if markerRe.MatchString(body) {
		return nil
	}
	if len(body) < degenerateFloor {
		return &core.RefusalError{Code: "degenerate-body", Message: "refusing degenerate body (<20 chars, no verdict marker)."}
	}
	if len(body) < placeholderFloor && asciiContainsLower(body, placeholderRe) {
		return &core.RefusalError{Code: "degenerate-body", Message: "refusing placeholder-looking short body."}
	}
	return nil
}

// asciiContainsLower reports a case-insensitive ASCII match, mirroring grep
// -i under the C locale.
func asciiContainsLower(s string, re *regexp.Regexp) bool {
	return re.MatchString(readcommon.ASCIILower(s))
}

var (
	markerRe      = regexp.MustCompile(`(?m)^> (✅|ℹ️|\[!IMPORTANT\]|\[!CAUTION\])`)
	placeholderRe = regexp.MustCompile(`test|testing|placeholder|temp|foo|bar|asdf|dummy|sample`)
)
