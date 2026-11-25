package postcore

import (
	"strings"

	"github.com/stainless-code/verdict/internal/jq"
	sigsum "github.com/stainless-code/verdict/internal/sig"
)

func contentDigest(body, head string, comments []any) string {
	projected := make([]any, 0, len(comments))
	for _, comment := range comments {
		obj, ok := comment.(*jq.Obj)
		if !ok || obj == nil {
			continue
		}
		fields := jq.NewObj()
		for _, key := range []string{fieldPath, fieldSide, fieldLine, fieldBody} {
			value, _ := obj.Get(key)
			fields.Set(key, value)
		}
		projected = append(projected, fields)
	}
	return sigsum.Sum([]byte(jq.NewObj(fieldBody, body, "head", head, "comments", projected).MarshalPretty()))
}

// payloadsDigest is the digest of the exact review payloads a real post hands
// the host, in posting order: every body (the review marker and each
// follow-up part included), the event, the head, and the inline comments. A
// NUL joins them, which a JSON-encoded payload never contains.
func payloadsDigest(payloads []string) string {
	return sigsum.Sum([]byte(strings.Join(payloads, "\x00")))
}
