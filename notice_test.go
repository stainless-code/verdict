package skillfs_test

import (
	"os"
	"path"
	"slices"
	"strings"
	"testing"

	skillfs "github.com/stainless-code/verdict"
)

// The adapted prompts ship as MIT text, so the notice has to reach every copy
// of them: this file pins the upstream commit, keeps the two notice copies
// byte-identical, and fails when a prompt is missing from the payload's list.
const (
	noticeRoot    = "NOTICE.md"
	noticePayload = "NOTICE.md" // relative to skillfs.Dir
	licenseStart  = "<!-- upstream-license:start -->"
	licenseEnd    = "<!-- upstream-license:end -->"

	// upstreamPin is the Pullfrog commit the adapted prompts came from. The
	// license block below was diffed byte-for-byte against that commit's
	// LICENSE: moving the pin means re-checking the text, not just the hash.
	upstreamPin = "bef4f79cef8c9793608ef50179bae9e925231e38"

	// copyrightLine is the MIT notice's copyright, which MIT requires in all
	// copies. It is the one line that cannot be inferred from the clauses.
	copyrightLine = "Copyright (c) 2026 Pullfrog, Inc."

	// permissionClause is the sentence that makes the notice a notice.
	permissionClause = "The above copyright notice and this permission notice shall be included in all\n" +
		"copies or substantial portions of the Software."
)

// TestNoticeCoversAdaptedPaths keeps the license notice honest: the root and
// payload copies carry the same verbatim block, both name the pinned upstream
// commit, and the payload's list matches the files it actually ships.
func TestNoticeCoversAdaptedPaths(t *testing.T) {
	t.Parallel()
	rawRoot, err := os.ReadFile(noticeRoot)
	if err != nil {
		t.Fatalf("read %s: %v", noticeRoot, err)
	}
	root := string(rawRoot)
	payload := string(mustRead(t, noticePayload))

	if !strings.Contains(root, "`skills/verdict/prompts/`") {
		t.Errorf("%s does not name the adapted prompts directory", noticeRoot)
	}
	if !strings.Contains(root, "`skills/verdict/NOTICE.md`") {
		t.Errorf("%s does not point at the payload copy that travels with the skill", noticeRoot)
	}
	for _, notice := range []struct{ name, body string }{
		{noticeRoot, root},
		{path.Join(skillfs.Dir, noticePayload), payload},
	} {
		if !strings.Contains(notice.body, "`"+upstreamPin+"`") {
			t.Errorf("%s does not pin upstream commit %s", notice.name, upstreamPin)
		}
		block, ok := licenseBlock(notice.body)
		if !ok {
			t.Errorf("%s has no %s/%s block", notice.name, licenseStart, licenseEnd)
			continue
		}
		if !strings.Contains(block, copyrightLine) {
			t.Errorf("%s license block lacks %q", notice.name, copyrightLine)
		}
		if !strings.Contains(block, permissionClause) {
			t.Errorf("%s license block lacks the MIT permission clause", notice.name)
		}
	}
	if rootBlock, ok := licenseBlock(root); ok {
		if payloadBlock, ok := licenseBlock(payload); ok && rootBlock != payloadBlock {
			t.Error("the root and payload license blocks differ; one copy was edited alone")
		}
	}

	files, err := skillfs.Paths()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := listedPrompts(payload), promptFiles(files); !slices.Equal(got, want) {
		t.Fatalf("prompts listed in %s = %v, want every shipped prompt %v; a new adapted file needs a notice entry",
			path.Join(skillfs.Dir, noticePayload), got, want)
	}
	for _, name := range topLevel(files) {
		if !strings.Contains(payload, "`"+name+"`") && !strings.Contains(payload, "`"+name+"/`") {
			t.Errorf("%s does not account for shipped path %q; list it as adapted or first-party",
				path.Join(skillfs.Dir, noticePayload), name)
		}
	}
}

// licenseBlock returns the text between the upstream-license markers, which
// delimit the verbatim MIT notice both copies reproduce.
func licenseBlock(notice string) (string, bool) {
	_, after, ok := strings.Cut(notice, licenseStart+"\n")
	if !ok {
		return "", false
	}
	block, _, ok := strings.Cut(after, licenseEnd)
	return block, ok
}

// listedPrompts collects the backticked "prompts/*.md" entries the payload
// notice lists as adapted, in the order it lists them.
func listedPrompts(notice string) []string {
	var listed []string
	for _, line := range strings.Split(notice, "\n") {
		entry, ok := strings.CutPrefix(strings.TrimSpace(line), "- `")
		if !ok {
			continue
		}
		entry, ok = strings.CutSuffix(entry, "`")
		if !ok || !strings.HasPrefix(entry, "prompts/") || !strings.HasSuffix(entry, ".md") {
			continue
		}
		listed = append(listed, entry)
	}
	slices.Sort(listed)
	return listed
}

// promptFiles is every shipped file under prompts/, the adapted set.
func promptFiles(files []string) []string {
	var prompts []string
	for _, file := range files {
		if strings.HasPrefix(file, "prompts/") {
			prompts = append(prompts, file)
		}
	}
	slices.Sort(prompts)
	return prompts
}

// topLevel is the distinct first path segment of every shipped file, so a new
// artifact cannot arrive without a notice line.
func topLevel(files []string) []string {
	var names []string
	for _, file := range files {
		name, _, _ := strings.Cut(file, "/")
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}
