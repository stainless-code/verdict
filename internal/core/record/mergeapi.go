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
	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/coverage"
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
	RepoRoot     string
	Body         string
	DryRun       bool
	FindingsFile string
	PreFooter    func(string) string
	Effective    core.Effective
	Forces       *core.GlobalForces
	Repo         string
	Pr           string
	Cmd          string
	Controls     *controls.Snapshot
	Run          *config.RunState
	HunkRows     []hunks.Hunk
	InputChecked bool
	// RequireCoverage is the opt-in gate: absent or partial diff coverage
	// refuses coverage-incomplete. --force never bypasses it.
	RequireCoverage bool
}

// MergeResult carries the shared merge outputs both verbs need.
type MergeResult struct { //nolint:govet // field grouping documents the merge contract
	Body         string
	Diagram      diagram.Status
	Entries      []any
	Kept         []any
	KeptSigs     []string
	NKept        int
	NMoved       int
	HunkN        int
	Marker       string
	Dir          string
	FindingsFile string
	Confidence   any
	Security     any
	Coverage     *coverage.Summary
	Warnings     []string
}

func (in *MergeParams) repoRootPath() string {
	if in.RepoRoot == "" {
		return "."
	}
	return in.RepoRoot
}

// Merge runs the shared validate_and_merge core: the merge engine, the
// findings write, KB credit staging, and the anchor/findings report. The
// record verb discards the curated body and finalizes the record artifact;
// the post verb carries the body into the payload. Errors are typed
// refusals matched with [errors.As] at the edge.
func Merge(rt *core.Runtime, in *MergeParams) (MergeResult, error) {
	if in.Controls == nil {
		return MergeResult{}, &core.RefusalError{Code: controls.CodeUnavailable, Message: "merge needs a controls snapshot"}
	}
	st := &state{
		review:          in.Review,
		forces:          in.Forces,
		preFooter:       in.PreFooter,
		progress:        rt.Progress,
		effective:       in.Effective,
		cmd:             in.Cmd,
		repo:            in.Repo,
		pr:              in.Pr,
		env:             rt.Env,
		now:             rt.TimeUTC(),
		findingsFile:    in.FindingsFile,
		dir:             in.Dir,
		reviewFile:      in.ReviewFile,
		root:            in.Root,
		repoRoot:        in.repoRootPath(),
		headFull:        in.HeadFull,
		headShort:       readcommon.ShortSHA(in.HeadFull),
		prov:            in.Prov,
		dryRun:          in.DryRun,
		controls:        in.Controls,
		run:             in.Run,
		hunkRows:        in.HunkRows,
		inputChecked:    in.InputChecked,
		requireCoverage: in.RequireCoverage,
	}
	curated, dstat, err := st.validateAndMerge(in.Body)
	if err != nil {
		return MergeResult{}, err
	}
	if !st.dryRun || st.cmd != "record" {
		if err := writeFindings(st); err != nil {
			return MergeResult{}, err
		}
		st.kb.CommitStaged(st.findingsFile, st.now)
	}
	reportFindings(st)
	if st.hunkN == 0 {
		st.warn("no hunks indexed — anchors unchecked, everything kept")
	}
	return MergeResult{
		Body:         curated,
		Diagram:      dstat,
		Entries:      st.entries,
		Kept:         st.kept,
		KeptSigs:     st.keptSigs,
		NKept:        st.nKept,
		NMoved:       st.nMoved,
		HunkN:        st.hunkN,
		Marker:       st.marker,
		Dir:          st.dir,
		FindingsFile: st.findingsFile,
		Confidence:   st.confidence,
		Security:     st.security,
		Coverage:     st.coverage,
		Warnings:     st.warnings,
	}, nil
}

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
		pv, _ := m.Get(fieldPath)
		hv, _ := m.Get("heading")
		st.human(fmt.Sprintf("  %s  %s  %s  %s",
			readcommon.Prefix(jq.Interp(sv)),
			jq.Interp(tv),
			jq.Interp(av),
			jq.Interp(jq.Alt(jq.Alt(pv, hv), "?")),
		))
	}
}

