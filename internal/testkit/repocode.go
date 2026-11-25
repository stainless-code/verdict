package testkit

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// repoHooks are the hooks git can run while verdict reads, stages, commits,
// or checks out content: each writes a marker named after itself.
var repoHooks = []string{
	"pre-commit", "prepare-commit-msg", "commit-msg", "post-commit",
	"post-checkout", "post-index-change", "reference-transaction",
}

// hookMode makes an armed hook or tool script executable.
const hookMode = 0o755

// shRun is the config value that runs one tool script through sh.
func shRun(path string) string { return "sh '" + path + "'\n" }

// RepoCode is a checkout armed so that every program git could run on its
// behalf leaves a marker: hooks under an in-tree core.hooksPath (.husky, as
// husky and lefthook set it), an fsmonitor, a clean/smudge filter and a
// textconv driver the checkout's .gitattributes selects for every path, and
// an external diff program.
type RepoCode struct {
	// Markers is the directory each program touches its name into.
	Markers string
}

// ArmRepoCode arms the non-worktree checkout at root. It writes the
// configuration into .git/config directly and runs no git, so nothing fires
// while arming; the hooks and .gitattributes are untracked files in the
// tree, as a pull request would carry them.
func ArmRepoCode(tb testing.TB, root string) RepoCode {
	tb.Helper()
	markers := filepath.Join(tb.TempDir(), "markers")
	tools := tb.TempDir()
	if err := os.MkdirAll(markers, fixtureDirMode); err != nil {
		tb.Fatal(err)
	}
	touch := func(name string) string {
		return "touch '" + filepath.ToSlash(filepath.Join(markers, name)) + "'\n"
	}
	script := func(dir, name, body string) string {
		path := filepath.Join(dir, name)
		WriteFile(tb, path, "#!/bin/sh\n"+body)
		if err := os.Chmod(path, hookMode); err != nil {
			tb.Fatal(err)
		}
		return filepath.ToSlash(path)
	}
	for _, hook := range repoHooks {
		script(filepath.Join(root, ".husky"), hook, touch(hook)+"cat >/dev/null 2>&1 || true\n")
	}
	WriteFile(tb, filepath.Join(root, ".gitattributes"), "* filter=mark diff=mark\n")
	config := "[core]\n\thooksPath = .husky\n" +
		"\tfsmonitor = " + script(tools, "fsmonitor", touch("fsmonitor")) + "\n" +
		"[filter \"mark\"]\n" +
		"\tclean = " + shRun(script(tools, "clean", touch("clean")+"cat\n")) +
		"\tsmudge = " + shRun(script(tools, "smudge", touch("smudge")+"cat\n")) +
		"\trequired = true\n" +
		"[diff \"mark\"]\n" +
		"\ttextconv = " + shRun(script(tools, "textconv", touch("textconv")+"cat \"$1\"\n")) +
		"[diff]\n" +
		"\texternal = " + shRun(script(tools, "extdiff", touch("extdiff")))
	appendConfig(tb, root, config)
	return RepoCode{Markers: markers}
}

// appendConfig appends text to root's .git/config without running git.
func appendConfig(tb testing.TB, root, text string) {
	tb.Helper()
	f, err := os.OpenFile(filepath.Join(root, ".git", "config"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		tb.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(text); err != nil {
		tb.Fatal(err)
	}
}

// Worktrees extends the arming at root to every worktree checked out from it,
// as a checked-out head that commits its own .husky and .gitattributes
// would: the attributes move to .git/info/attributes and the hooks path
// becomes absolute.
func (r RepoCode) Worktrees(tb testing.TB, root string) {
	tb.Helper()
	WriteFile(tb, filepath.Join(root, ".git", "info", "attributes"), "* filter=mark diff=mark\n")
	appendConfig(tb, root, "[core]\n\thooksPath = "+filepath.ToSlash(filepath.Join(root, ".husky"))+"\n")
}

// Ran lists the programs that left a marker, except the named ones.
func (r RepoCode) Ran(tb testing.TB, except ...string) []string {
	tb.Helper()
	entries, err := os.ReadDir(r.Markers)
	if err != nil {
		tb.Fatal(err)
	}
	var ran []string
	for _, e := range entries {
		if !slices.Contains(except, e.Name()) {
			ran = append(ran, e.Name())
		}
	}
	return ran
}
