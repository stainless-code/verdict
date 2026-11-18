// Package promptkeys holds the review.json key shapes a review prompt has to
// publish. internal/archtest holds the shipped prompt to this list, and
// bench/runner holds its derived tool-free prompt to it, so a key the schema
// needs cannot be named in one prompt and dropped from the other.
package promptkeys

// ReviewKey is one review.json key a prompt must name, the exact text it
// publishes it as, and the schema branch that defines it.
type ReviewKey struct {
	Key  string
	Text string
	Path []string
}

// Review lists the keys prompts/review.md publishes: a pilot review wrote
// `rung` and `in_bounds` because the prompt described the evidence rung and
// the in-bounds flag without naming their keys, and record repaired both
// instead of the review landing clean.
var Review = []ReviewKey{
	{"rigor", "`rigor`", findingBranch("rigor")},
	{"severity", "`severity`", findingBranch("severity")},
	{"claim", "`claim`", findingBranch("claim")},
	{"quote", "`quote`", findingBranch("quote")},
	{"confidence", "`confidence`", findingBranch("confidence")},
	{"file", `{"file": …, "ranges": …}`, coverageBranch("file")},
	{"ranges", `{"file": …, "ranges": …}`, coverageBranch("ranges")},
}

// RigorValues are the evidence rungs review.json accepts, in schema order. A
// prompt listing a value the schema refuses sends the model into a refusal,
// and one omitting a value hides an allowed rung.
var RigorValues = []string{"said", "pointed", "walked", "ran", "reproduced"}

// commentBranch is review.schema.json's path to one comment key.
func findingBranch(key string) []string {
	return []string{"$defs", "finding", "properties", key}
}

// coverageBranch is review.schema.json's path to one coverage entry key.
func coverageBranch(key string) []string {
	return []string{"$defs", "coverageClaim", "properties", key}
}
