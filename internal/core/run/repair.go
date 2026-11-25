package run

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
	"github.com/stainless-code/verdict/internal/core/repair"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/noblock"
	"github.com/stainless-code/verdict/internal/readcommon"
)

const (
	// codeRepairExhausted is the refusal when every round left an invalid
	// review: the loop spent its round cap without reaching a valid file.
	codeRepairExhausted = "repair-exhausted"
	// codeRoundsPathUnsafe is the refusal when the bundle's .run.rounds entry
	// is not a plain directory: ReadDir, Remove, and MkdirAll all follow a
	// symlink, so cleanup would delete and the writers would write files
	// outside the bundle.
	codeRoundsPathUnsafe = "rounds-path-unsafe"
	// maxRoundReviewBytes caps one round's review read, the same 32 MiB
	// record's agent-input cap uses.
	maxRoundReviewBytes = 32 << 20
	// quoteVerified is the standing the evidence receipt gives a quote that
	// matched the bundle diff; every other standing is a gap.
	quoteVerified = "verified"
)

// MinRounds and MaxRounds bound --rounds: two rounds by default, so the one
// repair round the evidence contract depends on always happens, and a cap so a
// never-valid agent cannot bill unbounded work.
const (
	MinRounds = 1
	MaxRounds = 5
	// defaultRounds is the round count when --rounds is absent: the first
	// round plus the one targeted repair round a wrong quote or a missing hunk
	// receipt needs.
	defaultRounds = 2
)

