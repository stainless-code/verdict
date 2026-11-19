package journal

import (
	"context"
	"fmt"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/readcommon"
)

const (
	codeCanceled      = "canceled"
	codeMissingBundle = "missing-bundle"
	codeCorruptStore  = "corrupt-store"
)

// Request carries a resolved `verdict journal` invocation.
type Request struct {
	core.ReadRequest
	Warnings []string
}

// Response carries the folded journal summary and rendered human output.
type Response struct {
	Data     *Summary
	Repo     string
	Pr       string
	At       string
	Next     string
	Warnings []string
	Human    []string
}

// Run reads and folds a bundle's external journal without taking a lock or
// writing any file.
func Run(ctx context.Context, req *Request) (*Response, *core.RefusalError) {
	if err := ctx.Err(); err != nil {
		return nil, &core.RefusalError{Code: codeCanceled, Message: "canceled before journal completed"}
	}
	dir, ref := readcommon.ResolveBundleDir(req.RepoRootPath(), req.Pr)
	if ref != nil {
		return nil, ref
	}
	if !readcommon.IsDir(dir) {
		return nil, &core.RefusalError{
			Code:    codeMissingBundle,
			Message: "no bundle at " + readcommon.DisplayPath(req.RepoRootPath(), dir),
		}
	}
	bundleName := readcommon.BundleName(dir)
	var env map[string]string
	if req.Runtime != nil {
		env = req.Env
	}
	journalPath, _, _ := Path(req.RepoRootPath(), bundleName, env)
	sum, err := Read(journalPath, bundleName)
	if err != nil {
		return nil, &core.RefusalError{
			Code:    codeCorruptStore,
			Message: fmt.Sprintf("could not read %s: %v", journalPath, err),
		}
	}
	warnings := append([]string{}, req.Warnings...)
	return &Response{
		Data:     sum,
		Repo:     req.Repo,
		Pr:       req.Pr,
		At:       req.TimeUTC(),
		Next:     "",
		Warnings: warnings,
		Human:    FormatHuman(sum),
	}, nil
}
