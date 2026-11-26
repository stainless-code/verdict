package drain

import (
	"testing"

	"github.com/stainless-code/verdict/internal/forgedline"
)

// A reply thread id comes from the agent's drain.replies.json.
func TestReplyThreadCannotForgeErrorLine(t *testing.T) {
	t.Parallel()
	ref := checkReply(Reply{ThreadID: forgedline.Value}, map[string]Pending{}, map[string]bool{}, "replies[0]", &Drafts{})
	forgedline.AssertOneErrorLine(t, ref, "thread "+forgedline.Named+" is not pending")
}
