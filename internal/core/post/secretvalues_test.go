package postcore

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
)

// secretFindingsReview carries an AWS key id in one finding body, so the
// pre-submit scan has one located site to name.
const secretFindingsReview = `{"schema_version":"1","body":"> ✅ Looks good — minor note below.","approved":false,` +
	`"comments":[{"path":"notes.txt","line":2,"side":"RIGHT",` +
	`"body":"Consider clarifying this line. AKIAIOSFODNN7EXAMPLE",` +
	`"rigor":"pointed","severity":"informational"}]}`

// secretSummaryReview carries the credential in the summary body instead, so
// the scan's two locations are graded apart.
const secretSummaryReview = `{"schema_version":"1","body":"> ⚠️ Rotate the leaked ghp_0123456789abcdefghijABCDEFGHIJ012345.","approved":false,"comments":[]}`

// TestRunRefusesSecretShapedBeforeHostCalls proves a real post refuses the
// published value with the typed code, names the located site and the override,
// and makes zero host calls: the scan runs before the live-head read, so a
// refused review never asks the host anything.
//
//nolint:revive // one leg, asserting code, wording, calls, and files inline
func TestRunRefusesSecretShapedBeforeHostCalls(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	postBundle(t, secretFindingsReview)
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{}}
	var warnings, human []string
	req := postTestReq(cmdr, Flags{Approve: "false", ReviewFile: ".verdict/state/7/review.json"}, &warnings, &human)
	_, ref := Run(context.Background(), req)
	if ref == nil {
		t.Fatal("secret-shaped review posted; want a refusal")
	}
	if ref.Code != "post-secret-shaped" {
		t.Fatalf("code = %q, want post-secret-shaped (message %q)", ref.Code, ref.Message)
	}
	for _, want := range []string{"finding 1 (aws-access-key-id)", "--allow-secret-values"} {
		if !strings.Contains(ref.Message, want) {
			t.Errorf("refusal %q misses %q", ref.Message, want)
		}
	}
	if strings.Contains(ref.Message, "AKIAIOSFODNN7EXAMPLE") {
		t.Errorf("refusal republished the value: %q", ref.Message)
	}
	if len(cmdr.calls) != 0 {
		t.Errorf("refused post called the host: %q", cmdr.calls)
	}
	if _, err := os.Stat(".verdict/state/7/post.json"); !os.IsNotExist(err) {
		t.Error("refused post wrote post.json")
	}
}

// TestRunRefusesSecretShapedSpecNote proves the spec row's note is scanned like
// every other free-text field on the row: a credential pasted into an `unmet`
// justification refuses the post by location, and the refusal never republishes
// the value it found.
func TestRunRefusesSecretShapedSpecNote(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	const leaked = "ghp_0123456789abcdefghijABCDEFGHIJ012345"
	review := `{"schema_version":"1","body":"> ⛔ One requirement is unmet.","approved":false,"comments":[],` +
		`"spec_checks":[{"item":"Reject expired tokens","status":"unmet",` +
		`"note":"the rotation job still embeds ` + "`" + leaked + "`" + `"}],` +
		`"receipts":[{"hunk":"notes.txt\tNEW\t1\t3","quote":"new line"}]}`
	postBundle(t, review)
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{}}
	var warnings, human []string
	req := postTestReq(cmdr, Flags{Approve: "false", ReviewFile: ".verdict/state/7/review.json"}, &warnings, &human)
	_, ref := Run(context.Background(), req)
	if ref == nil {
		t.Fatal("secret-shaped spec note posted; want a refusal")
	}
	if ref.Code != "post-secret-shaped" {
		t.Fatalf("code = %q, want post-secret-shaped (message %q)", ref.Code, ref.Message)
	}
	for _, want := range []string{"spec_check 1 (github-token)", "--allow-secret-values"} {
		if !strings.Contains(ref.Message, want) {
			t.Errorf("refusal %q misses %q", ref.Message, want)
		}
	}
	if strings.Contains(ref.Message, leaked) {
		t.Errorf("refusal republished the value: %q", ref.Message)
	}
	if len(cmdr.calls) != 0 {
		t.Errorf("refused post called the host: %q", cmdr.calls)
	}
}

