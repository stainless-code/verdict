package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"

	"github.com/stainless-code/verdict/internal/core"
	adoptcore "github.com/stainless-code/verdict/internal/core/adopt"
	calibratecore "github.com/stainless-code/verdict/internal/core/calibrate"
	checktreecore "github.com/stainless-code/verdict/internal/core/checktree"
	doctorcore "github.com/stainless-code/verdict/internal/core/doctor"
	fetchcore "github.com/stainless-code/verdict/internal/core/fetch"
	fixcore "github.com/stainless-code/verdict/internal/core/fix"
	initcore "github.com/stainless-code/verdict/internal/core/init"
	learncore "github.com/stainless-code/verdict/internal/core/learn"
	postcore "github.com/stainless-code/verdict/internal/core/post"
	readycore "github.com/stainless-code/verdict/internal/core/ready"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
	reportcore "github.com/stainless-code/verdict/internal/core/report"
	trendcore "github.com/stainless-code/verdict/internal/core/trend"
	triagecore "github.com/stainless-code/verdict/internal/core/triage"
	verifycore "github.com/stainless-code/verdict/internal/core/verify"
	"github.com/stainless-code/verdict/internal/jq"
)

// Argument keys and adapter constants shared by the registry.
const (
	argRepo            = "repo"
	argPr              = "pr"
	fieldRepo          = "Repo"
	fieldPr            = "Pr"
	msgRequestMismatch = "tool request type mismatch"
	artifactPerm       = 0o600
)

// requiredRepoPr is the common required-argument pair, and omitPositionals
// hides repo/pr from verbs that take no positionals.
var (
	requiredRepoPr  = []string{argRepo, argPr}
	omitPositionals = map[string]bool{fieldRepo: true, fieldPr: true}
)

// toolMeta is the descriptor half of a tool: the human title and the four
// MCP behavior hints. readOnly is also the access policy the CLI filters on.
type toolMeta struct {
	title       string
	readOnly    bool
	destructive bool
	idempotent  bool
	openWorld   bool
}

// tool is one MCP tool: the core request type it decodes into, the field
// policy that hides adapter-owned members, and the service call it maps to.
type tool struct { //nolint:govet // the grouping documents the descriptor; alignment is irrelevant
	name         string
	title        string
	description  string
	request      reflect.Type
	omit         map[string]bool
	required     []string
	readOnly     bool
	destructive  bool
	idempotent   bool
	openWorld    bool
	operatorOnly bool
	invoke       func(context.Context, any) (*jq.Obj, *core.RefusalError)
}

