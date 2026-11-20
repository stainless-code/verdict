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
	"github.com/stainless-code/verdict/internal/core/repair"
	"github.com/stainless-code/verdict/internal/jq"
)

func (st *state) launchSelected(ctx context.Context, args *launchArgs, rounds int) (*core.RunResponse, *core.RefusalError) {
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
	captured, ref := readFinderCapture(dir)
	if ref != nil {
		return ref
	}
	if captured == nil || captured.Execution == nil {
		return nil
	}
	location, err := retireArtifacts(dir)
	if err != nil {
		return retirementRefusal(location, err)
	}
	count, source, ref := st.finderPolicy(dir)
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

func retirementRefusal(location string, err error) *core.RefusalError {
	return &core.RefusalError{Code: codeRecordFailed, Message: fmt.Sprintf(
		"previous execution could not be retired: %v; restore bundle paths from %s, then re-run; no agent launched", err, location)}
}

const retirementDirMode = 0o700

func retireArtifacts(dir string) (string, error) {
	location := filepath.Join("x-superseded", repair.Nonce())
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
	return location, syncRetirement(root, "x-superseded")
}

func moveExecutionArtifacts(root *os.Root, staging string) error {
	for _, name := range []string{
		"launcher.json", "review.json", "verification", "x-finder-1", "x-finder-2",
		"x-cross-1", "x-cross-2", "cost.json",
	} {
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
