package postcore

import (
	"strings"

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
	if before, _, found := strings.Cut(body, "\n\n<!-- verdict-finding"); found {
		body = before
	}
	return commentIdentity{head: head, path: orderedString(comment, "path"), side: orderedString(comment, "side"), line: n, body: sigsum.Sum([]byte(body))}
}

func (key commentIdentity) sameFinding(other commentIdentity) bool {
	return key.head == other.head && key.path == other.path && key.side == other.side && key.body == other.body
}

func missingComments(head string, comments []any, remote []commentIdentity) []any {
	var missing []any
	for _, value := range comments {
		comment, ok := value.(*jq.Obj)
		if !ok || comment == nil {
			continue
		}
		key := identifyComment(head, comment)
		found := false
		for _, previous := range remote {
			if key.sameFinding(previous) {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, comment)
		}
	}
	return missing
}