// TestRunDryRunWarnsOnSecretShaped proves a dry run warns instead of refusing:
// the warning names the location, the shape, and the override, and the response
// still renders (the adapter exits 0). The summary body and a finding body are
// graded in one run, so a scan that only reads one of them fails.
//
//nolint:revive // one leg, asserting the warnings and the empty POST inline
func TestRunDryRunWarnsOnSecretShaped(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	postBundle(t, secretFindingsReview)
	cmdr := postCommander(t)
	var warnings, human []string
	req := postTestReq(cmdr, Flags{Approve: "false", ReviewFile: ".verdict/state/7/review.json", DryRun: true}, &warnings, &human)
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("dry run refused: %v", ref)
	}
	if resp == nil || resp.Data == nil {
		t.Fatal("dry run returned no data")
	}
	joined := strings.Join(resp.Warnings, "\n")
	for _, want := range []string{"finding 1 (aws-access-key-id)", "--allow-secret-values", "post-secret-shaped"} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings %q miss %q", joined, want)
		}
	}
	if strings.Contains(joined, "AKIAIOSFODNN7EXAMPLE") {
		t.Errorf("warning republished the value: %q", joined)
	}
	for _, c := range cmdr.calls {
		if strings.Contains(strings.Join(c, " "), "--method POST") {
			t.Errorf("dry run posted: %q", c)
		}
	}
}

// TestRunAllowSecretValuesRecordsOverride proves the override publishes the
// review and records which shapes it let through on the receipt.
//
//nolint:revive // one leg, asserting the POST and the receipt inline
func TestRunAllowSecretValuesRecordsOverride(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	postBundle(t, secretSummaryReview)
	cmdr := postCommander(t)
	var warnings, human []string
	req := postTestReq(cmdr, Flags{
		Approve: "false", ReviewFile: ".verdict/state/7/review.json", AllowSecretValues: true,
	}, &warnings, &human)
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("override refused: %v", ref)
	}
	if resp == nil || resp.Data == nil || resp.Data.PayloadHash == "" {
		t.Fatalf("override returned no posted data: %+v", resp)
	}
	posted := 0
	for _, c := range cmdr.calls {
		if strings.Contains(strings.Join(c, " "), "--method POST") {
			posted++
		}
	}
	if posted != 1 {
		t.Errorf("reviews calls = %d, want 1", posted)
	}
	raw, err := os.ReadFile(".verdict/state/7/post.json")
	if err != nil {
		t.Fatalf("read post.json: %v", err)
	}
	var receipt struct {
		SecretShapesAllowed []string `json:"secret_shapes_allowed"`
	}
	if err := json.Unmarshal(raw, &receipt); err != nil {
		t.Fatalf("post.json is not JSON: %v", err)
	}
	if got := strings.Join(receipt.SecretShapesAllowed, ","); got != "github-token" {
		t.Errorf("post.json secret_shapes_allowed = %q, want github-token", got)
	}
	if joined := strings.Join(warnings, "\n"); !strings.Contains(joined, "--allow-secret-values") {
		t.Errorf("override warned nothing about the flag: %q", joined)
	}
}

