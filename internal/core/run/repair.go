package run

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
	"github.com/stainless-code/verdict/internal/core/repair"
)

const (
	// codeRepairExhausted is the refusal when every round left an invalid
	// review: the loop spent its round cap without reaching a valid file.
	codeRepairExhausted = "repair-exhausted"
	// minRounds is the one-shot default; maxRounds caps the launch-to-check
	// loop so a never-valid agent cannot bill unbounded work.
	minRounds = 1
	maxRounds = 5
	// maxRoundReviewBytes caps one round's review read, the same 32 MiB
	// record's agent-input cap uses.
	maxRoundReviewBytes = 32 << 20
)

// rounds parses the raw --rounds value: a total launch-to-check round count
// between 1 and 5. Anything else is a bad-flags refusal, the same family a
// missing --agent uses.
func (st *state) rounds() (int, *core.RefusalError) {
	raw := st.req.RoundsRaw
	if raw == "" {
		raw = "1"
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < minRounds || n > maxRounds || !digits(raw) {
		return 0, st.refuse(codeBadFlags, fmt.Sprintf("run --rounds must be 1-5, got '%s'", st.req.RoundsRaw))
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
	bin, ref := st.resolve(args.def)
	if ref != nil {
		return nil, ref
	}
	started := st.now()
	rec := &launcher{
		SchemaVersion: launcherSchema,
		Agent:         args.def.Name,
		Binary:        bin,
		Bundle:        args.bundle,
		Artifact:      args.artifact,
		StartedAt:     core.NowUTC(started),
		Timeout:       args.timeout,
	}
	rec.Argv, st.argSecrets = recordArgv(args.argv, args.prompt, st.req.Environ)
	st.clearRoundFiles(args.dir)
	rep := &core.RunRepair{Rounds: n, Attempts: []core.RunAttempt{}}
	var prior []byte
	var lastRes *core.Result
	var lastArt artifactState
	for k := 1; k <= n; k++ {
		// The launch record's mtime is this round's freshness baseline: it
		// comes from the same filesystem clock, at the same granularity, as the
		// artifact's mtime. A process clock runs ahead of a whole-second
		// filesystem's stamps and refuses a review the agent did write.
		launched, writeRef := writeLauncher(args.dir+"/"+launcherFile, rec)
		if writeRef != nil {
			return nil, writeRef
		}
		resp, stop, ref, review, res, art := st.repairRound(ctx, args, rec, rep, started, launched, prior, k, n)
		lastRes, lastArt = res, art
		if stop {
			return resp, ref
		}
		prior = review
	}
	resp, ref, _, _ := st.repairRefusal(args, rec, rep, started, lastRes, lastArt, false)
	return resp, ref
}

// repairRound runs one launch-to-check round and its shape check, launched
// being the modification time the round's launch record just got. stop ends
// the loop: a valid review, any launch or artifact failure, and a round that
// repeats the prior bytes all stop here. review is the bytes this round left,
// for the next round's no-progress comparison.
func (st *state) repairRound(ctx context.Context, args *launchArgs, rec *launcher, rep *core.RunRepair,
	started, launched time.Time, prior []byte, k, n int,
) (resp *core.RunResponse, stop bool, ref *core.RefusalError, review []byte, res *core.Result, art artifactState) {
	prompt := args.prompt
	if k > 1 {
		prompt = repair.RoundPrompt(args.prompt, repair.Nonce(), prior, k, n, rep.Attempts[len(rep.Attempts)-1].Problems, args.artifact)
	}
	argv := argvFor(args.def, st.req.PassThrough, prompt)
	st.human(fmt.Sprintf("» round %d/%d: %s (timeout %ds)", k, n, strings.Join(rec.Argv, " "), args.timeout))
	roundStarted := st.now()
	roundRes, spawnErr := st.spawn(ctx, argv, args.timeout)
	wallS := st.now().Sub(roundStarted).Seconds()
	st.writeRoundOut(args.dir, k, roundRes)
	roundArt := inspectArtifact(args.abs, launched)
	if ref := st.verdict(ctx, args, roundRes, spawnErr, roundArt); ref != nil {
		st.writeCrashRoundReview(args, k)
		resp, ref, res, art := st.roundFailed(args, rec, rep, started, roundRes, roundArt, ref)
		return resp, true, ref, nil, res, art
	}
	review, readRef := readRoundReview(args, roundRes)
	if readRef != nil {
		resp, ref, res, art := st.roundFailed(args, rec, rep, started, roundRes, roundArt, readRef)
		return resp, true, ref, nil, res, art
	}
	check, checkRef := repair.Check(args.dir, review)
	if checkRef != nil {
		resp, ref, res, art := st.roundFailed(args, rec, rep, started, roundRes, roundArt, checkRef)
		return resp, true, ref, nil, res, art
	}
	problems := repair.Problems(check)
	st.writeRoundFiles(args.dir, k, review, check)
	rep.Attempts = append(rep.Attempts, core.RunAttempt{
		Round:        k,
		PromptSHA256: shaHex([]byte(prompt)),
		ReviewSHA256: shaHex(review),
		CheckOK:      repair.Valid(check),
		Problems:     problems,
		Truncated:    check.Truncated,
		WallS:        wallS,
	})
	if k > 1 && bytes.Equal(review, prior) {
		resp, ref, res, art := st.repairRefusal(args, rec, rep, started, roundRes, roundArt, true)
		return resp, true, ref, nil, res, art
	}
	if repair.Valid(check) {
		rep.RoundsToValid = k
		rep.FinalValid = true
		resp, ref, kept := st.roundSucceeded(args, rec, rep, started, roundRes, roundArt, review)
		return resp, true, ref, kept, roundRes, roundArt
	}
	return nil, false, nil, review, roundRes, roundArt
}

// roundSucceeded ends the loop on the first valid review: the outcome is
// recorded and the handoff names the command the launcher did not run. A
// failed outcome write refuses like the one-shot path, since no other
// refusal competes on this path.
func (st *state) roundSucceeded(args *launchArgs, rec *launcher, rep *core.RunRepair,
	started time.Time, res *core.Result, art artifactState, review []byte,
) (*core.RunResponse, *core.RefusalError, []byte) {
	ended := st.now()
	if writeRef := st.recordOutcome(rec, args.dir, ended, started, res, art, rep); writeRef != nil {
		return st.refused(writeRef, res, ended), writeRef, nil
	}
	return st.response(args, rec, res, art, started, ended, rep), nil, review
}

// roundFailed ends the loop on a launch or artifact failure with the existing
// codes: the outcome so far is recorded and no further round runs.
func (st *state) roundFailed(args *launchArgs, rec *launcher, rep *core.RunRepair,
	started time.Time, res *core.Result, art artifactState, ref *core.RefusalError,
) (*core.RunResponse, *core.RefusalError, *core.Result, artifactState) {
	ended := st.now()
	if writeRef := st.recordOutcome(rec, args.dir, ended, started, res, art, rep); writeRef != nil {
		st.warn(writeRef.Message)
	}
	return st.refused(ref, res, ended), ref, res, art
}

// repairRefusal ends a never-valid run: the repair-exhausted refusal carrying
// the final round's problems, with the no-progress cause when the last round
// repeated the prior bytes instead of spending the cap.
func (st *state) repairRefusal(args *launchArgs, rec *launcher, rep *core.RunRepair,
	started time.Time, res *core.Result, art artifactState, noProgress bool,
) (*core.RunResponse, *core.RefusalError, *core.Result, artifactState) {
	last := core.RunAttempt{}
	if len(rep.Attempts) > 0 {
		last = rep.Attempts[len(rep.Attempts)-1]
	}
	errors := 0
	for _, p := range last.Problems {
		if p.Level == core.ProblemLevelError {
			errors++
		}
	}
	var msg string
	if noProgress {
		msg = fmt.Sprintf("repair stopped after round %d/%d: review.json is byte-identical to round %d (no progress) "+
			"with %d error(s) — fix every error, then re-run", last.Round, rep.Rounds, last.Round-1, errors)
	} else {
		msg = fmt.Sprintf("repair exhausted after %d round(s): review.json still has %d error(s) — "+
			"fix every error, then re-run", rep.Rounds, errors)
	}
	ref := &core.RefusalError{
		Code:              codeRepairExhausted,
		Message:           msg,
		Problems:          last.Problems,
		ProblemsTruncated: last.Truncated,
	}
	ended := st.now()
	if writeRef := st.recordOutcome(rec, args.dir, ended, started, res, art, rep); writeRef != nil {
		st.warn(writeRef.Message)
	}
	return st.refused(ref, res, ended), ref, res, art
}

// readRoundReview reads the artifact the verdict already accepted, capped at
// the 32 MiB agent-input cap record uses. A read failure after a passing
// stat keeps the artifact family: the contract path did not deliver bytes.
func readRoundReview(args *launchArgs, res *core.Result) ([]byte, *core.RefusalError) {
	f, err := os.Open(args.abs)
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

// clearRoundFiles removes stale per-round evidence before the loop starts,
// so a shorter run never inherits a longer run's numbered files.
func (st *state) clearRoundFiles(dir string) {
	rounded := filepath.Join(dir, roundsDir)
	entries, err := os.ReadDir(rounded)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
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
func (st *state) writeRoundOut(dir string, k int, res *core.Result) {
	if res == nil {
		return
	}
	rounded := filepath.Join(dir, roundsDir)
	if err := os.MkdirAll(rounded, roundsDirMode); err != nil {
		st.warn(fmt.Sprintf("could not keep round-%d evidence: %v", k, err))
		return
	}
	if err := atomic.WriteFileMode(filepath.Join(rounded, fmt.Sprintf("%02d.out", k)),
		[]byte(res.Stdout+res.Stderr), runOutputFileMode); err != nil {
		st.warn(fmt.Sprintf("could not keep round-%d output: %v", k, err))
	}
}

// writeCrashRoundReview keeps the review bytes a failed round left, when the
// file exists: evidence for a crash, timeout, or artifact failure that stops
// the loop before the shape check.
func (st *state) writeCrashRoundReview(args *launchArgs, k int) {
	raw, err := os.ReadFile(args.abs)
	if err != nil {
		return
	}
	rounded := filepath.Join(args.dir, roundsDir)
	if err := os.MkdirAll(rounded, roundsDirMode); err != nil {
		st.warn(fmt.Sprintf("could not keep round-%d evidence: %v", k, err))
		return
	}
	if err := atomic.WriteFile(filepath.Join(rounded, fmt.Sprintf("%02d.review.json", k)), raw); err != nil {
		st.warn(fmt.Sprintf("could not keep round-%d review bytes: %v", k, err))
	}
}

// writeRoundFiles keeps one round's evidence beside the final review: the
// exact bytes the agent left and the checker's verdict on them. A write
// failure warns, never refuses: the review file is the contract, these files
// are the receipt.
func (st *state) writeRoundFiles(dir string, k int, review []byte, check recordcore.CheckResult) {
	rounded := filepath.Join(dir, roundsDir)
	if err := os.MkdirAll(rounded, roundsDirMode); err != nil {
		st.warn(fmt.Sprintf("could not keep round-%d evidence: %v", k, err))
		return
	}
	base := filepath.Join(rounded, fmt.Sprintf("%02d", k))
	if err := atomic.WriteFile(base+".review.json", review); err != nil {
		st.warn(fmt.Sprintf("could not keep round-%d review bytes: %v", k, err))
	}
	raw, err := json.MarshalIndent(check, "", "  ")
	if err != nil {
		st.warn(fmt.Sprintf("could not render the round-%d check: %v", k, err))
		return
	}
	if err := atomic.WriteFile(base+".check.json", raw); err != nil {
		st.warn(fmt.Sprintf("could not keep the round-%d check: %v", k, err))
	}
}

// shaHex digests one prompt or review for the attempt record.
func shaHex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