// rounds parses the raw --rounds value: a total launch-to-check round count
// between 1 and 5, two when the flag is absent. Anything else is a bad-flags
// refusal, the same family a missing --agent uses.
func (st *state) rounds() (int, *core.RefusalError) {
	raw := st.req.RoundsRaw
	if raw == "" {
		raw = strconv.Itoa(defaultRounds)
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < MinRounds || n > MaxRounds || !digits(raw) {
		return 0, st.refuse(codeBadFlags, fmt.Sprintf("run --rounds must be %d-%d, got '%s'",
			MinRounds, MaxRounds, st.req.RoundsRaw))
	}
	return n, nil
}

// repairLoop runs up to n launch-to-check rounds against one bundle. Round 1
// sends the one-shot directive unchanged; later rounds append the prior
// review bytes and the shape checker's fix list under a fresh per-round
// nonce. Each round rewrites the launch record before it spawns, so the
// freshness assertion measures that round's launch. The loop stops on the
// first valid round, on any launch or artifact failure (the existing codes, no
// further rounds), and on a round that repeats the prior bytes. A never-valid
// run leaves the last attempt's review.json in place.
func (st *state) repairLoop(ctx context.Context, args *launchArgs, n int) (*core.RunResponse, *core.RefusalError) {
	bin, ref := st.resolve(&args.def)
	if ref != nil {
		return nil, ref
	}
	started := st.now()
	rec := st.launchRecord(args, bin, started)
	argv, argSecrets := st.displayArgv(args)
	rec.Argv = argv
	if ref := st.clearRoundFiles(args); ref != nil {
		return nil, ref
	}
	rep := &core.RunRepair{Rounds: n, Attempts: []core.RunAttempt{}}
	var in roundState
	var last roundOutcome
	for k := 1; k <= n; k++ {
		// The launch record's mtime is this round's freshness baseline: it
		// comes from the same filesystem clock, at the same granularity, as the
		// artifact's mtime. A process clock runs ahead of a whole-second
		// filesystem's stamps and refuses a review the agent did write.
		launched, writeRef := writeLauncher(args.dir+"/"+launcherFile, rec)
		if writeRef != nil {
			return nil, writeRef
		}
		last = st.repairRound(ctx, args, rec, rep, started, launched, in, k, n, argSecrets)
		if last.stop {
			return last.resp, last.ref
		}
		in = last.next
	}
	resp, ref := st.repairRefusal(args, rec, rep, started, &last, argSecrets)
	return resp, ref
}

// roundState is what round k inherits from round k-1: the quote locators its
// re-prompt asked to fix, which bounds every quote to one targeted repair
// round, and the bytes it left, for the no-progress comparison and the
// re-prompt's prior section.
type roundState struct {
	asked map[string]bool
	prior []byte
}

// roundOutcome is one round's answer to the loop. stop ends the loop with resp
// and ref; next carries the state the following round reads.
type roundOutcome struct {
	resp   *core.RunResponse
	ref    *core.RefusalError
	review []byte
	res    *core.Result
	art    artifactState
	next   roundState
	// check is the checker's verdict as produced; work is the loop's view of
	// it, after the quote budget demoted the problems it settled. The final
	// refusal reads check: a settled quote stops being asked about, but the
	// evidence it leaves behind is still missing.
	check      recordcore.CheckResult
	work       recordcore.CheckResult
	noProgress bool
	stop       bool
}

// repairRound runs one launch-to-check round and its shape check, launched
// being the modification time the round's launch record just got. stop ends
// the loop: a valid review, any launch or artifact failure, and a round that
// repeats the prior bytes all stop here. review is the bytes this round left,
// for the next round's no-progress comparison.
func (st *state) repairRound(ctx context.Context, args *launchArgs, rec *launcher, rep *core.RunRepair,
	started, launched time.Time, in roundState, k, n int, argSecrets []string,
) roundOutcome {
	if ref := args.checkpoint(); ref != nil {
		return roundOutcome{stop: true, ref: ref}
	}
	prompt := st.roundPrompt(args, rep, in, k, n)
	st.human(fmt.Sprintf("» round %d/%d: %s (timeout %ds)", k, n, strings.Join(rec.Argv, " "), args.timeout))
	if ref := st.admit(ctx, k); ref != nil {
		return roundOutcome{stop: true, ref: ref}
	}
	roundStarted := st.now()
	spawn, spawnErr := st.spawnAgent(ctx, args, prompt)
	roundRes := spawn.res
	st.recordSpend(args, k, spawn, spawnErr)
	if ref := args.checkpoint(); ref != nil {
		return roundOutcome{stop: true, ref: ref}
	}
	wallS := st.now().Sub(roundStarted).Seconds()
	st.writeRoundOut(args, k, roundRes)
	st.maybeSalvage(ctx, args, rec, spawn, spawnErr)
	roundArt := inspectArtifact(args.abs, launched)
	if ref := st.verdict(ctx, args, roundRes, spawnErr, roundArt); ref != nil {
		st.writeCrashRoundReview(args, k)
		return st.stopped(args, rec, rep, started, roundRes, roundArt, ref, argSecrets)
	}
	review, readRef := readRoundReview(args, roundRes)
	if readRef != nil {
		return st.stopped(args, rec, rep, started, roundRes, roundArt, readRef, argSecrets)
	}
	check, checkRef := args.checkReview(review)
	if checkRef != nil {
		return st.stopped(args, rec, rep, started, roundRes, roundArt, checkRef, argSecrets)
	}
	return st.checkedRound(args, rec, rep, started, roundRes, roundArt, in, &roundBytes{
		review: review, check: check, prompt: prompt, wallS: wallS,
	}, k, argSecrets)
}

// roundBytes is one round's checked output, as the stop decision reads it.
type roundBytes struct {
	prompt string
	review []byte
	check  recordcore.CheckResult
	wallS  float64
}

// checkedRound applies the round's evidence budget and decides whether the loop
// stops: a byte-identical review, a review that is ready to publish, or nothing
// left to repair.
func (st *state) checkedRound(args *launchArgs, rec *launcher, rep *core.RunRepair,
	started time.Time, res *core.Result, art artifactState, in roundState, out *roundBytes, k int,
	argSecrets []string,
) roundOutcome {
	// Settle spends the one-round quote budget before anything reads the
	// result: a quote the previous round asked about and this round still
	// cannot verify is permanently unverified, so its problems drop to
	// warnings and no later round pays for it again.
	effective, settled := repair.Settle(out.check, in.asked)
	st.writeRoundFiles(args, k, out.review, out.check)
	rep.Attempts = append(rep.Attempts, core.RunAttempt{
		Round:        k,
		PromptSHA256: shaHex([]byte(out.prompt)),
		ReviewSHA256: shaHex(out.review),
		Evidence:     evidenceSummary(effective, settled),
		CheckOK:      publishable(effective),
		Problems:     repair.Problems(effective),
		Truncated:    effective.Truncated,
		WallS:        out.wallS,
	})
	next := roundState{prior: out.review, asked: repair.RepairableQuotes(effective, settled)}
	last := roundOutcome{review: out.review, res: res, art: art, next: next, check: out.check, work: effective}
	if k > 1 && bytes.Equal(out.review, in.prior) {
		last.noProgress, last.stop = true, true
		last.resp, last.ref = st.repairRefusal(args, rec, rep, started, &last, argSecrets)
		return last
	}
	if !repair.Valid(effective) {
		return last
	}
	// A well-shaped review can still leave a gating quote unverified, a hunk
	// unreceipted, or a spec item unchecked: that is the run's
	// review-insufficient refusal, not a success.
	if ref := repair.InsufficientRefusal(out.check, ""); ref != nil {
		return st.stopped(args, rec, rep, started, res, art, ref, argSecrets)
	}
	st.publishRound(rep, k)
	last.stop = true
	last.resp, last.ref, last.review = st.roundSucceeded(args, rec, rep, started, res, art, out.review, argSecrets)
	return last
}

// publishRound marks the repair profile's outcome: the round the loop reached
// a valid review in, and that the final round is the one that did.
func (st *state) publishRound(rep *core.RunRepair, k int) {
	rep.RoundsToValid = k
	rep.FinalValid = true
}

// roundPrompt renders the directive round k sends: round 1 keeps the base
// directive byte-identical, and later rounds append the prior review and the
// fix list.
func (st *state) roundPrompt(args *launchArgs, rep *core.RunRepair, in roundState, k, n int) string {
	if k == 1 {
		return args.prompt
	}
	return repair.RoundPrompt(args.prompt, repair.Nonce(), in.prior, k, n,
		rep.Attempts[len(rep.Attempts)-1].Problems, args.artifact)
}

// stopped ends the loop on one round's refusal, recording the outcome first so
// the launcher says how far the loop got.
func (st *state) stopped(args *launchArgs, rec *launcher, rep *core.RunRepair,
	started time.Time, res *core.Result, art artifactState, ref *core.RefusalError, argSecrets []string,
) roundOutcome {
	resp, ref, res, art := st.roundFailed(args, rec, rep, started, res, art, ref, argSecrets)
	return roundOutcome{resp: resp, ref: ref, res: res, art: art, stop: true}
}

// evidenceSummary renders one round's evidence verdict for the attempt record:
// the coverage counts, the error count, the quotes this round settled, and
// whether the receipt withholds approval. It reports nothing for a round whose
// bytes never decoded, since an undecodable review carries no findings to
// verify.
func evidenceSummary(check recordcore.CheckResult, settled []string) *core.RunEvidence {
	if check.Evidence == nil {
		return nil
	}
	ev := check.Evidence.Receipt
	if ev == nil {
		return nil
	}
	return &core.RunEvidence{
		Settled:      settled,
		QuoteGaps:    unverifiedQuotes(ev.Quotes),
		ReceiptGaps:  ev.Receipts.Total - ev.Receipts.Covered,
		SpecGaps:     ev.Spec.Total - ev.Spec.Covered,
		Errors:       check.Evidence.Errors,
		Insufficient: ev.Insufficient,
	}
}

// publishable reports whether one round left a review the run can hand off:
// no shape error, no evidence error, and no receipt gap withholding approval.
func publishable(check recordcore.CheckResult) bool {
	ev := check.Evidence
	return repair.Valid(check) && (ev == nil || ev.Receipt == nil || !ev.Receipt.Insufficient)
}

// unverifiedQuotes counts the receipt's quotes that did not match the diff.
func unverifiedQuotes(quotes []core.EvidenceQuote) int {
	n := 0
	for i := range quotes {
		if quotes[i].Status != quoteVerified {
			n++
		}
	}
	return n
}

// maybeSalvage fills an absent review from the round's stdout and records
// the repair on the same launcher the round outcome writes.
func (st *state) maybeSalvage(ctx context.Context, args *launchArgs, rec *launcher, out spawned, spawnErr error) {
	// The launcher describes the round that produced the final artifact, so
	// last round's salvage must never survive into this one.
	rec.Salvaged = false
	if st.drainDigest != "" || !salvageAllowed(ctx, out.res, spawnErr) {
		return
	}
	rec.Salvaged = st.salvageReview(args, st.agentText(out))
}

// roundSucceeded ends the loop on the first valid review: the outcome is
// recorded and the handoff names the command the launcher did not run. A
// failed outcome write refuses like the one-shot path, since no other
// refusal competes on this path.
func (st *state) roundSucceeded(args *launchArgs, rec *launcher, rep *core.RunRepair,
	started time.Time, res *core.Result, art artifactState, review []byte, argSecrets []string,
) (*core.RunResponse, *core.RefusalError, []byte) {
	ended := st.now()
	if writeRef := st.recordOutcome(rec, args.dir, ended, started, res, art, rep, argSecrets); writeRef != nil {
		return st.refused(writeRef, res, ended), writeRef, nil
	}
	return st.response(args, rec, res, art, started, ended, rep), nil, review
}

// roundFailed ends the loop on a launch or artifact failure with the existing
// codes: the outcome so far is recorded and no further round runs.
func (st *state) roundFailed(args *launchArgs, rec *launcher, rep *core.RunRepair,
	started time.Time, res *core.Result, art artifactState, ref *core.RefusalError, argSecrets []string,
) (*core.RunResponse, *core.RefusalError, *core.Result, artifactState) {
	ended := st.now()
	if writeRef := st.recordOutcome(rec, args.dir, ended, started, res, art, rep, argSecrets); writeRef != nil {
		st.warn(writeRef.Message)
	}
	return st.refused(ref, res, ended), ref, res, art
}

// repairRefusal ends a never-valid run: the repair-exhausted refusal carrying
// the final round's problems, with the no-progress cause when the last round
// repeated the prior bytes instead of spending the cap.
func (st *state) repairRefusal(args *launchArgs, rec *launcher, rep *core.RunRepair,
	started time.Time, last *roundOutcome, argSecrets []string,
) (*core.RunResponse, *core.RefusalError) {
	attempt := core.RunAttempt{}
	if len(rep.Attempts) > 0 {
		attempt = rep.Attempts[len(rep.Attempts)-1]
	}
	note := ""
	if last.noProgress {
		note = fmt.Sprintf("repair stopped after round %d/%d: review.json is byte-identical to round %d (no progress)",
			attempt.Round, rep.Rounds, attempt.Round-1)
	}
	// A document the shape check still rejects keeps its own code: the operator
	// has to fix JSON before any quote can be checked against it. Only a
	// well-shaped review whose evidence the bundle cannot confirm earns
	// review-insufficient, which is the run's refusal when evidence errors
	// survive the rounds.
	// Two branches cover every way the loop stops short of a valid review: a
	// shape error left, or an evidence gap left. Demotion only lowers a level,
	// so a round that ends invalid either keeps a shape error or keeps an
	// evidence error, and either one is a refusal below.
	ref := shapeRefusal(&attempt, last.check, rep.Rounds, last.noProgress, note)
	if ref == nil {
		ref = repair.InsufficientRefusal(last.check, note)
	}
	ended := st.now()
	if writeRef := st.recordOutcome(rec, args.dir, ended, started, last.res, last.art, rep, argSecrets); writeRef != nil {
		st.warn(writeRef.Message)
	}
	return st.refused(ref, last.res, ended), ref
}

// shapeRefusal is the never-valid refusal for shape-only problems, keeping the
// two codes the loop already published: a repeated review is repair-exhausted
// and a spent round cap is bad-review-input. It reports nil when the round has
// no shape errors left, which is where the evidence refusal takes over.
func shapeRefusal(attempt *core.RunAttempt, check recordcore.CheckResult, rounds int, noProgress bool, note string) *core.RefusalError {
	errorCount := check.ShapeErrors()
	if errorCount == 0 {
		return nil
	}
	if noProgress {
		return &core.RefusalError{
			Code:              codeRepairExhausted,
			Message:           fmt.Sprintf("%s with %d error(s) — fix every error, then re-run", note, errorCount),
			Problems:          attempt.Problems,
			ProblemsTruncated: attempt.Truncated,
		}
	}
	return &core.RefusalError{
		Code: recordcore.CodeBadReviewInput,
		Message: fmt.Sprintf("repair exhausted after %d round(s): review.json still has %d error(s) — "+
			"fix every error, then re-run", rounds, errorCount),
		Problems:          attempt.Problems,
		ProblemsTruncated: attempt.Truncated,
	}
}

// readRoundReview reads the artifact the verdict already accepted, capped at
// the 32 MiB agent-input cap record uses. A read failure after a passing
// stat keeps the artifact family: the contract path did not deliver bytes.
func readRoundReview(args *launchArgs, res *core.Result) ([]byte, *core.RefusalError) {
	f, err := noblock.OpenRegular(args.abs)
	if err != nil {
		return nil, &core.RefusalError{
			Code:    codeNoArtifact,
			Message: fmt.Sprintf("%s is unreadable after exit %d: %v", args.artifact, res.ExitCode, err),
		}
	}
	defer func() { _ = f.Close() }()
	if fi, statErr := f.Stat(); statErr == nil && fi.Size() > maxRoundReviewBytes {
		return nil, &core.RefusalError{
			Code:    codeNoArtifact,
			Message: fmt.Sprintf("%s exceeds the 32 MiB agent-input cap after exit %d", args.artifact, res.ExitCode),
		}
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxRoundReviewBytes+1))
	if err != nil {
		return nil, &core.RefusalError{
			Code:    codeNoArtifact,
			Message: fmt.Sprintf("%s is unreadable after exit %d: %v", args.artifact, res.ExitCode, err),
		}
	}
	if len(raw) > maxRoundReviewBytes {
		return nil, &core.RefusalError{
			Code:    codeNoArtifact,
			Message: fmt.Sprintf("%s exceeds the 32 MiB agent-input cap after exit %d", args.artifact, res.ExitCode),
		}
	}
	return raw, nil
}

