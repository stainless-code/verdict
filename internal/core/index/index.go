// Package indexcore is the index verb's domain service: it builds a Go index
// from a git tree with the standard library parser, stores it under
// .verdict/index/ keyed by tree SHA and content-addressed shards, and answers
// caller queries. Parsing is go/parser and go/ast only. internal/index owns
// argv parsing, envelope rendering, and exit mapping.
package indexcore

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
)

// Index subcommands.
const (
	QueryBuild       = "build"
	QueryStatus      = "status"
	QueryPrune       = "prune"
	QueryCallers     = "callers"
	QueryTestsFor    = "tests-for"
	QueryCoChanged   = "co-changed"
	QueryOwners      = "owners"
	QueryBlastRadius = "blast-radius"
)

// MCPQueries are the index subcommands the read-only MCP tool serves. Build
// and prune stay on the CLI because they write the store.
var MCPQueries = []string{QueryCallers, QueryStatus, QueryTestsFor, QueryCoChanged, QueryOwners, QueryBlastRadius}

// DefaultLimit is the row limit when the request names none.
const DefaultLimit = 50

// MaxLimit is the largest row limit a request may name.
const MaxLimit = 200

// Co-change query defaults and bounds.
const (
	DefaultCoChangeLimit   = 20
	DefaultCoChangeCommits = 2000
	DefaultMinCount        = 2
	MaxCommitWindow        = 100000
)

// Shipped index caps.
const (
	defaultFiles       = 20000
	defaultFileBytes   = 1 << 20
	defaultTreeBytes   = 96 << 20
	defaultShardsBytes = 256 << 20
	defaultKeepTrees   = 4
	defaultRefsPerFile = 5000
)

// Refusal codes and resolutions this package emits.
const (
	codeBadFlags         = "bad-flags"
	codeIndexCapped      = "index-capped"
	codeIndexCorrupt     = "index-corrupt"
	codeIndexFailed      = "index-failed"
	codeMissingIndex     = "missing-index"
	gitBin               = "git"
	resolutionExact      = "exact"
	resolutionLexical    = "lexical"
	resolutionUnresolved = "unresolved"
	goSuffix             = ".go"
	testSuffix           = "_test.go"
)

// Limits is the index's byte and count budget. The zero value reads as
// [DefaultLimits]; tests set smaller caps to exercise the refusals.
type Limits struct {
	Files       int
	FileBytes   int64
	TreeBytes   int64
	ShardsBytes int64
	KeepTrees   int
	RefsPerFile int
}

// DefaultLimits returns the shipped caps.
func DefaultLimits() Limits {
	return Limits{
		Files:       defaultFiles,
		FileBytes:   defaultFileBytes,
		TreeBytes:   defaultTreeBytes,
		ShardsBytes: defaultShardsBytes,
		KeepTrees:   defaultKeepTrees,
		RefsPerFile: defaultRefsPerFile,
	}
}

// Request carries one index invocation: the subcommand, the source directory
// and revision, and the caller-query inputs.
type Request struct { //nolint:govet // field grouping documents the request surface
	core.ReadRequest
	Query    string
	From     string
	At       string
	Symbol   string
	Path     string
	Since    string
	Paths    []string
	Commits  int `max:"100000" min:"1"`
	MinCount int `max:"100000" min:"1"`
	Limit    int `max:"200"    min:"1"`
	Warnings []string
	Limits   *Limits `json:"-"`
	// NoCache reads a valid cached co-change window but never writes or
	// prunes the cache.
	NoCache bool `json:"-"`
}

// Response carries the completed invocation: the envelope fields, the human
// lines, and the typed data profile.
type Response struct { //nolint:govet // field grouping documents the response surface
	At       string
	Next     string
	Warnings []string
	Human    []string
	Data     *Data
}

// Data is the index verb's --json data profile. Wire branches on Kind, so
// each subcommand publishes only the keys it measured.
type Data struct { //nolint:govet // field order is the published property order
	Kind          string
	Tree          string
	Commit        string
	Stale         bool
	Built         bool
	Files         int
	Skipped       int
	Parsed        int
	Reused        int
	TreeBytes     int64
	ShardCount    int
	ShardBytes    int64
	Trees         []TreeRow
	Symbol        string
	Path          string
	Limit         int
	Callers       int
	Unresolved    int
	Rows          []Row
	Truncated     int
	Kept          []string
	RemovedTree   int
	GoneShards    int
	Tests         int
	TestRows      []TestRow
	Window        Window
	Partners      int
	TargetCommits int
	CoChangeRows  []CoChangeRow
	Target        *Target
	Blast         *Blast
}

// TreeRow is one stored tree file.
type TreeRow struct {
	Tree  string
	Files int
	Bytes int64
}

// Row is one caller row.
type Row struct {
	Path       string
	Kind       string
	Resolution string
	Line       int
}