// LoadReview reads and gates a review.json file: the size cap and the JSON
// object shape. It normalizes first, so post reads the same repaired object
// record checks and stores; record's dry-run data.normalized is exactly what
// post merges. record's checker owns content validity; post calls
// [CheckDegenerate] for its body bar.
func LoadReview(path string) (*jq.Obj, error) {
	reviewRaw, err := readAgentFile(path)
	if err != nil {
		if ref := agentRefusal(err); ref != nil {
			return nil, ref
		}
		return nil, &core.RefusalError{Code: codeMissingReviewFile, Message: fmt.Sprintf(msgReviewMissing, path)}
	}
	return LoadReviewBytes(reviewRaw)
}

// LoadReviewBytes decodes one already-read review.json document and keeps the
// record path's object and schema refusals. The bytes run through
// NormalizeReview first: normalization is idempotent, so record's
// already-normalized input decodes unchanged while post's raw file gains the
// same repairs record reports.
func LoadReviewBytes(reviewRaw []byte) (*jq.Obj, error) {
	normalized, _ := NormalizeReview(reviewRaw)
	reviewDoc, err := jq.DecodeOrdered(normalized)
	if err != nil {
		return nil, &core.RefusalError{Code: codeBadReviewInput, Message: msgBadReviewJSON}
	}
	review, ok := reviewDoc.(*jq.Obj)
	if !ok {
		return nil, &core.RefusalError{Code: codeBadReviewSchema, Message: msgReviewNotObject}
	}
	return review, nil
}

func reviewBodyOf(review *jq.Obj) string {
	bodyRaw, _ := review.Get("body")
	return jq.R(jq.Alt(bodyRaw, ""))
}

// EnsureStore backfills the store marker when absent.
func EnsureStore(dir string) error {
	if exists(dir + "/store.json") {
		return nil
	}
	return atomic.WriteFile(dir+"/store.json", []byte("{\n  \"schema_version\": \"1\"\n}\n"))
}

// EnsureHunksSnapshot rebuilds the hunk index from full.diff when the
// fetch-time snapshot is missing, warning that validation is best-effort.
func EnsureHunksSnapshot(rt core.Runtime, dir string) error {
	if exists(dir + "/hunks.tsv") {
		return nil
	}
	rt.Warn(fmt.Sprintf(msgHunksRebuild, dir))
	raw, err := os.ReadFile(dir + "/full.diff")
	if err != nil {
		return &core.RefusalError{Code: "missing-diff", Message: fmt.Sprintf(msgMissingDiff, dir)}
	}
	return atomic.WriteFile(dir+"/hunks.tsv", []byte(hunks.Snapshot(string(raw))))
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

const maxAgentBytes = 32 << 20

func readAgentFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
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

func agentCapRefusal(path string) error {
	return &core.RefusalError{Code: codeBadReviewInput, Message: path +
		" exceeds the 32 MiB agent-input cap (shrink the review, then re-run)"}
}

func agentRefusal(err error) error {
	if ref, ok := errors.AsType[*core.RefusalError](err); ok {
		return ref
	}
	return nil
}

func removeDerivedSnapshot(st *state) {
	if st.dryRun {
		return
	}
	if st.anchorHunks != st.dir+"/hunks.tsv" {
		removeBestEffort(st.anchorHunks)
	}
}

func removeBestEffort(path string) {
	_ = os.Remove(path)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// CheckDegenerate refuses a degenerate review body: shorter than the floor
// with no verdict marker, or a short placeholder-looking string.
func CheckDegenerate(body string) error {
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

func asciiContainsLower(s string, re *regexp.Regexp) bool {
	return re.MatchString(readcommon.ASCIILower(s))
}

var (
	markerRe      = regexp.MustCompile(`(?m)^> (✅|ℹ️|\[!IMPORTANT\]|\[!CAUTION\])`)
	placeholderRe = regexp.MustCompile(`test|testing|placeholder|temp|foo|bar|asdf|dummy|sample`)
)
