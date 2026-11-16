package archtest

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

var selfPinSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

var actionSelfPins = []struct {
	path   string
	prefix string
}{
	{path: "action.yml", prefix: "stainless-code/verdict/setup-verdict@"},
	{path: "examples/verdict-review.yml", prefix: "stainless-code/verdict@"},
}

// TestActionPinsAtRelease keeps the composite Action and its copy-paste
// workflow on the release that package.json names, with no re-pin TODO left in
// the line.
func TestActionPinsAtRelease(t *testing.T) {
	root := moduleRoot(t)
	version := actionPackageVersion(t, root)
	wantComment := "# v" + version
	var shas []string
	for _, target := range actionSelfPins {
		src := readRepoFile(t, root, target.path)
		sha, problems := pinProblems(src, target.prefix, wantComment)
		for _, problem := range problems {
			t.Errorf("%s: %s", target.path, problem)
		}
		shas = append(shas, sha)
	}
	if len(shas) == 2 && shas[0] != "" && shas[1] != "" && shas[0] != shas[1] {
		t.Errorf("self-pins disagree: action.yml is %s, examples/verdict-review.yml is %s", shas[0], shas[1])
	}
	t.Run("release tag points at the pin", func(t *testing.T) {
		tags := tagsAt(t, root, shas[0])
		if len(tags) == 0 {
			t.Skip("no tags in this checkout: the SHA-to-version check needs a tagged clone")
		}
		if !slices.Contains(tags, "v"+version) {
			t.Errorf("self-pin %s has tags %v; want tag v%s at that commit", shas[0], tags, version)
		}
	})
}

func actionPackageVersion(t *testing.T, root string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		t.Fatalf("read package.json: %v", err)
	}
	var pkg struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(raw, &pkg); err != nil {
		t.Fatalf("decode package.json: %v", err)
	}
	if pkg.Version == "" {
		t.Fatal("package.json has no version: the pin guard would pass on nothing")
	}
	return pkg.Version
}

// A shallow CI checkout has no tags; the line checks still run there.
func tagsAt(t *testing.T, root, sha string) []string {
	t.Helper()
	if !selfPinSHA.MatchString(sha) {
		return nil
	}
	if _, err := exec.LookPath("git"); err != nil {
		return nil
	}
	cmd := exec.CommandContext(t.Context(), "git", "-C", root, "tag", "--points-at", sha)
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	return strings.Fields(string(out))
}

func pinProblems(src, prefix, wantComment string) (string, []string) {
	var problems []string
	for _, line := range strings.Split(src, "\n") {
		start := strings.Index(line, prefix)
		if start < 0 {
			continue
		}
		tail := strings.Fields(strings.TrimPrefix(line[start:], prefix))
		sha := ""
		if len(tail) > 0 {
			sha = tail[0]
		}
		if !selfPinSHA.MatchString(sha) {
			problems = append(problems, fmt.Sprintf("self-pin %q is not a full 40-hex commit SHA", sha))
		}
		gotComment := strings.Join(tail[min(len(tail), 1):], " ")
		if gotComment != wantComment {
			problems = append(problems, fmt.Sprintf("self-pin comment %q, want %q", gotComment, wantComment))
		}
		for _, marker := range []string{"re-pin", "repin", "as introduced", "todo"} {
			if strings.Contains(strings.ToLower(line), marker) {
				problems = append(problems, "self-pin still carries an open re-pin TODO")
				break
			}
		}
		return sha, problems
	}
	return "", []string{"no " + prefix + " self-pin found"}
}

func TestPinProblems(t *testing.T) {
	t.Parallel()
	const prefix = "stainless-code/verdict/setup-verdict@"
	const release = "b4c36344f03375985a8edf859b9092ba7d59f077"
	const introduced = "471d2985b29060caa90036fefac565e97a12bb2c"
	tests := []struct {
		name string
		line string
		want []string
	}{
		{
			name: "release pin",
			line: "      uses: " + prefix + release + " # v0.7.0",
		},
		{
			name: "introduction pin with an open TODO",
			line: "      uses: " + prefix + introduced + " # action as introduced; re-pin to a release commit",
			want: []string{"open re-pin TODO", "comment"},
		},
		{
			name: "release SHA with a stale version comment",
			line: "      uses: " + prefix + release + " # v0.6.1",
			want: []string{"comment"},
		},
		{
			name: "moving tag",
			line: "      uses: " + prefix + "latest # v0.7.0",
			want: []string{"40-hex"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, problems := pinProblems(tc.line, prefix, "# v0.7.0")
			for _, expected := range tc.want {
				if !slices.ContainsFunc(problems, func(problem string) bool {
					return strings.Contains(problem, expected)
				}) {
					t.Errorf("pinProblems() = %v; want a problem containing %q", problems, expected)
				}
			}
			if len(tc.want) == 0 && len(problems) != 0 {
				t.Errorf("pinProblems() = %v; want no problems", problems)
			}
		})
	}
}