// roundsPath resolves the bundle's per-round evidence directory. An entry that
// is not a plain directory refuses: ReadDir, Remove, and MkdirAll all follow a
// symlink, so a stale-file sweep would delete, and the writers would write,
// files outside the bundle.
func (st *state) roundsPath(args *launchArgs) (string, *core.RefusalError) {
	rounded := filepath.Join(args.dir, roundsDir)
	shown := args.bundle + "/" + roundsDir
	fi, err := os.Lstat(rounded)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return rounded, nil
	case err != nil:
		return "", st.refuse(codeRoundsPathUnsafe, fmt.Sprintf(
			"cannot inspect %s: %v — fix its permissions or remove it, then re-run", shown, err))
	case !fi.IsDir():
		kind := "is not a directory"
		if fi.Mode()&os.ModeSymlink != 0 {
			kind = "is a symlink"
		}
		return "", st.refuse(codeRoundsPathUnsafe, fmt.Sprintf(
			"%s %s, so round evidence would land outside the bundle — remove it, then re-run", shown, kind))
	}
	return rounded, nil
}

// roundTarget resolves and creates the evidence directory for one round's
// writer, warning instead of refusing: the review file is the contract, these
// files are the receipt.
func (st *state) roundTarget(args *launchArgs, k int) (string, bool) {
	rounded, ref := st.roundsPath(args)
	if ref != nil {
		st.warn(ref.Message)
		return "", false
	}
	if err := os.MkdirAll(rounded, roundsDirMode); err != nil {
		st.warn(fmt.Sprintf("could not keep round-%d evidence: %v", k, err))
		return "", false
	}
	return rounded, true
}