// TestRow is one candidate test for a changed file or symbol.
type TestRow struct {
	Path string
	Via  string
}

// CoChangeRow is one partner path and how often it moved with the target.
type CoChangeRow struct {
	Path    string
	Commits int
	Jaccard float64
}

// Window is the bounded history window a co-change query read: the oldest and
// newest commits it saw, and how many it counted.
type Window struct {
	First   string
	Last    string
	Commits int
}

// Target is one queried path and the CODEOWNERS rule that owns it. A nil
// Owners slice is "no rule matched" (or no CODEOWNERS file at the tree); an
// empty non-nil slice is GitHub's ownerless rule, which clears an earlier
// match. It is never rendered as an owner named nobody.
type Target struct {
	Path         string
	OwnersSource string
	Owners       []string
}

type state struct {
	req      *Request
	warnings []string
	human    []string
	trust    []string
}

// limits resolves the caps: the repository's typed index section first, then
// any injected override.
//
//nolint:revive // the cap override matrix reads as one block
func (st *state) limits() (Limits, *core.RefusalError) {
	l := DefaultLimits()
	if ref := st.loadConfigLimits(&l); ref != nil {
		return Limits{}, ref
	}
	if st.req.Limits == nil {
		return l, nil
	}
	override := *st.req.Limits
	if override.Files > 0 {
		l.Files = override.Files
	}
	if override.FileBytes > 0 {
		l.FileBytes = override.FileBytes
	}
	if override.TreeBytes > 0 {
		l.TreeBytes = override.TreeBytes
	}
	if override.ShardsBytes > 0 {
		l.ShardsBytes = override.ShardsBytes
	}
	if override.KeepTrees > 0 {
		l.KeepTrees = override.KeepTrees
	}
	if override.RefsPerFile > 0 {
		l.RefsPerFile = override.RefsPerFile
	}
	return l, nil
}

// loadConfigLimits overlays the repository's typed index caps when present.
func (st *state) loadConfigLimits(l *Limits) *core.RefusalError {
	raw, err := os.ReadFile(filepath.Join(st.sourceRoot(), config.VerdictDir, "config.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return &core.RefusalError{Code: codeIndexFailed, Message: "could not read the index caps: " + err.Error()}
	}
	cfg, ref := config.Parse(raw)
	if ref != nil {
		return ref
	}
	if cfg.Index == nil {
		return nil
	}
	applyIndexCaps(l, cfg.Index)
	return nil
}

// applyIndexCaps copies the non-nil typed caps onto l.
func applyIndexCaps(l *Limits, caps *core.ConfigIndex) {
	if caps.Files != nil {
		l.Files = *caps.Files
	}
	if caps.FileBytes != nil {
		l.FileBytes = *caps.FileBytes
	}
	if caps.TreeBytes != nil {
		l.TreeBytes = *caps.TreeBytes
	}
	if caps.ShardsBytes != nil {
		l.ShardsBytes = *caps.ShardsBytes
	}
	if caps.KeepTrees != nil {
		l.KeepTrees = *caps.KeepTrees
	}
	if caps.RefsPerFile != nil {
		l.RefsPerFile = *caps.RefsPerFile
	}
}

func (st *state) sourceRoot() string {
	if st.req.From != "" {
		return st.req.From
	}
	return st.req.RepoRootPath()
}

func (st *state) warn(msg string) {
	st.warnings = append(st.warnings, msg)
	st.req.Warn(msg)
}

func (st *state) line(line string) {
	st.human = append(st.human, line)
}

// Run dispatches one index subcommand.
func Run(ctx context.Context, req *Request) (*Response, *core.RefusalError) {
	st := &state{req: req, warnings: append([]string{}, req.Warnings...)}
	if err := ctx.Err(); err != nil {
		return nil, &core.RefusalError{Code: "canceled", Message: "canceled before index completed"}
	}
	var resp *Response
	var ref *core.RefusalError
	switch req.Query {
	case QueryBuild:
		resp, ref = st.build(ctx)
	case QueryStatus:
		resp, ref = st.status(ctx)
	case QueryPrune:
		resp, ref = st.prune(ctx)
	case QueryCallers:
		resp, ref = st.callers(ctx)
	case QueryTestsFor:
		resp, ref = st.testsFor(ctx)
	case QueryCoChanged:
		resp, ref = st.coChanged(ctx)
	case QueryOwners:
		resp, ref = st.owners(ctx)
	case QueryBlastRadius:
		resp, ref = st.blastRadius(ctx)
	default:
		return nil, &core.RefusalError{
			Code:    "bad-flags",
			Message: "index wants build, status, prune, callers, tests-for, co-changed, owners, or blast-radius",
		}
	}
	if ref != nil {
		ref.Warnings = st.warnings
		return nil, ref
	}
	resp.Warnings = st.warnings
	resp.Human = st.human
	return resp, nil
}
