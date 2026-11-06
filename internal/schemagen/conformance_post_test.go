package schemagen

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	postcore "github.com/stainless-code/verdict/internal/core/post"
)

// postCommander replays the post happy path: the live-SHA read and the
// reviews POST (plus the empty thread recheck when the APPROVE path needs
// it). Unscripted argv fails the test.
type postCommander struct {
	out map[string]string
	t   *testing.T
}

// Run replays the scripted stdout for argv.
func (c postCommander) Run(_ context.Context, cmd *core.Command) (core.Result, error) {
	c.t.Helper()
	key := strings.Join(cmd.Argv, "\x00")
	out, ok := c.out[key]
	if !ok {
		c.t.Errorf("unexpected argv: %q", cmd.Argv)
		return core.Result{}, errUnexpectedPostArgv
	}
	return core.Result{Stdout: out}, nil
}

// errUnexpectedPostArgv seals an unscripted post spawn in conformance tests.
var errUnexpectedPostArgv = postArgvError("unexpected argv")

// postArgvError is a plain error value for unscripted spawns.
type postArgvError string

// Error implements error.
func (e postArgvError) Error() string { return string(e) }

// postTestTime is the deterministic clock for post conformance.
var postTestTime = time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

// runPostDryRun builds a minimal GitHub bundle and runs the post dry-run.
func runPostDryRun(t *testing.T) *core.PostResponse {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	writeFixture(t, ".verdict/state/5101/PR.json",
		`{"state":"OPEN","headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",`+
			`"baseRefOid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","author":{"login":"someone-else"}}`)
	writeFixture(t, ".verdict/state/5101/full.diff", fixtureDiff())
	writeFixture(t, ".verdict/state/5101/review.json",
		`{"schema_version":"1","body":"> ✅ Looks good — minor note below.","approved":false,"comments":[]}`)
	seedControls(t, ".verdict/state/5101")
	threads := "gh\x00api\x00graphql\x00--paginate\x00-F\x00owner=acme\x00-F\x00name=app\x00-F\x00pr=5101\x00-f\x00query=" + postQueryLiveCount
	_ = threads
	cmdr := postCommander{t: t, out: map[string]string{
		"gh\x00api\x00repos/acme/app/pulls/5101\x00--jq\x00.head.sha": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n",
	}}
	root := ""
	req := &core.PostRequest{
		Runtime: &core.Runtime{
			Now:       func() time.Time { return postTestTime },
			Commander: cmdr,
		},
		Repo: "acme/app", Pr: "5101", Root: &root,
		Flags:     core.PostFlags{Approve: "false", ReviewFile: ".verdict/state/5101/review.json", DryRun: true},
		Effective: core.Defaults(),
	}
	resp, ref := postcore.Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("dry-run refusal: %v", ref)
	}
	return resp
}

// runPostSubmit builds the same bundle and runs the real post through the
// stubbed reviews POST.
func runPostSubmit(t *testing.T) *core.PostResponse {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	writeFixture(t, ".verdict/state/5101/PR.json",
		`{"state":"OPEN","headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",`+
			`"baseRefOid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","author":{"login":"someone-else"}}`)
	writeFixture(t, ".verdict/state/5101/full.diff", fixtureDiff())
	writeFixture(t, ".verdict/state/5101/review.json",
		`{"schema_version":"1","body":"> ✅ Looks good — minor note below.","approved":false,"comments":[]}`)
	seedControls(t, ".verdict/state/5101")
	cmdr := postCommander{t: t, out: map[string]string{
		"gh\x00api\x00repos/acme/app/pulls/5101\x00--jq\x00.head.sha":                                                                       "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n",
		"gh\x00api\x00repos/acme/app/pulls/5101/reviews\x00--method\x00POST\x00--input\x00-\x00--jq\x00{id, state, html_url, submitted_at}": `{"id":101,"state":"commented","html_url":"https://example.invalid/r/101","submitted_at":"2026-01-01T00:00:00Z"}`,
	}}
	root := ""
	req := &core.PostRequest{
		Runtime: &core.Runtime{
			Now:       func() time.Time { return postTestTime },
			Commander: cmdr,
		},
		Repo: "acme/app", Pr: "5101", Root: &root,
		Flags:     core.PostFlags{Approve: "false", ReviewFile: ".verdict/state/5101/review.json"},
		Effective: core.Defaults(),
		JSONMode:  true,
	}
	resp, ref := postcore.Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("submit refusal: %v", ref)
	}
	return resp
}

// postQueryLiveCount mirrors the post service's live-thread query for the
// unused-thread key (kept to document the seam; the dry-run path never
// fires it).
const postQueryLiveCount = `
      query($owner:String!, $name:String!, $pr:Int!, $endCursor:String) {
        repository(owner:$owner, name:$name) {
          pullRequest(number:$pr) {
            reviewThreads(first:100, after:$endCursor) {
              nodes { isResolved }
            }
          }
        }
      }`

