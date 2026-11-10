package postcore

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
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
func TestRunRefusesSecretShapedBeforeHostCalls(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	postBundle(t, secretFindingsReview)
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{}}
	var warnings, human []string
	req := postTestReq(cmdr, core.PostFlags{Approve: "false", ReviewFile: ".verdict/state/7/review.json"}, &warnings, &human)
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

// TestRunDryRunWarnsOnSecretShaped proves a dry run warns instead of refusing:
// the warning names the location, the shape, and the override, and the response
// still renders (the adapter exits 0). The summary body and a finding body are
// graded in one run, so a scan that only reads one of them fails.
func TestRunDryRunWarnsOnSecretShaped(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	postBundle(t, secretFindingsReview)
	cmdr := postCommander(t)
	var warnings, human []string
	req := postTestReq(cmdr, core.PostFlags{Approve: "false", ReviewFile: ".verdict/state/7/review.json", DryRun: true}, &warnings, &human)
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
		if strings.Contains(strings.Join(c, " "), "/reviews") {
			t.Errorf("dry run posted: %q", c)
		}
	}
}

// TestRunAllowSecretValuesRecordsOverride proves the override publishes the
// review and records which shapes it let through on the receipt.
func TestRunAllowSecretValuesRecordsOverride(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	postBundle(t, secretSummaryReview)
	cmdr := postCommander(t)
	var warnings, human []string
	req := postTestReq(cmdr, core.PostFlags{
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
		if strings.Contains(strings.Join(c, " "), "/reviews") {
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
func TestReviewSecretSitesLocations(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		review string
		want   []string
	}{
		{"clean", `{"body":"all good","comments":[{"body":"nice"}]}`, nil},
		{
			name:   "summary and suggestion",
			review: `{"body":"see sk-ant-api03-abcdefghij0123","comments":[{"body":"ok","suggestion":"K = \"AKIAIOSFODNN7EXAMPLE\""}]}`,
			want:   []string{"summary body (anthropic-api-key)", "finding 1 (aws-access-key-id)"},
		},
		{
			name:   "two findings",
			review: `{"body":"x","comments":[{"body":"AKIAIOSFODNN7EXAMPLE"},{"body":"ghp_0123456789abcdefghijABCDEFGHIJ012345"}]}`,
			want:   []string{"finding 1 (aws-access-key-id)", "finding 2 (github-token)"},
		},
		{"non-object comment", `{"body":"x","comments":[7,{"body":"AKIAIOSFODNN7EXAMPLE"}]}`, []string{"finding 2 (aws-access-key-id)"}},
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
			if body, has := obj.Get("body"); has {
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