// TestReviewSecretSitesLocations proves the scan's three locations and its
// silence on clean text: the summary body, a comment body, and a suggestion are
// each one site, and a review with no credential reports none.
//
//nolint:gocognit,revive // the location matrix reads as one block
func TestReviewSecretSitesLocations(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		review string
		want   []string
	}{
		{"clean", `{"summary":"all good","findings":[{"claim":"nice"}]}`, nil},
		{
			name:   "summary and suggestion",
			review: `{"summary":"see sk-ant-api03-abcdefghij0123","findings":[{"claim":"ok","suggestion":"K = \"AKIAIOSFODNN7EXAMPLE\""}]}`,
			want:   []string{"summary body (anthropic-api-key)", "finding 1 (aws-access-key-id)"},
		},
		{
			name:   "two findings",
			review: `{"summary":"x","findings":[{"claim":"AKIAIOSFODNN7EXAMPLE"},{"claim":"ghp_0123456789abcdefghijABCDEFGHIJ012345"}]}`,
			want:   []string{"finding 1 (aws-access-key-id)", "finding 2 (github-token)"},
		},
		{
			name:   "receipt hunk and quote",
			review: `{"summary":"x","receipts":[{"hunk":"AKIAIOSFODNN7EXAMPLE.txt\tNEW\t1\t3","quote":"ok"},{"hunk":"a.txt\tNEW\t1\t3","quote":"ghp_0123456789abcdefghijABCDEFGHIJ012345"}]}`,
			want:   []string{"receipt 1 (aws-access-key-id)", "receipt 2 (github-token)"},
		},
		{"non-object comment", `{"summary":"x","findings":[7,{"claim":"AKIAIOSFODNN7EXAMPLE"}]}`, []string{"finding 2 (aws-access-key-id)"}},
		{
			name:   "spec note",
			review: `{"summary":"x","spec_checks":[{"item":"ok","status":"unmet","note":"rotate the leaked ghp_0123456789abcdefghijABCDEFGHIJ012345 first"}]}`,
			want:   []string{"spec_check 1 (github-token)"},
		},
		{
			name:   "finding and brief and spec paths",
			review: `{"summary":"x","findings":[{"path":"dir/AKIAIOSFODNN7EXAMPLE.go","claim":"ok"},{"path":"a.go","claim":"ok","brief":{"sites":[{"path":"dir/ghp_0123456789abcdefghijABCDEFGHIJ012345.go"}],"outcome":"fix"}}],"spec_checks":[{"item":"ok","status":"met","evidence":{"path":"dir/sk-ant-api03-abcdefghij0123.go","line":1,"quote":"ok"}}]}`,
			want:   []string{"finding 1 (aws-access-key-id)", "finding 2 (github-token)", "spec_check 1 (anthropic-api-key)"},
		},
		{
			name: "brief outcome, approach and questions",
			review: `{"summary":"x","findings":[{"claim":"ok","brief":` +
				`{"outcome":"Rotate AKIAIOSFODNN7EXAMPLE before merging.",` +
				`"approach":"Use ghp_0123456789abcdefghijABCDEFGHIJ012345 for the deploy.",` +
				`"questions":["Is sk-ant-api03-abcdefghij0123 the right key?"]}}]}`,
			want: []string{
				"finding 1 (aws-access-key-id)",
				"finding 1 (github-token)",
				"finding 1 (anthropic-api-key)",
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			doc, err := jq.DecodeOrdered([]byte(c.review))
			if err != nil {
				t.Fatalf("decode review: %v", err)
			}
			obj, ok := doc.(*jq.Obj)
			if !ok {
				t.Fatalf("review decoded as %T", doc)
			}
			st := &state{review: obj}
			if body, has := obj.Get("summary"); has {
				st.body = jq.R(body)
			}
			var got []string
			for _, site := range st.reviewSecretSites() {
				got = append(got, site.where+" ("+strings.Join(site.shapes, ", ")+")")
			}
			if strings.Join(got, "|") != strings.Join(c.want, "|") {
				t.Fatalf("sites = %v, want %v", got, c.want)
			}
		})
	}
}

// TestSecretSiteListCaps proves one line stays bounded on a review full of
// keys, and that the count carries what the cap dropped.
func TestSecretSiteListCaps(t *testing.T) {
	t.Parallel()
	sites := make([]secretSite, 0, maxSecretSites+2)
	for i := 1; i <= maxSecretSites+2; i++ {
		sites = append(sites, secretSite{where: "finding " + strconv.Itoa(i), shapes: []string{"github-token"}})
	}
	got := secretSiteList(sites)
	if !strings.HasSuffix(got, ", and 2 more") {
		t.Errorf("capped line %q misses the dropped count", got)
	}
	if strings.Contains(got, "finding 7") {
		t.Errorf("capped line %q names a site past the cap", got)
	}
	if got := secretSiteList([]secretSite{{where: "summary body", shapes: []string{"stripe-key"}}}); got != "summary body (stripe-key)" {
		t.Errorf("single site = %q", got)
	}
}

