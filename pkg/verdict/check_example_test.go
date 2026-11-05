package verdict_test

import (
	"fmt"

	"github.com/stainless-code/verdict/pkg/verdict"
)

// ExampleCheckReview checks an agent's review against the diff it reviewed
// without a store: every problem comes back at once, with its fix.
func ExampleCheckReview() {
	diff := "diff --git a/main.go b/main.go\n--- a/main.go\n+++ b/main.go\n@@ -1,2 +1,2 @@\n package main\n-var x = 1\n+var x = 2\n"
	review := []byte(`{"body":"> ✅ body long enough to pass the guard","approved":false,"comments":[{"path":"main.go","line":2,"body":"x changed","severity":"urgent","brief":{"questions":"why?"}}]}`)
	res := verdict.CheckReview(review, diff)
	for _, p := range res.Problems {
		fmt.Println(p.Level, p.Code, p.Path)
	}
	// Output:
	// error bad-enum $.comments[0].severity
	// error missing-field $.comments[0].brief.outcome
	// error wrong-type $.comments[0].brief.questions
	// warning rigor-unset $.comments[0].rigor
	// warning missing-field $.schema_version
}
