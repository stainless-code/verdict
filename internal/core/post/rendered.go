package postcore

import (
	"fmt"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// renderedComments lists every comment a real post sends, in posting order: the
// review body (with its hidden review marker, exactly as the host receives it),
// each follow-up part a split adds, then each inline comment. The posted digest
// covers all of them, so a caller that shows the operator what the digest pins
// must show all of them, not the head alone. It reads the state after
// buildPayloads.
func (st *state) renderedComments() []core.RenderedComment {
	parts := len(st.extraParts) + 1
	label := "Review body"
	if parts > 1 {
		label = fmt.Sprintf("Review body, part 1 of %d", parts)
	}
	out := []core.RenderedComment{{Label: label, Text: st.marked}}
	for i, extra := range st.extraParts {
		out = append(out, core.RenderedComment{
			Label: fmt.Sprintf("Follow-up review comment, part %d of %d", i+2, parts),
			Text:  splitFollowHead(i+2, parts) + extra,
		})
	}
	for _, c := range st.comments {
		obj, ok := c.(*jq.Obj)
		if !ok || obj == nil {
			continue
		}
		path, _ := obj.Get(fieldPath)
		line, _ := obj.Get(fieldLine)
		text, _ := obj.Get(fieldBody)
		out = append(out, core.RenderedComment{
			Label: fmt.Sprintf("Inline comment on %s:%s", jq.R(path), jq.R(line)),
			Text:  jq.R(jq.Alt(text, "")),
		})
	}
	return out
}