// allTools returns the registered tool set for one surface. readOnly keeps
// only non-writing tools; operator adds the human-owned verbs. The CLI
// refuses the two together, and the filter order here makes that impossible
// to violate from the library either. upgrade/skill are distribution
// surfaces, never review tools.
//
//nolint:revive // the registry literal and surface filter read as one table
func allTools(readOnly, operator bool) []*tool {
	tools := []*tool{
		dataTool("adopt", "Copy findings from a local bundle into the target PR bundle "+
			"and re-anchor them. Call after a local review to post it on a PR. "+
			"Writes the target bundle's findings store.",
			reflect.TypeFor[adoptcore.Request](), nil, []string{argRepo, argPr, "from"},
			toolMeta{title: "Adopt findings", idempotent: true}, adoptcore.Run),
		dataTool("calibrate", "Join stored findings against live thread and file state, "+
			"or grade the offline probe. Call after posting to measure precision. "+
			"Reads GitHub live; the probe rewrites kb-grades.jsonl.",
			reflect.TypeFor[calibratecore.Request](), nil, requiredRepoPr,
			toolMeta{title: "Calibrate findings", idempotent: true, openWorld: true}, calibratecore.Run),
		{
			name: "check-tree", title: "Check worktree",
			request: reflect.TypeFor[checktreecore.Request](),
			description: "Gate on a clean git worktree, allowing the paths in expect. " +
				"Call before finalizing when stray edits must fail the review. " +
				"Reads git only; writes nothing.",
			readOnly: true, idempotent: true, omit: omitPositionals, invoke: invokeCheckTree,
		},
		dataTool("doctor", "Preflight gh auth, git, and storage. Call first when a verb "+
			"fails for environmental reasons. Reads local tools and config; writes nothing.",
			reflect.TypeFor[doctorcore.Request](), omitPositionals, nil,
			toolMeta{title: "Preflight environment", readOnly: true, idempotent: true}, doctorcore.Run),
		{
			name: "fetch", title: "Fetch pull request",
			request: reflect.TypeFor[core.FetchRequest](),
			description: "Fetch a PR into .review/<pr> (diff, metadata, threads, prompt input). " +
				"Call first for any PR review; an over-cap diff refuses, so re-run with chunk=true. " +
				"Writes the bundle and calls GitHub.",
			required: requiredRepoPr, idempotent: true, openWorld: true, invoke: invokeFetch,
		},
		{
			name: "fetch-local", title: "Fetch local diff",
			request: reflect.TypeFor[core.FetchLocalRequest](),
			description: "Fetch a local git diff into .review/local-<name>. Call to review " +
				"uncommitted or branch changes without a PR. Writes the bundle; no network.",
			required: requiredRepoPr, idempotent: true, invoke: invokeFetchLocal,
		},
		dataTool("fix", "Run one bounded, verify-gated fix pass over an inline finding. "+
			"Call after review to prepare one finding's patch; the ledger caps passes. "+
			"Writes the fix ledger and runs the recorded verify command.",
			reflect.TypeFor[fixcore.Request](), nil, requiredRepoPr,
			toolMeta{title: "Prepare a fix"}, fixcore.Run),
		dataTool("init", "Scaffold .review control files from a style preset. Call once in a "+
			"new repository; yes=true overwrites existing controls. Writes controls and "+
			"runs a tree check.",
			reflect.TypeFor[initcore.Request](), omitPositionals, nil,
			toolMeta{title: "Scaffold controls", destructive: true, idempotent: true}, initcore.Run),
		dataTool("learn", "Read or update the local knowledge base. Call to list rules or "+
			"add, retire, or restore one. Writes .review/kb.jsonl.",
			reflect.TypeFor[learncore.Request](), omitPositionals, nil,
			toolMeta{title: "Manage knowledge base", idempotent: true}, learncore.Run),
		{
			name: "next", title: "Next finding",
			request: reflect.TypeFor[core.NextRequest](),
			description: "Return the top actionable finding. Call after fetch to pick the next " +
				"review or fix target. Reads the bundle; writes nothing.",
			required: requiredRepoPr, readOnly: true, idempotent: true, invoke: invokeNext,
		},
		dataTool("post", "Post exactly one review to GitHub, or preview with dryRun. Call once "+
			"after review.json is final; never write a review note. Writes post.json and "+
			"sends to GitHub.",
			reflect.TypeFor[core.PostRequest](), nil, requiredRepoPr,
			toolMeta{title: "Post review", destructive: true, idempotent: true, openWorld: true},
			postcore.Run),
		dataTool("ready", "Report merge readiness (checks, threads, draft, conflict) or gate on "+
			"it. Call before merging. Reads GitHub; writes nothing.",
			reflect.TypeFor[readycore.Request](), nil, requiredRepoPr,
			toolMeta{title: "Merge readiness", readOnly: true, idempotent: true, openWorld: true},
			readycore.Run),
		dataTool("record", "Validate and merge review.json into the findings and record stores. "+
			"Call for local bundles that cannot post. Writes findings.json and record.json.",
			reflect.TypeFor[core.RecordRequest](), nil, requiredRepoPr,
			toolMeta{title: "Record review", idempotent: true}, recordcore.Run),
		{
			name: "report", title: "Render report",
			request: reflect.TypeFor[core.ReportRequest](),
			description: "Render the offline review report (ranked findings, counts, markdown/SARIF). " +
				"Call after fetch or record; the follow-up is next. Reads the bundle and writes " +
				"only artifacts you request.",
			required: requiredRepoPr, readOnly: true, idempotent: true, invoke: invokeReport,
		},
		{
			name: "show", title: "Show finding",
			request: reflect.TypeFor[core.ShowRequest](),
			description: "Return one full finding by signature or unique prefix. Call to inspect " +
				"a finding named by report or next. Reads the bundle; writes nothing.",
			required: []string{argRepo, argPr, "sig"}, readOnly: true, idempotent: true,
			invoke: invokeShow,
		},
		dataTool("threads", "Refresh the normalized review-thread feed "+
			"(.review/<pr>/threads.json) from the host. Call after pushing fixes to re-read "+
			"resolution state before a re-review or merge readiness. Reads the host; writes "+
			"threads.json only.",
			reflect.TypeFor[fetchcore.ThreadsRequest](), nil, requiredRepoPr,
			toolMeta{title: "Refresh review threads", readOnly: true, idempotent: true, openWorld: true},
			fetchcore.RunThreads),
		dataTool("trend", "Roll up review outcomes across fetched bundles. Call for a cross-PR "+
			"summary. Reads local bundles; writes nothing.",
			reflect.TypeFor[trendcore.Request](), map[string]bool{fieldPr: true}, []string{argRepo},
			toolMeta{title: "Review trend", readOnly: true, idempotent: true}, trendcore.Run),
		operatorTool(dataTool("triage", "Operator-only: flip one finding's status with a "+
			"rationale. Never call while reviewing a PR; propose triage[] in review.json "+
			"instead. Writes findings.json and stages KB credit.",
			reflect.TypeFor[triagecore.Request](), nil, []string{argRepo, argPr, "sig", "status"},
			toolMeta{title: "Triage finding", destructive: true}, triagecore.Run)),
		dataTool("verify", "Run the recorded verifyCmd under the watchdog and record the proof. "+
			"Call to prove a fix before post. Writes verification.json and runs the recorded command.",
			reflect.TypeFor[core.VerifyRequest](), nil, requiredRepoPr,
			toolMeta{title: "Run verification", destructive: true, openWorld: true}, verifycore.Run),
	}
	kept := make([]*tool, 0, len(tools))
	for _, t := range tools {
		if readOnly && !t.readOnly {
			continue
		}
		if t.operatorOnly && (readOnly || !operator) {
			continue
		}
		kept = append(kept, t)
	}
	return kept
}

