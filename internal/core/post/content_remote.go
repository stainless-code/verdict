package postcore

import (
	"fmt"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

func remotePage(items []any) []map[string]any {
	var out []map[string]any
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		notes := jq.Elements(m["notes"])
		if len(notes) == 0 {
			out = append(out, m)
		} else {
			out = append(out, remotePage(notes)...)
		}
	}
	return out
}

const fieldSide = "side"

func remoteComment(m map[string]any) (string, *jq.Obj) {
	head := jq.StrOf(m["commit_id"])
	if head == "" {
		head = jq.StrOf(m["original_commit_id"])
	}
	obj := jq.NewObj(fieldPath, m[fieldPath], fieldSide, m[fieldSide], fieldLine, m[fieldLine], "body", m["body"])
	mapNumericPosition(m, obj)
	if pos, ok := m["position"].(map[string]any); ok {
		head = jq.StrOf(pos["head_sha"])
		obj.Set(fieldPath, pos["new_path"])
		obj.Set(fieldSide, "RIGHT")
		obj.Set(fieldLine, pos["new_line"])
		if jq.Falsey(pos["new_line"]) {
			obj.Set(fieldPath, pos["old_path"])
			obj.Set(fieldSide, "LEFT")
			obj.Set(fieldLine, pos["old_line"])
		}
	}
	return head, obj
}

func reviewContent(body string) string {
	if _, _, ok := reviewMarker(body); ok {
		before, _, _ := strings.CutLast(strings.TrimRight(body, " \t\r\n"), "\n\n")
		return before
	}
	return body
}

func (st *state) remoteDiff() *core.RefusalError {
	remote, previous := st.remoteEvidence()
	if previous != "" {
		st.previousHead = previous
		st.warn(fmt.Sprintf("rearmed: true; previous_head=%s; reviewed_head=%s", previous, st.commitID))
		st.forceBypass("head-rearmed", nil, nil, &previous)
	}
	if st.flags.Force {
		return nil
	}
	st.comments = missingComments(st.commitID, st.comments, remote)
	pending := st.missingPayloads()
	if len(pending) == 0 {
		return duplicatePayloadRefusal()
	}
	st.payloads = pending
	st.payload = pending[0]
	return nil
}

func (st *state) remoteBodyPresent(body string) bool {
	for _, rev := range st.liveReviews() {
		if rev.commitID == st.commitID && reviewContent(rev.body) == reviewContent(body) {
			return true
		}
	}
	return false
}

func (st *state) missingPayloads() []string {
	var pending []string
	for i, payload := range st.payloads {
		comments := []any{}
		if i == 0 && len(st.comments) > 0 {
			comments = st.comments
		}
		if part := st.missingPayload(payload, comments); part != "" {
			pending = append(pending, part)
		}
	}
	return pending
}

func (st *state) missingPayload(payload string, comments []any) string {
	doc, err := jq.DecodeOrdered([]byte(payload))
	obj, ok := doc.(*jq.Obj)
	if err != nil || !ok || obj == nil {
		return ""
	}
	body := orderedString(obj, "body")
	var summary any = body
	event := orderedString(obj, "event")
	if bindingEvent(event) && !st.bindingPending(event) {
		event = eventComment
	}
	if st.remoteBodyPresent(body) {
		if len(comments) == 0 && !st.bindingPending(event) {
			return ""
		}
		summary = nil
	}
	return postPayload(summary, event, commitIDValue(st.commitID), comments)
}

func (st *state) remoteEvidence() ([]commentIdentity, string) {
	var remote []commentIdentity
	previous := ""
	seenCurrent := false
	items := st.remoteInline
	for _, m := range items {
		head, obj := remoteComment(m)
		seenCurrent = seenCurrent || head == st.commitID
		remote = append(remote, identifyComment(head, obj))
		previous = priorHead(st.commitID, head, previous)
	}
	for _, rev := range st.liveReviews() {
		seenCurrent = seenCurrent || rev.commitID == st.commitID
		previous = priorHead(st.commitID, rev.commitID, previous)
	}
	if seenCurrent {
		previous = ""
	}
	return remote, previous
}

func priorHead(current, candidate, previous string) string {
	if candidate != "" && candidate != current {
		return candidate
	}
	return previous
}

func bindingEvent(event string) bool { return event == eventApprove || event == "REQUEST_CHANGES" }

func (st *state) bindingPending(event string) bool {
	if !bindingEvent(event) {
		return false
	}
	for _, rev := range st.liveReviews() {
		if rev.commitID == st.commitID && verdictStateMatches(event, rev.state) {
			return false
		}
	}
	return true
}

func verdictStateMatches(event, state string) bool {
	switch strings.ToUpper(state) {
	case "APPROVED":
		return event == eventApprove
	case "REQUEST_CHANGES", "CHANGES_REQUESTED":
		return event == "REQUEST_CHANGES"
	default:
		return false
	}
}

func mapNumericPosition(m map[string]any, obj *jq.Obj) {
	if _, hasLine := m[fieldLine]; hasLine {
		return
	}
	position, ok := jq.IntOf(m["position"])
	if !ok {
		return
	}
	if position > 0 {
		obj.Set(fieldLine, position)
		return
	}
	original, ok := jq.IntOf(m["original_position"])
	if position == 0 && ok && original > 0 {
		obj.Set(fieldLine, original)
		obj.Set(fieldSide, "LEFT")
	}
}