// roundEvidenceFile reports whether name is one the loop writes — NN.out,
// NN.review.json, NN.check.json — so cleanup never removes a name it did not
// create.
func roundEvidenceFile(name string) bool {
	num, ext, ok := strings.Cut(name, ".")
	if !ok || len(num) != 2 || !digits(num) {
		return false
	}
	return ext == "out" || ext == "review.json" || ext == "check.json" || ext == "evidence.json"
}

// clearRoundFiles removes stale per-round evidence before the loop starts,
// so a shorter run never inherits a longer run's numbered files. Only the
// names the loop writes are cleared: anything else under .run.rounds is the
// operator's and stays.
func (st *state) clearRoundFiles(args *launchArgs) *core.RefusalError {
	rounded, ref := st.roundsPath(args)
	if ref != nil {
		return ref
	}
	st.removeRoundEvidence(rounded)
	return nil
}

// removeRoundEvidence deletes the stale names the loop writes, warning when one
// cannot go: residue only makes the next round's evidence ambiguous, which is
// not worth stopping the run for.
func (st *state) removeRoundEvidence(rounded string) {
	entries, err := os.ReadDir(rounded)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !roundEvidenceFile(e.Name()) {
			continue
		}
		if err := os.Remove(filepath.Join(rounded, e.Name())); err != nil {
			st.warn(fmt.Sprintf("could not clear stale round file %s: %v", e.Name(), err))
			return
		}
	}
}

