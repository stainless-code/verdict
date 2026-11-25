package verifycore

import (
	"testing"

	"github.com/stainless-code/verdict/internal/contenttree"
)

// TestFilteredContentNamesGitLFS pins what an operator reads when content a
// filter driver handles keeps a run from binding: the warning and the group
// run's refusal name Git LFS and its pointers, not a dirty checkout, and say
// which claims can still land.
func TestFilteredContentNamesGitLFS(t *testing.T) {
	t.Parallel()
	lfs := contenttree.Filtered{Driver: "lfs", Paths: []string{"assets/logo.png", "data.bin"}}
	st := &state{req: &Request{}}
	st.warnLeftOut(&contenttree.Content{Filtered: []contenttree.Filtered{lfs, {Driver: "mark", Paths: []string{"x.txt"}}}})
	want := []string{
		"2 path(s) use the `lfs` filter, which verify never runs (assets/logo.png, data.bin): the snapshot holds " +
			"their working-tree content, which may differ from what the configured filter would produce (Git LFS commits a " +
			"pointer), so its tree matches only a commit that " +
			"stores those same bytes — a PR's VERIFIED claim binds to this run only if the PR's head stores those " +
			"bytes; a fetch-local bundle of this checkout still binds",
		"1 path(s) use the `mark` filter, which verify never runs (x.txt): the snapshot holds their working-tree " +
			"content, which may differ from what the configured filter would produce, so its tree matches only a commit that " +
			"stores those same bytes — a PR's VERIFIED claim binds to this run only if the PR's head stores those " +
			"bytes; a fetch-local bundle of this checkout still binds",
	}
	if len(st.warnings) != len(want) {
		t.Fatalf("warnings = %q, want %q", st.warnings, want)
	}
	for i := range want {
		if st.warnings[i] != want[i] {
			t.Errorf("warning %d = %q\nwant %q", i, st.warnings[i], want[i])
		}
	}
	run := &validationRun{root: "/repo", reviewedHead: "1234567890abcdef1234567890abcdef12345678"}
	ref := otherContent(run, []contenttree.Filtered{lfs})
	wantRef := "the content at /repo is not the reviewed commit 1234567's: 2 path(s) use the `lfs` filter, which " +
		"verify never runs (assets/logo.png, data.bin): the snapshot holds their working-tree content, which may " +
		"differ from what the configured filter would produce (Git LFS commits a pointer), so its tree matches only " +
		"a commit that stores those same bytes — validation certifies the " +
		"reviewed content only"
	if ref.Code != codeHeadMismatch || ref.Message != wantRef {
		t.Errorf("refusal = %s %q\nwant %s %q", ref.Code, ref.Message, codeHeadMismatch, wantRef)
	}
}
