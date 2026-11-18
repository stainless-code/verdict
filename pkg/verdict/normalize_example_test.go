package verdict_test

import (
	"fmt"

	"github.com/stainless-code/verdict/pkg/verdict"
)

// ExampleNormalizeReview repairs a fenced review with an aliased key and a
// case-folded severity, then reports each rewrite.
func ExampleNormalizeReview() {
	review := []byte("```json\n" +
		`{"schema_version":"1","findings":[{"path":"main.go","line":2,"severity":"critical","claim":"x changed","quote":"var x = 2","confidence":"high"}]}` +
		"\n```")
	normalized, repairs := verdict.NormalizeReview(review)
	fmt.Print(string(normalized))
	for _, repair := range repairs {
		fmt.Println(repair.Code, repair.Path)
	}
	// Output:
	// {
	//   "schema_version": "1",
	//   "findings": [
	//     {
	//       "path": "main.go",
	//       "line": 2,
	//       "severity": "critical",
	//       "claim": "x changed",
	//       "quote": "var x = 2",
	//       "confidence": "high"
	//     }
	//   ]
	// }
	// normalized-fence $
}
