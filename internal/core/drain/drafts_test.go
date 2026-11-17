package drain

import (
	"encoding/json"
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

func draftDoc(t *testing.T, mutate func(map[string]any)) []byte {
	t.Helper()
	doc := map[string]any{
		"schema_version": "1", "plan_digest": draftPlan().PlanDigest,
		"replies": []any{map[string]any{
			"thread_id": "t1", "waiting_comment_id": "w1", "disposition": "answer", "body": "a real answer",
		}},
	}
	if mutate != nil {
		mutate(doc)
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestCheckDraftsSites(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, site, code string
		mutate           func(map[string]any)
	}{
		{name: "unknown root key", site: "root.extra", code: "drafts-invalid", mutate: func(d map[string]any) { d["extra"] = true }},
		{name: "unknown entry key", site: "replies[0].extra", code: "drafts-invalid", mutate: func(d map[string]any) {
			d["replies"].([]any)[0].(map[string]any)["extra"] = true
		}},
		{name: "unknown thread", site: "replies[0].thread_id", code: "drafts-invalid", mutate: func(d map[string]any) {
			d["replies"].([]any)[0].(map[string]any)["thread_id"] = "t9"
		}},
		{name: "unknown comment", site: "replies[0].waiting_comment_id", code: "drafts-invalid", mutate: func(d map[string]any) {
			d["replies"].([]any)[0].(map[string]any)["waiting_comment_id"] = "w9"
		}},
		{name: "bad disposition", site: "replies[0].disposition", code: "drafts-invalid", mutate: func(d map[string]any) {
			d["replies"].([]any)[0].(map[string]any)["disposition"] = "solve"
		}},
		{name: "empty body", site: "replies[0].body", code: "drafts-invalid", mutate: func(d map[string]any) {
			d["replies"].([]any)[0].(map[string]any)["body"] = ""
		}},
		{name: "over-cap body", site: "replies[0].body", code: "drafts-invalid", mutate: func(d map[string]any) {
			d["replies"].([]any)[0].(map[string]any)["body"] = strings.Repeat("é", MaxBodyRunes+1)
		}},
		{name: "marker body", site: "replies[0].body", code: "drafts-invalid", mutate: func(d map[string]any) {
			d["replies"].([]any)[0].(map[string]any)["body"] = "answer <!-- verdict-finding sig=abc -->"
		}},
		{name: "stale digest", site: "", code: "drafts-stale", mutate: func(d map[string]any) { d["plan_digest"] = "stale" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, _, ref := CheckDrafts(draftDoc(t, tc.mutate), draftPlan())
			if ref == nil || ref.Code != tc.code || !strings.Contains(ref.Message, tc.site) {
				t.Errorf("refusal = %+v, want %s naming %q", ref, tc.code, tc.site)
			}
		})
	}
}

func TestCheckDraftsDuplicateAndGoodDraft(t *testing.T) {
	t.Parallel()
	dup := draftDoc(t, func(d map[string]any) {
		d["replies"] = append(d["replies"].([]any), d["replies"].([]any)[0])
	})
	_, _, ref := CheckDrafts(dup, draftPlan())
	if ref == nil || ref.Code != "drafts-invalid" || !strings.Contains(ref.Message, "replies[1].thread_id") {
		t.Errorf("duplicate refusal = %+v", ref)
	}
	good := draftDoc(t, func(d map[string]any) {
		d["replies"] = []any{map[string]any{
			"thread_id": "t1", "waiting_comment_id": "w1", "disposition": "defer", "body": "",
		}}
	})
	summary, skipped, ref := CheckDrafts(good, draftPlan())
	if ref != nil || summary == nil || summary.Count != 1 || summary.Dispositions.Defer != 1 || len(skipped) != 0 {
		t.Errorf("good draft = %+v, skipped %+v, refusal %+v", summary, skipped, ref)
	}
	missing := draftDoc(t, func(d map[string]any) { d["replies"] = []any{} })
	summary, skipped, ref = CheckDrafts(missing, draftPlan())
	if ref != nil || summary == nil || summary.Count != 0 || len(skipped) != 1 || skipped[0].Reason != ReasonNoDraft {
		t.Errorf("missing draft = %+v, skipped %+v, refusal %+v", summary, skipped, ref)
	}
}
