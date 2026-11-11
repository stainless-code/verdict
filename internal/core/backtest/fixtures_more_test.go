package backtest

import (
	"fmt"
	"testing"
)

// bugRepo is a merged PR that introduced one missing error check.
type bugRepo struct {
	r             *repo
	base, feature string
	merge         string
}

// newMergedBugRepo builds the merged PR without the later fix.
func newMergedBugRepo(t *testing.T) bugRepo {
	t.Helper()
	r := newRepo(t)
	r.write("main.go", "package main\n\nimport \"os\"\n\nfunc main() {\n\tos.Remove(\"out.txt\")\n}\n")
	base := r.commit("feat: base")
	r.switchNew("feature")
	r.write("main.go", "package main\n\nimport \"os\"\n\nfunc main() {\n\tos.Remove(\"missing\")\n}\n")
	feature := r.commit("feat: remove the output")
	r.switchMain()
	merge := r.merge("feature", "Merge pull request #2 from acme/feature")
	return bugRepo{r: r, base: base, feature: feature, merge: merge}
}

// newMergedFixRepo is the canonical fixture: a merged PR introduces the
// defect, and a later fix deletes the blamed line and adds the check.
func newMergedFixRepo(t *testing.T) (bugRepo, string) {
	t.Helper()
	b := newMergedBugRepo(t)
	b.r.write("main.go", "package main\n\nimport \"os\"\n\nfunc main() {\n\tif err := os.Remove(\"missing\"); err != nil {\n\t\tpanic(err)\n\t}\n}\n")
	return b, b.r.commit("fix: guard the removal")
}

// newRevertRepo is a merged PR whose later fix is a revert commit.
func newRevertRepo(t *testing.T) (bugRepo, string) {
	t.Helper()
	b := newMergedBugRepo(t)
	b.r.write("main.go", "package main\n\nimport \"os\"\n\nfunc main() {\n\tos.Remove(\"out.txt\")\n}\n")
	return b, b.r.commit(`Revert "feat: remove the output"`)
}

// newSquashRepo lands the bug as a squash commit whose subject ends (#7).
func newSquashRepo(t *testing.T) (r *repo, base, squash, fix string) {
	t.Helper()
	r = newRepo(t)
	r.write("a.txt", "one\n")
	base = r.commit("feat: base")
	r.switchNew("feature")
	r.write("a.txt", "bug\n")
	r.commit("feat: introduce bug")
	r.switchMain()
	squash = r.squash("feature", "feat: introduce bug (#7)")
	r.write("a.txt", "fixed\n")
	fix = r.commit("fix: repair a.txt")
	return r, base, squash, fix
}

// newDirectRepo lands the bug as a direct push with no containing merge.
func newDirectRepo(t *testing.T) (r *repo, base, bug, fix string) {
	t.Helper()
	r = newRepo(t)
	r.write("a.txt", "one\n")
	base = r.commit("feat: base")
	r.write("a.txt", "bug\n")
	bug = r.commit("feat: introduce bug")
	r.write("a.txt", "fixed\n")
	fix = r.commit("fix: repair a.txt")
	return r, base, bug, fix
}

// newUnalignedRepo introduces a line whose text already exists in the file, so
// the post-fix grep cannot identify one line.
func newUnalignedRepo(t *testing.T) (r *repo, base, feature, fix string) {
	t.Helper()
	r = newRepo(t)
	r.write("main.go", "package main\n\nfunc main() {\n\tx := 1\n\tprintln(x)\n}\n")
	base = r.commit("feat: base")
	r.switchNew("feature")
	r.write("main.go", "package main\n\nfunc main() {\n\tx := 1\n\tprintln(x)\n}\n\nfunc extra() {\n\tx := 1\n\tprintln(x)\n}\n")
	feature = r.commit("feat: add extra")
	r.switchMain()
	r.merge("feature", "Merge pull request #1 from acme/feature")
	r.write("main.go", "package main\n\nfunc main() {\n\tx := 1\n\tprintln(x)\n}\n\nfunc extra() {\n\tprintln(1)\n}\n")
	fix = r.commit("fix: drop duplicate")
	return r, base, feature, fix
}

// twoCandidateRepo builds two merged-and-fixed PRs on different days.
func twoCandidateRepo(t *testing.T) (*repo, string) {
	t.Helper()
	r := newRepo(t)
	r.write("a.txt", "one\n")
	r.write("b.txt", "one\n")
	base := r.commit("feat: base")
	for i, name := range []string{"a.txt", "b.txt"} {
		branch := fmt.Sprintf("feature%d", i+1)
		r.switchNew(branch)
		r.write(name, "bug\n")
		r.commit("feat: introduce bug in " + name)
		r.switchMain()
		r.merge(branch, fmt.Sprintf("Merge pull request #%d from acme/%s", i+1, branch))
		r.write(name, "fixed\n")
		r.commit("fix: repair " + name)
	}
	return r, base
}

// conflictRepo resolves a merge conflict to a buggy line, so the merge commit
// itself is the blamed introducer.
func conflictRepo(t *testing.T) (r *repo, base, feature, merge, fix string) {
	t.Helper()
	r = newRepo(t)
	r.write("a.txt", "one\n")
	base = r.commit("feat: base")
	r.switchNew("feature")
	r.write("a.txt", "feature\n")
	feature = r.commit("feat: feature side")
	r.switchMain()
	r.write("a.txt", "main\n")
	r.commit("feat: main side")
	r.gitAllow("merge", "--no-ff", "feature")
	r.write("a.txt", "bug\n")
	r.git("add", "-A")
	r.git("commit", "-qm", "Merge branch 'feature'")
	r.day++
	merge = r.head()
	r.write("a.txt", "fixed\n")
	fix = r.commit("fix: repair a.txt")
	return r, base, feature, merge, fix
}