// operatorTool marks one tool as part of the opt-in operator surface.
func operatorTool(t *tool) *tool {
	t.operatorOnly = true
	return t
}

// dataTool builds a tool whose service returns a response carrying a Data
// field, the shape every verb except next/show/check-tree/fetch shares.
func dataTool[R, S any](
	name, description string, request reflect.Type, omit map[string]bool,
	required []string, meta toolMeta, svc func(context.Context, *R) (*S, *core.RefusalError),
) *tool {
	return &tool{
		name: name, title: meta.title, description: description, request: request, omit: omit,
		required: required, readOnly: meta.readOnly, destructive: meta.destructive,
		idempotent: meta.idempotent, openWorld: meta.openWorld,
		invoke: func(ctx context.Context, raw any) (*jq.Obj, *core.RefusalError) {
			return invokeService(ctx, raw, svc, wireResponseData[S])
		},
	}
}

// invokeService adapts one typed core service call. The request pointer is
// built by the reflector from the same type the schema came from, so the
// assertion is exact; a mismatch is an internal bug, never user input.
func invokeService[R, S any](
	ctx context.Context, raw any,
	svc func(context.Context, *R) (*S, *core.RefusalError),
	data func(*S) *jq.Obj,
) (*jq.Obj, *core.RefusalError) {
	req, ok := raw.(*R)
	if !ok {
		return nil, internalRefusal()
	}
	resp, ref := svc(ctx, req)
	if ref != nil {
		return nil, ref
	}
	return data(resp), nil
}

// wireResponseData reflects the response's Data field (the shared profile
// shape) through core's ordered wire mapping.
func wireResponseData[S any](resp *S) *jq.Obj {
	value := reflect.ValueOf(resp)
	if value.Kind() != reflect.Pointer || value.IsNil() {
		return jq.NewObj()
	}
	data := value.Elem().FieldByName("Data")
	if !data.IsValid() {
		return jq.NewObj()
	}
	return wireData(data.Interface())
}

// invokeReport runs report and writes its requested artifacts (the CLI
// adapter owns the same writes; the transport returns data, not files).
func invokeReport(ctx context.Context, raw any) (*jq.Obj, *core.RefusalError) {
	req, ok := raw.(*core.ReportRequest)
	if !ok {
		return nil, internalRefusal()
	}
	resp, ref := reportcore.Run(ctx, req)
	if ref != nil {
		return nil, ref
	}
	if ref := writeArtifact(resp.Output); ref != nil {
		return nil, ref
	}
	if ref := writeArtifact(resp.Sarif); ref != nil {
		return nil, ref
	}
	return wireData(resp.Data), nil
}

