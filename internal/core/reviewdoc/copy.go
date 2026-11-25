package reviewdoc

import (
	"github.com/stainless-code/verdict/internal/brief"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/untrusted"
)

// RenderFixPrompt renders the deterministic `Prompt for AI agent` block for
// one stored finding record: the same fenced prompt text the hosted review
// body embeds, so a copy button and a posted body can never disagree. It
// returns "" when the finding holds no prompt (closed, unverified, below
// important, or brief-less); a copy caller renders no button then.
func RenderFixPrompt(m *jq.Obj) string {
	if m == nil {
		return ""
	}
	b := brief.FromRecord(m)
	if !fixPromptEligible(m, b) {
		return ""
	}
	path := jq.StrOf(fieldVal(m, "path"))
	startLine, _ := jq.IntOf(fieldVal(m, "start_line"))
	endLine, _ := jq.IntOf(fieldVal(m, "line"))
	claim := untrusted.InlineProse(findingClaim(m))
	return fixPromptText(m, b, path, startLine, endLine, claim)
}
