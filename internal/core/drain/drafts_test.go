package drain

import (
	"strings"
	"testing"
)

func draftPlan() *File {
	return &File{
		SchemaVersion: SchemaVersion, PlanDigest: Digest([]Pending{{
			ThreadID: "t1", WaitingCommentID: "w1",
		}}),
		Pending: []Pending{{ThreadID: "t1", WaitingCommentID: "w1"}},
	}
}

func validDraft() string {
	return draftEnvelope(`,"replies":[{"thread_id":"t1","waiting_comment_id":"w1","disposition":"answer","body":"a real answer"}]`)
}

func draftEnvelope(replies string) string {
	return `{"schema_version":"1","plan_digest":"` + draftPlan().PlanDigest + `"` + replies + `}`
}

func replace(raw, from, to string) string {
	if !strings.Contains(raw, from) {
		return raw + "/* missing target */"
	}
	return strings.Replace(raw, from, to, 1)
}

const bodyField = `"body":"a real answer"`

func TestCheckDraftsSites(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, site, code, reason, raw string
	}{
		{
			name: "missing replies", site: "replies", code: "drafts-invalid",
			raw: draftEnvelope(""),
		},
		{
			name: "null replies", site: "replies", code: "drafts-invalid",
			raw: draftEnvelope(`,"replies":null`),
		},
		{
			name: "unknown root key", site: "root.extra", code: "drafts-invalid",
			raw: replace(validDraft(), `{"schema_version"`, `{"extra":true,"schema_version"`),
		},
		{
			name: "unknown entry key", site: "replies[0].extra", code: "drafts-invalid",
			raw: replace(validDraft(), bodyField, bodyField+`,"extra":true`),
		},
		{
			name: "missing thread id", site: "replies[0].thread_id", code: "drafts-invalid", reason: "missing key",
			raw: replace(validDraft(), `"thread_id":"t1",`, ""),
		},
		{
			name: "missing waiting comment id", site: "replies[0].waiting_comment_id", code: "drafts-invalid", reason: "missing key",
			raw: replace(validDraft(), `,"waiting_comment_id":"w1"`, ""),
		},
		{
			name: "missing disposition", site: "replies[0].disposition", code: "drafts-invalid", reason: "missing key",
			raw: replace(validDraft(), `,"disposition":"answer"`, ""),
		},
		{
			name: "missing body", site: "replies[0].body", code: "drafts-invalid", reason: "missing key",
			raw: replace(validDraft(), `,"body":"a real answer"`, ""),
		},
		{
			name: "missing defer body", site: "replies[0].body", code: "drafts-invalid", reason: "missing key",
			raw: replace(replace(validDraft(), `"disposition":"answer"`, `"disposition":"defer"`),
				`,"body":"a real answer"`, ""),
		},
		{
			name: "unknown thread", site: "replies[0].thread_id", code: "drafts-invalid",
			raw: replace(validDraft(), `"thread_id":"t1"`, `"thread_id":"t9"`),
		},
		{
			name: "unknown comment", site: "replies[0].waiting_comment_id", code: "drafts-invalid",
			raw: replace(validDraft(), `"waiting_comment_id":"w1"`, `"waiting_comment_id":"w9"`),
		},
		{
			name: "bad disposition", site: "replies[0].disposition", code: "drafts-invalid",
			raw: replace(validDraft(), `"disposition":"answer"`, `"disposition":"solve"`),
		},
		{
			name: "empty body", site: "replies[0].body", code: "drafts-invalid",
			raw: replace(validDraft(), bodyField, `"body":""`),
		},
		{
			name: "over-cap body", site: "replies[0].body", code: "drafts-invalid",
			raw: replace(validDraft(), `a real answer`, strings.Repeat("é", MaxBodyRunes+1)),
		},
		{
			name: "over-cap defer body", site: "replies[0].body", code: "drafts-invalid",
			raw: replace(replace(validDraft(), `"disposition":"answer"`, `"disposition":"defer"`), `a real answer`, strings.Repeat("x", MaxBodyRunes+1)),
		},
		{
			name: "whitespace body", site: "replies[0].body", code: "drafts-invalid",
			raw: replace(validDraft(), `a real answer`, ` \t\n`),
		},
		{
			name: "marker body", site: "replies[0].body", code: "drafts-invalid",
			raw: replace(validDraft(), `a real answer`, `answer <!-- verdict-finding sig=abc -->`),
		},
		{
			name: "stale digest", site: "", code: "drafts-stale",
			raw: replace(validDraft(), draftPlan().PlanDigest, "stale"),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, _, ref := CheckDrafts([]byte(tc.raw), draftPlan())
			if ref == nil || ref.Code != tc.code || !strings.Contains(ref.Message, tc.site) ||
				(tc.reason != "" && !strings.Contains(ref.Message, tc.reason)) {
				t.Errorf("refusal = %+v, want %s naming %q with %q", ref, tc.code, tc.site, tc.reason)
			}
		})
	}
}

func TestCheckDraftsGoodAndMissing(t *testing.T) {
	t.Parallel()
	deferRaw := replace(replace(validDraft(), `"disposition":"answer"`, `"disposition":"defer"`), bodyField, `"body":""`)
	summary, skipped, ref := CheckDrafts([]byte(deferRaw), draftPlan())
	if ref != nil || summary == nil || summary.Count != 1 || summary.Dispositions.Defer != 1 || len(skipped) != 0 {
		t.Errorf("good draft = %+v, skipped %+v, refusal %+v", summary, skipped, ref)
	}
	noReplies := `{"schema_version":"1","plan_digest":"` + draftPlan().PlanDigest + `","replies":[]}`
	summary, skipped, ref = CheckDrafts([]byte(noReplies), draftPlan())
	if ref != nil || summary == nil || summary.Count != 0 || len(skipped) != 1 || skipped[0].Reason != ReasonNoDraft {
		t.Errorf("missing draft = %+v, skipped %+v, refusal %+v", summary, skipped, ref)
	}
}

func TestCheckDraftsDuplicateThread(t *testing.T) {
	t.Parallel()
	one := `{"thread_id":"t1","waiting_comment_id":"w1","disposition":"answer","body":"a real answer"}`
	dup := `{"schema_version":"1","plan_digest":"` + draftPlan().PlanDigest + `","replies":[` + one + `,` + one + `]}`
	_, _, ref := CheckDrafts([]byte(dup), draftPlan())
	if ref == nil || ref.Code != "drafts-invalid" || !strings.Contains(ref.Message, "replies[1].thread_id") {
		t.Errorf("duplicate refusal = %+v", ref)
	}
}
