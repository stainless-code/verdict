package verdict_test

import (
	"fmt"

	"github.com/stainless-code/verdict/pkg/verdict"
)

// ExampleNormalizeReview repairs a fenced review with an aliased key and a
// case-folded severity, then reports each rewrite.
func ExampleNormalizeReview() {
	review := []byte("```json\n" +
		`{"schema_version":"1","body":"> ✅ body long enough to pass the guard","approved":false,` +
		`"comments":[{"file":"main.go","line":2,"body":"x changed","severity":"Critical"}]}` +
		"\n```")
	normalized, repairs := verdict.NormalizeReview(review)
	fmt.Print(string(normalized))
	for _, repair := range repairs {
		fmt.Println(repair.Code, repair.Path)
	}
	// Output:
	// {
	//   "schema_version": "1",
	//   "body": "> ✅ body long enough to pass the guard",
	//   "approved": false,
	//   "comments": [
	//     {
	//       "path": "main.go",
	//       "line": 2,
	//       "body": "x changed",
	//       "severity": "critical"
	//     }
	//   ]
	// }
	// normalized-fence $
	// key-alias $.comments[0].path
	// enum-synonym $.comments[0].severity
}