const (
	secretBriefReview = `{"schema_version":"1","body":"> ✅ Looks good — minor note below.","approved":false,` +
		`"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"Consider clarifying this line.",` +
		`"rigor":"pointed","severity":"informational",` +
		`"brief":{"outcome":"Rotate AKIAIOSFODNN7EXAMPLE before merging."}}]}`
	briefOmittedReview = `{"schema_version":"1","body":"> ✅ Looks good — minor note below.","approved":false,` +
		`"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"Consider clarifying this line.",` +
		`"rigor":"pointed","severity":"informational"}]}`
)

// TestRunRefusesSecretInheritedFromStoredBrief proves the scan covers what the
// merge publishes, not only what review.json carries: a stored brief whose
// outcome holds a credential rides into the body when a later same-signature
// review omits the brief, so that post must refuse as well.
func TestRunRefusesSecretInheritedFromStoredBrief(t *testing.T) {
	seedStoredSecretBrief(t)
	var warnings, human []string
	cmdr := postCommander(t)
	second := postTestReq(cmdr, Flags{Approve: "false", ReviewFile: ".verdict/state/7/review.json"}, &warnings, &human)
	_, ref := Run(context.Background(), second)
	if ref == nil || ref.Code != "post-secret-shaped" {
		t.Fatalf("inherited secret brief: refusal = %v, want post-secret-shaped", ref)
	}
	if strings.Contains(ref.Message, "AKIAIOSFODNN7EXAMPLE") {
		t.Errorf("refusal republished the value: %q", ref.Message)
	}
	for _, call := range cmdr.calls {
		if strings.Contains(strings.Join(call, " "), "--method POST") {
			t.Errorf("refused post submitted to the host: %q", call)
		}
	}
}

func seedStoredSecretBrief(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	postBundle(t, secretBriefReview)
	var warnings, human []string
	first := postTestReq(postCommander(t), Flags{
		Approve: "false", ReviewFile: ".verdict/state/7/review.json", DryRun: true, AllowSecretValues: true,
	}, &warnings, &human)
	if _, ref := Run(context.Background(), first); ref != nil {
		t.Fatalf("recording run refused: %v", ref)
	}
	if err := os.Rename(".verdict/state/7/findings.dryrun.json", ".verdict/state/7/findings.json"); err != nil {
		t.Fatalf("promote recorded findings: %v", err)
	}
	if err := writeTestFile(".verdict/state/7/review.json", briefOmittedReview); err != nil {
		t.Fatal(err)
	}
}

// TestRunRefusesSecretShapedBrief proves the pre-submit scan covers the
// structured brief on a review comment, not only the free-text fields: a
// credential in the brief refuses the post by finding location, makes no host
// call, and never reappears in the refusal message.
func TestRunRefusesSecretShapedBrief(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	const leaked = "ghp_0123456789abcdefghijABCDEFGHIJ012345"
	review := `{"schema_version":"1","body":"> ⚠️ Findings require attention.","approved":false,` +
		`"comments":[{"path":"notes.txt","line":2,"side":"RIGHT",` +
		`"body":"Rotate the leaked token before merging.",` +
		`"rigor":"pointed","severity":"critical",` +
		`"verification":{"quote":"verified"},` +
		`"brief":{"outcome":"Rotate ` + "`" + leaked + "`" + ` before merging."}}],` +
		`"receipts":[{"hunk":"notes.txt\tNEW\t1\t3","quote":"new line"}]}`
	postBundle(t, review)
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{}}
	var warnings, human []string
	req := postTestReq(cmdr, Flags{Approve: "false", ReviewFile: ".verdict/state/7/review.json"}, &warnings, &human)
	_, ref := Run(context.Background(), req)
	if ref == nil {
		t.Fatal("secret-shaped brief posted; want a refusal")
	}
	if ref.Code != "post-secret-shaped" {
		t.Fatalf("code = %q, want post-secret-shaped (message %q)", ref.Code, ref.Message)
	}
	for _, want := range []string{"finding 1 (github-token)", "--allow-secret-values"} {
		if !strings.Contains(ref.Message, want) {
			t.Errorf("refusal %q misses %q", ref.Message, want)
		}
	}
	if strings.Contains(ref.Message, leaked) {
		t.Errorf("refusal republished the value: %q", ref.Message)
	}
	if len(cmdr.calls) != 0 {
		t.Errorf("refused post called the host: %q", cmdr.calls)
	}
}