// TestRuntimePostDryRunValidates proves the post dry-run response (a)
// validates against the generated postData definition and (b) round-trips
// through the modeled shape field for field.
func TestRuntimePostDryRunValidates(t *testing.T) {
	doc := loadSchemaDocument(t, "envelope.schema.json")
	resp := runPostDryRun(t)
	data := resp.Data.Wire()

	validateWire(t, doc, "$defs.postData", defOf(t, doc, "postData"), decodeWire(t, data))

	var modeled PostData
	unmarshalWire(t, data, &modeled)
	comparePostData(t, resp.Data, &modeled)

	env := core.RenderEnvelope("post", "acme/app", "5101", resp.At, resp.Next,
		resp.Warnings, data, nil)
	validateWire(t, doc, "$", doc, decodeWire(t, env))

	var envelope Envelope
	unmarshalWire(t, env, &envelope)
	if envelope.Verb != "post" || envelope.Repo != "acme/app" {
		t.Errorf("envelope identity = %+v", envelope)
	}
	if envelope.Data == nil || (*envelope.Data)["event"] != "COMMENT" {
		t.Errorf("envelope data = %v", envelope.Data)
	}
	if envelope.Error != nil {
		t.Errorf("success envelope error = %+v", envelope.Error)
	}
}

// TestRuntimePostSubmitValidates proves the real-post response validates
// too, carrying the accepted review before provenance in wire order.
func TestRuntimePostSubmitValidates(t *testing.T) {
	doc := loadSchemaDocument(t, "envelope.schema.json")
	resp := runPostSubmit(t)
	data := resp.Data.Wire()

	validateWire(t, doc, "$defs.postData", defOf(t, doc, "postData"), decodeWire(t, data))

	var modeled PostData
	unmarshalWire(t, data, &modeled)
	comparePostData(t, resp.Data, &modeled)

	keys := data.Keys
	reviewIdx, provIdx := -1, -1
	for i, k := range keys {
		if k == "review" {
			reviewIdx = i
		}
		if k == "provenance" {
			provIdx = i
		}
	}
	if reviewIdx < 0 || provIdx < 0 || reviewIdx > provIdx {
		t.Errorf("real-post wire order misses review-before-provenance: %v", keys)
	}

	env := core.RenderEnvelope("post", "acme/app", "5101", resp.At, resp.Next,
		resp.Warnings, data, nil)
	validateWire(t, doc, "$", doc, decodeWire(t, env))
}

// TestPostArtifactValidates proves the written post.json validates against
// the generated post schema and round-trips through the modeled shape.
func TestPostArtifactValidates(t *testing.T) {
	doc := loadSchemaDocument(t, "post.schema.json")
	runPostSubmit(t)
	raw, err := os.ReadFile(".verdict/state/5101/post.json")
	if err != nil {
		t.Fatalf("read post.json: %v", err)
	}
	var v any
	unmarshalJSON(t, raw, &v)
	validateWire(t, doc, "$", doc, v)

	var modeled Post
	unmarshalJSON(t, raw, &modeled)
	if modeled.SchemaVersion != "1" || modeled.Head != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Errorf("post identity = %+v", modeled)
	}
	if modeled.Review == nil {
		t.Errorf("post misses the accepted review: %+v", modeled)
	}
}

// comparePostData asserts every modeled post field round-trips.
//
//nolint:revive,cyclop // one assertion block per modeled field group
func comparePostData(t *testing.T, got *core.PostData, modeled *PostData) {
	t.Helper()
	if modeled.DryRun != got.DryRun || modeled.Event != got.Event || modeled.CommitID != got.CommitID {
		t.Errorf("identity round-trip = %+v", modeled)
	}
	if modeled.Body.Marker != got.Body.Marker {
		t.Errorf("body marker = %+v, want %+v", modeled.Body, got.Body)
	}
	if modeled.Anchors != got.Anchors || modeled.Suggestions != got.Suggestions || modeled.Findings.Open != got.Findings.Open {
		t.Errorf("tallies = %+v %+v %+v", modeled.Anchors, modeled.Suggestions, modeled.Findings)
	}
	if len(modeled.Items) != len(got.Items) {
		t.Fatalf("items = %d, want %d", len(modeled.Items), len(got.Items))
	}
	for i := range got.Items {
		want, have := got.Items[i], modeled.Items[i]
		if have.Sig != want.Sig || have.Status != want.Status || have.Locator != want.Locator {
			t.Errorf("item %d = %+v, want %+v", i, have, want)
		}
	}
	if !reflect.DeepEqual(modeled.Confidence, got.Confidence) {
		t.Errorf("confidence = %+v, want %+v", modeled.Confidence, got.Confidence)
	}
	if !reflect.DeepEqual(modeled.SecuritySummary, got.SecuritySummary) {
		t.Errorf("security = %+v, want %+v", modeled.SecuritySummary, got.SecuritySummary)
	}
	if modeled.PayloadHash != got.PayloadHash {
		t.Errorf("payload hash = %q, want %q", modeled.PayloadHash, got.PayloadHash)
	}
	if !reflect.DeepEqual(modeled.Provenance, got.Provenance) {
		t.Errorf("provenance = %+v, want %+v", modeled.Provenance, got.Provenance)
	}
}