// invokeNext maps the next response to the CLI envelope's data shape.
func invokeNext(ctx context.Context, raw any) (*jq.Obj, *core.RefusalError) {
	req, ok := raw.(*core.NextRequest)
	if !ok {
		return nil, internalRefusal()
	}
	resp, ref := reportcore.RunNext(ctx, req)
	if ref != nil {
		return nil, ref
	}
	var item any
	if resp.Item != nil {
		item = resp.Item.Wire()
	}
	return jq.NewObj("item", item), nil
}

// invokeShow maps the show response to the CLI envelope's data shape.
func invokeShow(ctx context.Context, raw any) (*jq.Obj, *core.RefusalError) {
	req, ok := raw.(*core.ShowRequest)
	if !ok {
		return nil, internalRefusal()
	}
	resp, ref := reportcore.RunShow(ctx, req)
	if ref != nil {
		return nil, ref
	}
	var item any
	if resp.Item != nil {
		item = resp.Item.Raw
	}
	data := jq.NewObj("item", item)
	if resp.Fix != nil {
		data.Set("fix", resp.Fix.Wire())
	}
	return data, nil
}

// invokeCheckTree maps the not-a-repo machine refusal to a tool refusal.
func invokeCheckTree(ctx context.Context, raw any) (*jq.Obj, *core.RefusalError) {
	req, ok := raw.(*checktreecore.Request)
	if !ok {
		return nil, internalRefusal()
	}
	resp, ref := checktreecore.Run(ctx, req)
	if ref != nil {
		return nil, ref
	}
	if resp.Error != nil {
		return nil, &core.RefusalError{Code: resp.Error.Code, Message: resp.Error.Message}
	}
	return wireData(resp.Data), nil
}

// invokeFetch calls the network-backed fetch service; its error is the
// adapter's missing-tool/refusal seam.
func invokeFetch(ctx context.Context, raw any) (*jq.Obj, *core.RefusalError) {
	req, ok := raw.(*core.FetchRequest)
	if !ok {
		return nil, internalRefusal()
	}
	resp, err := fetchcore.Run(ctx, req)
	if err != nil {
		return nil, commandRefusal(err)
	}
	return wireData(resp.Data), nil
}

// invokeFetchLocal calls the local fetch service.
func invokeFetchLocal(ctx context.Context, raw any) (*jq.Obj, *core.RefusalError) {
	req, ok := raw.(*core.FetchLocalRequest)
	if !ok {
		return nil, internalRefusal()
	}
	resp, err := fetchcore.RunLocal(ctx, req)
	if err != nil {
		return nil, commandRefusal(err)
	}
	return wireData(resp.Data), nil
}

// commandRefusal maps the command runner's error forms to a tool refusal.
func commandRefusal(err error) *core.RefusalError {
	if ref, ok := errors.AsType[*core.RefusalError](err); ok {
		return ref
	}
	if missing, ok := errors.AsType[*core.MissingToolError](err); ok {
		return &core.RefusalError{Code: "missing-tool", Message: "missing: " + missing.Tool}
	}
	return &core.RefusalError{Code: "fetch-failed", Message: err.Error()}
}

// writeArtifact writes one requested report artifact (0600: reports can
// carry reviewed-diff excerpts).
func writeArtifact(a *core.FileArtifact) *core.RefusalError {
	if a == nil {
		return nil
	}
	if err := os.WriteFile(a.Path, a.Content, artifactPerm); err != nil {
		return &core.RefusalError{
			Code:    "report-failed",
			Message: fmt.Sprintf("could not write %s: %v", a.Path, err),
		}
	}
	return nil
}

// wireData renders a typed data profile through core's ordered wire
// mapping, so the MCP result equals the CLI --json envelope's data.
func wireData(v any) *jq.Obj {
	if v == nil {
		return jq.NewObj()
	}
	if o, ok := core.Wire(v).(*jq.Obj); ok {
		return o
	}
	return jq.NewObj()
}

// internalRefusal builds a sealed internal refusal for impossible request
// mismatches.
func internalRefusal() *core.RefusalError {
	return &core.RefusalError{Code: "internal", Message: msgRequestMismatch}
}

// rawMarshal is kept next to wireData so result construction uses the same
// compact encoder as the CLI envelope.
func rawMarshal(o *jq.Obj) json.RawMessage { return json.RawMessage(o.MarshalCompact()) }
