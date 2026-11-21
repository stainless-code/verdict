package run

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	panelcore "github.com/stainless-code/verdict/internal/core/panel"
	"github.com/stainless-code/verdict/internal/core/repair"
	"github.com/stainless-code/verdict/internal/jq"
)

func (st *state) launchSelected(ctx context.Context, args *launchArgs, rounds int) (*core.RunResponse, *core.RefusalError) {
	if st.twoFinders && resumeArguments(&args.def, st.req.PassThrough) {
		return nil, st.refuse(codeBadFlags, "two finders require fresh processes — remove session-resume options")
	}
	if !st.req.Print && st.drainDigest == "" {
		if ref := st.supersede(args.target.evidence); ref != nil {
			return nil, ref
		}
	}
	if st.twoFinders {
		return st.coordinate(ctx, args, rounds)
	}
	return st.invoke(ctx, args, rounds)
}

func (st *state) supersede(dir string) *core.RefusalError {
	if !panelcore.CheckExecution(dir, nil).Active {
		return nil
	}
	captured, ref := readFinderCapture(dir)
	if ref != nil {
		return ref
	}
	if captured == nil {
		return retirementRefusal(supersededDir, damagedCapture())
	}
	location, ref := retirementLocation(captured, dir)
	if ref != nil {
		return ref
	}
	count, source, ref := st.finderPolicy(captured)
	if ref != nil {
		return ref
	}
	captured.Execution = nil
	captured.Finders, captured.FinderSource, captured.Superseded = count, source, location
	if err := config.WriteRunState(dir, captured); err != nil {
		return retirementRefusal(location, err)
	}
	return nil
}

// executionArtifacts names the automatic artifacts a captured execution
// writes beside its record: retirement moves them, and a live one among them
// means the capture is damaged rather than retired.
var executionArtifacts = []string{
	"launcher.json", "review.json", "verification", "x-finder-1", "x-finder-2",
	"x-cross-1", "x-cross-2", "cost.json",
}

// retirementLocation retires the captured execution and names where it went. A
// capture that already published its retirement keeps that location, so a
// replacement that failed after the move starts over instead of stalling.
func retirementLocation(captured *config.RunState, dir string) (string, *core.RefusalError) {
	if captured.Execution != nil {
		location, err := retireArtifacts(dir)
		if err != nil {
			return location, retirementRefusal(location, err)
		}
		return location, nil
	}
	if !retiredCapture(captured, dir) {
		return "", retirementRefusal(supersededDir, damagedCapture())
	}
	return captured.Superseded, nil
}

func damagedCapture() error {
	return jq.NewErrf("automatic artifacts have no captured execution; restore run.json")
}

// supersededDir holds the executions earlier replacement runs retired.
const supersededDir = "x-superseded"

// retiredCapture reports whether a capture that lost its execution reference is
// an already-retired one. A replacement launch can abort after the retirement
// published, and re-running it then has to start over instead of asking for a
// hand repair; a capture with no retirement marker or a live artifact is a
// damaged execution and keeps its refusal.
func retiredCapture(captured *config.RunState, dir string) bool {
	if captured.Superseded == "" {
		return false
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return false
	}
	for _, name := range executionArtifacts {
		if _, err := os.Lstat(filepath.Join(dir, name)); !errors.Is(err, fs.ErrNotExist) {
			return false
		}
	}
	return true
}

func retirementRefusal(location string, err error) *core.RefusalError {
	return &core.RefusalError{Code: codeRecordFailed, Message: fmt.Sprintf(
		"previous execution could not be retired: %v; restore bundle paths from %s, then re-run; no agent launched", err, location)}
}

const retirementDirMode = 0o700

func retireArtifacts(dir string) (string, error) {
	location := filepath.Join(supersededDir, repair.Nonce())
	staging := location + ".retiring"
	root, err := os.OpenRoot(dir)
	if err != nil {
		return staging, jq.NewErrf("open retirement root: %v", err)
	}
	defer func() { _ = root.Close() }()
	if err := root.MkdirAll(staging, retirementDirMode); err != nil {
		return staging, jq.NewErrf("create retirement location: %v", err)
	}
	if err := moveExecutionArtifacts(root, staging); err != nil {
		return staging, err
	}
	if err := syncRetirement(root, staging); err != nil {
		return staging, err
	}
	if err := root.Rename(staging, location); err != nil {
		return staging, jq.NewErrf("publish retirement: %v", err)
	}
	return location, syncRetirement(root, supersededDir)
}

func moveExecutionArtifacts(root *os.Root, staging string) error {
	for _, name := range executionArtifacts {
		if err := root.Rename(name, filepath.Join(staging, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return jq.NewErrf("retire %s: %v", name, err)
		}
	}
	return nil
}

func syncRetirement(root *os.Root, path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	file, err := root.Open(path)
	if err != nil {
		return jq.NewErrf("open retirement directory: %v", err)
	}
	defer func() { _ = file.Close() }()
	if err := file.Sync(); err != nil {
		return jq.NewErrf("sync retirement directory: %v", err)
	}
	return nil
}
