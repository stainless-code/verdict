package postcore

import (
	"strings"

	threadscore "github.com/stainless-code/verdict/internal/core/threads"
	"github.com/stainless-code/verdict/internal/jq"
	sigsum "github.com/stainless-code/verdict/internal/sig"
)

type commentIdentity struct {
	head, path, side, body string
	line                   int
}

func identifyComment(head string, comment *jq.Obj) commentIdentity {
	line, _ := comment.Get("line")
	n, _ := jq.IntOf(line)
	body := orderedString(comment, "body")
	if _, valid := threadscore.FindingSig(body); valid {
		body, _, _ = strings.CutLast(strings.TrimRight(body, " \t\r\n"), "\n\n")
	}
	side := orderedString(comment, "side")
	if side == "" {
		side = "RIGHT"
	}
	return commentIdentity{
		head: head, path: orderedString(comment, "path"),
		side: side, line: n, body: sigsum.Sum([]byte(body)),
	}
}

func (key commentIdentity) sameFinding(other commentIdentity) bool {
	return key.head == other.head && key.path == other.path && key.side == other.side && key.body == other.body
}

func missingComments(head string, comments []any, remote []commentIdentity) []any {
	used := make([]bool, len(remote))
	paired := make([]bool, len(comments))
	pairComments(head, comments, remote, used, paired, true)
	pairComments(head, comments, remote, used, paired, false)
	var missing []any
	for i, value := range comments {
		if !paired[i] {
			missing = append(missing, value)
		}
	}
	return missing
}

func matchOccurrence(key commentIdentity, remote []commentIdentity, used []bool, exact bool) int {
	for i, previous := range remote {
		if !used[i] && key.sameFinding(previous) && (!exact || key.line == previous.line) {
			return i
		}
	}
	return -1
}

func pairComments(head string, comments []any, remote []commentIdentity, used, paired []bool, exact bool) {
	for i, value := range comments {
		if paired[i] {
			continue
		}
		j := matchComment(head, value, remote, used, exact)
		if j >= 0 {
			paired[i] = true
			used[j] = true
		}
	}
}

func matchComment(head string, value any, remote []commentIdentity, used []bool, exact bool) int {
	comment, ok := value.(*jq.Obj)
	if !ok || comment == nil {
		return -1
	}
	return matchOccurrence(identifyComment(head, comment), remote, used, exact)
}