// writeRoundOut keeps one round's child output beside the final .run.out,
// which still carries only the last round. Crash rounds keep theirs too.
func (st *state) writeRoundOut(args *launchArgs, k int, res *core.Result) {
	if res == nil {
		return
	}
	rounded, ok := st.roundTarget(args, k)
	if !ok {
		return
	}
	if err := atomic.WriteFileMode(filepath.Join(rounded, fmt.Sprintf("%02d.out", k)),
		[]byte(res.Stdout+res.Stderr), roundEvidenceMode); err != nil {
		st.warn(fmt.Sprintf("could not keep round-%d output: %v", k, err))
	}
}

// crashReviewBytes returns the bytes a stopped round left at path. The open is
// the check, never a stat ahead of it: the child owns the path, so a regular
// file can become a FIFO between the two calls, and a path the child controls
// has no size ceiling. A file that does not open, is not regular once opened,
// or outgrew the review cap yields nothing to copy.
func crashReviewBytes(path string) ([]byte, bool) {
	f, err := readcommon.OpenNoBlock(path)
	if err != nil {
		return nil, false
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > maxRoundReviewBytes {
		return nil, false
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxRoundReviewBytes+1))
	if err != nil || len(raw) > maxRoundReviewBytes {
		return nil, false
	}
	return raw, true
}

