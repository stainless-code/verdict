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

// tool is one MCP tool: the core request type it decodes into, the field
// policy that hides adapter-owned members, and the service call it maps to.
type tool struct { //nolint:govet // the grouping documents the descriptor; alignment is irrelevant
	name        string
	description string
	request     reflect.Type
	omit        map[string]bool
	required    []string
	readOnly    bool
	invoke      func(context.Context, any) (*jq.Obj, *core.RefusalError)
}

// allTools returns the registered tool set. upgrade/skill are distribution
// surfaces, never review tools.
func allTools(readOnly bool) []*tool {
	tools := []*tool{
		dataTool("adopt", "Copy findings from a local bundle into the target PR bundle and re-anchor them.",
			reflect.TypeFor[adoptcore.Request](), nil, []string{argRepo, argPr, "from"}, false, adoptcore.Run),
		dataTool("calibrate", "Compare stored findings against live ground truth, or grade the offline probe.",
			reflect.TypeFor[calibratecore.Request](), nil, requiredRepoPr, false, calibratecore.Run),
		{
			name: "check-tree", request: reflect.TypeFor[checktreecore.Request](),
			description: "Gate on a clean git worktree, allowing the paths in expect.",
			readOnly:    true, omit: omitPositionals, invoke: invokeCheckTree,
		},
		dataTool("doctor", "Preflight the harness (gh auth, git, storage) and report unhealthy checks.",
			reflect.TypeFor[doctorcore.Request](), omitPositionals, nil, true, doctorcore.Run),
		{
			name: "fetch", request: reflect.TypeFor[core.FetchRequest](),
			description: "Fetch a pull request into .review/<pr> (diff, metadata, threads, prompt input).",
			required:    requiredRepoPr, invoke: invokeFetch,
		},
		{
			name: "fetch-local", request: reflect.TypeFor[core.FetchLocalRequest](),
			description: "Fetch a local git diff into .review/local-<name>.",
			required:    requiredRepoPr, invoke: invokeFetchLocal,
		},
		dataTool("fix", "Run one bounded fix pass over an inline finding (or report completion).",
			reflect.TypeFor[fixcore.Request](), nil, requiredRepoPr, false, fixcore.Run),
		dataTool("init", "Scaffold .review control files from a style preset; never prompts in MCP mode.",
			reflect.TypeFor[initcore.Request](), omitPositionals, nil, false, initcore.Run),
		dataTool("learn", "Read or update the local structured knowledge base (.review/kb.jsonl).",
			reflect.TypeFor[learncore.Request](), omitPositionals, nil, false, learncore.Run),
		{
			name: "next", request: reflect.TypeFor[core.NextRequest](),
			description: "Return the top actionable finding for the bundle.",
			required:    requiredRepoPr, readOnly: true, invoke: invokeNext,
		},
		dataTool("post", "Post exactly one review to GitHub (or preview with dryRun); writes and calls GitHub.",
			reflect.TypeFor[core.PostRequest](), nil, requiredRepoPr, false, postcore.Run),
		dataTool("ready", "Report merge readiness (checks, threads, draft/conflict) or gate on it.",
			reflect.TypeFor[readycore.Request](), nil, requiredRepoPr, true, readycore.Run),
		dataTool("record", "Validate and merge a local review.json into the findings and record stores.",
			reflect.TypeFor[core.RecordRequest](), nil, requiredRepoPr, false, recordcore.Run),
		{
			name: "report", request: reflect.TypeFor[core.ReportRequest](),
			description: "Render the offline review report (ranked findings, counts, optional markdown/SARIF).",
			required:    requiredRepoPr, readOnly: true, invoke: invokeReport,
		},
		{
			name: "show", request: reflect.TypeFor[core.ShowRequest](),
			description: "Return one full finding record by signature or unique prefix.",
			required:    []string{argRepo, argPr, "sig"}, readOnly: true, invoke: invokeShow,
		},
		dataTool("trend", "Roll up review outcomes across fetched bundles.",
			reflect.TypeFor[trendcore.Request](), map[string]bool{fieldPr: true}, []string{argRepo}, true, trendcore.Run),
		dataTool("triage", "Flip one finding's status (or preview the flip with dryRun).",
			reflect.TypeFor[triagecore.Request](), nil, []string{argRepo, argPr, "sig", "status"}, false, triagecore.Run),
		dataTool("verify", "Run the recorded verifyCmd with the watchdog and record the proof.",
			reflect.TypeFor[core.VerifyRequest](), nil, requiredRepoPr, false, verifycore.Run),
	}
	if !readOnly {
		return tools
	}
	kept := make([]*tool, 0, len(tools))
	for _, t := range tools {
		if t.readOnly {
			kept = append(kept, t)
		}
	}
	return kept
}

// dataTool builds a tool whose service returns a response carrying a Data
// field, the shape every verb except next/show/check-tree/fetch shares.
func dataTool[R, S any](
	name, description string, request reflect.Type, omit map[string]bool,
	required []string, readOnly bool, svc func(context.Context, *R) (*S, *core.RefusalError),
) *tool {
	return &tool{
		name: name, description: description, request: request, omit: omit,
		required: required, readOnly: readOnly,
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
