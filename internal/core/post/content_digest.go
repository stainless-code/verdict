package postcore

import (
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
		for _, key := range []string{"path", "side", "line", "body"} {
			value, _ := obj.Get(key)
			fields.Set(key, value)
		}
		projected = append(projected, fields)
	}
	return sigsum.Sum([]byte(jq.NewObj("body", body, "head", head, "comments", projected).MarshalPretty()))
}
