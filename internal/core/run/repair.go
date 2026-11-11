package run

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
// review bytes and the shape checker's fix list. The loop stops on the first
// valid round, on any launch or artifact failure (the existing codes, no
// further rounds), on a round that repeats the prior bytes, and when the cap
// runs out. A never-valid run leaves the last attempt's review.json in place.
func (st *state) repairLoop(ctx context.Context, args *launchArgs, n int) (*core.RunResponse, *core.RefusalError) {
	bin, ref := st.resolve(args.def)
	if ref != nil {
		return nil, ref
	}
	started := st.now()
	nonce := repair.Nonce()
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
	launched, ref := writeLauncher(args.dir+"/"+launcherFile, rec)
	if ref != nil {
		return nil, ref
	}
	rep := &core.RunRepair{Rounds: n, Attempts: []core.RunAttempt{}}
	var prior []byte
	for k := 1; k <= n; k++ {
		resp, stop, ref, review := st.repairRound(ctx, args, rec, rep, launched, started, nonce, prior, k, n)
		if stop {
			return resp, ref
		}
		prior = review
	}
	resp, _, ref, _ := st.repairRefusal(args, rec, rep, started, nil, artifactState{}, false)
	return resp, ref
}

// repairRound runs one launch-to-check round and its shape check. stop ends
// the loop: a valid review, any launch or artifact failure, a round that
// repeats the prior bytes, and the capped round all stop here. review is the
// bytes this round left, for the next round's no-progress comparison.
func (st *state) repairRound(ctx context.Context, args *launchArgs, rec *launcher, rep *core.RunRepair,
	launched, started time.Time, nonce string, prior []byte, k, n int,
) (resp *core.RunResponse, stop bool, ref *core.RefusalError, review []byte) {
	prompt := args.prompt
	if k > 1 {
		prompt = repair.RoundPrompt(args.prompt, nonce, prior, k, n, rep.Attempts[len(rep.Attempts)-1].Problems)
	}
	argv := argvFor(args.def, st.req.PassThrough, prompt)
	st.human(fmt.Sprintf("» round %d/%d: %s (timeout %ds)", k, n, strings.Join(rec.Argv, " "), args.timeout))
	roundStarted := st.now()
	res, spawnErr := st.spawn(ctx, argv, args.timeout)
	wallS := st.now().Sub(roundStarted).Seconds()
	art := inspectArtifact(args.abs, launched)
	if ref := st.verdict(ctx, args, res, spawnErr, art); ref != nil {
		return st.roundFailed(args, rec, rep, started, res, art, ref)
	}
	review, readRef := readRoundReview(args, res)
	if readRef != nil {
		return st.roundFailed(args, rec, rep, started, res, art, readRef)
	}
	check := repair.Check(args.dir, review)
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
		return st.repairRefusal(args, rec, rep, started, res, art, true)
	}
	if repair.Valid(check) {
		rep.RoundsToValid = k
		rep.FinalValid = true
		return st.roundSucceeded(args, rec, rep, started, res, art, review)
	}
	if k == n {
		return st.repairRefusal(args, rec, rep, started, res, art, false)
	}
	return nil, false, nil, review
}

// roundSucceeded ends the loop on the first valid review: the outcome is
// recorded and the handoff names the command the launcher did not run.
func (st *state) roundSucceeded(args *launchArgs, rec *launcher, rep *core.RunRepair,
	started time.Time, res *core.Result, art artifactState, review []byte,
) (*core.RunResponse, bool, *core.RefusalError, []byte) {
	ended := st.now()
	if writeRef := st.recordOutcome(rec, args.dir, ended, started, res, art, rep); writeRef != nil {
		st.warn(writeRef.Message)
	}
	return st.response(args, rec, res, art, started, ended, rep), true, nil, review
}

// roundFailed ends the loop on a launch or artifact failure with the existing
// codes: the outcome so far is recorded and no further round runs.
func (st *state) roundFailed(args *launchArgs, rec *launcher, rep *core.RunRepair,
	started time.Time, res *core.Result, art artifactState, ref *core.RefusalError,
) (*core.RunResponse, bool, *core.RefusalError, []byte) {
	ended := st.now()
	if writeRef := st.recordOutcome(rec, args.dir, ended, started, res, art, rep); writeRef != nil {
		st.warn(writeRef.Message)
	}
	return st.refused(ref, res, ended), true, ref, nil
}

// repairRefusal ends a never-valid run: the repair-exhausted refusal carrying
// the final round's problems, with the no-progress cause when the last round
// repeated the prior bytes instead of spending the cap.
func (st *state) repairRefusal(args *launchArgs, rec *launcher, rep *core.RunRepair,
	started time.Time, res *core.Result, art artifactState, noProgress bool,
) (*core.RunResponse, bool, *core.RefusalError, []byte) {
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
	return st.refused(ref, res, ended), true, ref, nil
}

// readRoundReview reads the artifact the verdict already accepted. A read
// failure after a passing stat keeps the artifact family: the contract path
// did not deliver bytes.
func readRoundReview(args *launchArgs, res *core.Result) ([]byte, *core.RefusalError) {
	review, err := os.ReadFile(args.abs)
	if err != nil {
		return nil, &core.RefusalError{
			Code:    codeNoArtifact,
			Message: fmt.Sprintf("%s is unreadable after exit %d: %v", args.artifact, res.ExitCode, err),
		}
	}
	return review, nil
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