// writeCrashRoundReview keeps the review bytes a failed round left, when they
// are safe to read: evidence for a crash, timeout, or artifact failure that
// stops the loop before the shape check. The round's own refusal is unaffected.
func (st *state) writeCrashRoundReview(args *launchArgs, k int) {
	raw, ok := crashReviewBytes(args.abs)
	if !ok {
		return
	}
	rounded, ok := st.roundTarget(args, k)
	if !ok {
		return
	}
	if err := atomic.WriteFileMode(filepath.Join(rounded, fmt.Sprintf("%02d.review.json", k)),
		raw, roundEvidenceMode); err != nil {
		st.warn(fmt.Sprintf("could not keep round-%d review bytes: %v", k, err))
	}
}

// writeRoundFiles keeps one round's evidence beside the final review: the
// exact bytes the agent left and the checker's verdict on them. A write
// failure warns, never refuses: the review file is the contract, these files
// are the receipt.
func (st *state) writeRoundFiles(args *launchArgs, k int, review []byte, check recordcore.CheckResult) {
	rounded, ok := st.roundTarget(args, k)
	if !ok {
		return
	}
	base := filepath.Join(rounded, fmt.Sprintf("%02d", k))
	if err := atomic.WriteFileMode(base+".review.json", review, roundEvidenceMode); err != nil {
		st.warn(fmt.Sprintf("could not keep round-%d review bytes: %v", k, err))
	}
	raw, err := jq.MarshalIndent(check, "", "  ")
	if err != nil {
		st.warn(fmt.Sprintf("could not render the round-%d check: %v", k, err))
		return
	}
	if err := atomic.WriteFileMode(base+".check.json", raw, roundEvidenceMode); err != nil {
		st.warn(fmt.Sprintf("could not keep the round-%d check: %v", k, err))
	}
	st.writeRoundEvidence(base, evidenceReceiptOf(check), k)
}

// evidenceReceiptOf returns the round's stored receipt, or nil when the bytes
// never decoded and no evidence pass ran.
func evidenceReceiptOf(check recordcore.CheckResult) *core.EvidenceReceipt {
	if check.Evidence == nil {
		return nil
	}
	return check.Evidence.Receipt
}

// writeRoundEvidence keeps the round's evidence receipt beside its check: the
// authoritative record of what the bundle could and could not verify, which a
// re-prompt truncated by its budget still points at.
func (st *state) writeRoundEvidence(base string, receipt *core.EvidenceReceipt, k int) {
	if receipt == nil {
		return
	}
	raw, err := jq.MarshalIndent(receipt, "", "  ")
	if err != nil {
		st.warn(fmt.Sprintf("could not render the round-%d evidence: %v", k, err))
		return
	}
	if err := atomic.WriteFileMode(base+".evidence.json", raw, roundEvidenceMode); err != nil {
		st.warn(fmt.Sprintf("could not keep the round-%d evidence: %v", k, err))
	}
}

// shaHex digests one prompt or review for the attempt record.
func shaHex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
