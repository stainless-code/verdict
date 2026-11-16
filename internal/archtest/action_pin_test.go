package archtest

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

var (
	selfPinSHA        = regexp.MustCompile(`^[0-9a-f]{40}$`)
	semverPattern     = regexp.MustCompile(`^([0-9]+)\.([0-9]+)\.([0-9]+)$`)
	pinCommentPattern = regexp.MustCompile(`^# v([0-9]+\.[0-9]+\.[0-9]+)$`)
)

type selfPinTarget struct {
	path   string
	prefix string
}

type selfPin struct {
	sha     string
	version string
}

var actionSelfPins = []selfPinTarget{
	{path: "action.yml", prefix: "stainless-code/verdict/setup-verdict@"},
	{path: "examples/verdict-review.yml", prefix: "stainless-code/verdict@"},
}

// TestActionPinsAtRelease keeps both self-pins on the same tagged release at
// or before package.json: a Version PR bumps the manifest before the matching
// release commit exists, so equality would deadlock the next release.
func TestActionPinsAtRelease(t *testing.T) {
	root := moduleRoot(t)
	version := actionPackageVersion(t, root)
	var pins []selfPin
	for _, target := range actionSelfPins {
		pins = append(pins, checkActionPin(t, root, target, version))
	}
	checkReleaseTag(t, root, pins)
}

func checkActionPin(t *testing.T, root string, target selfPinTarget, packageVersion string) selfPin {
	t.Helper()
	src := readRepoFile(t, root, target.path)
	pin, problems := pinProblems(src, target.prefix, packageVersion)
	for _, problem := range problems {
		t.Errorf("%s: %s", target.path, problem)
	}
	return pin
}

func checkReleaseTag(t *testing.T, root string, pins []selfPin) {
	t.Helper()
	if len(pins) != 2 {
		t.Fatalf("found %d self-pins; want 2", len(pins))
	}
	if pins[0].sha == "" || pins[1].sha == "" {
		return
	}
	if pins[0] != pins[1] {
		t.Errorf("self-pins disagree: action.yml is %s v%s, examples/verdict-review.yml is %s v%s",
			pins[0].sha, pins[0].version, pins[1].sha, pins[1].version)
	}
	t.Run("release tag points at the pin", func(t *testing.T) {
		checkTagAtPin(t, root, pins[0].sha, pins[0].version)
	})
}

func checkTagAtPin(t *testing.T, root, sha, pinVersion string) {
	t.Helper()
	tags := tagsAt(t, root, sha)
	if len(tags) == 0 {
		t.Skip("no tags in this checkout: the SHA-to-version check needs a tagged clone")
	}
	if !slices.Contains(tags, "v"+pinVersion) {
		t.Errorf("self-pin %s has tags %v; want tag v%s at that commit", sha, tags, pinVersion)
	}
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
	if !semverValid(pkg.Version) {
		t.Fatalf("package.json version %q is not X.Y.Z", pkg.Version)
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

func pinProblems(src, prefix, packageVersion string) (selfPin, []string) {
	for line := range strings.SplitSeq(src, "\n") {
		if strings.Contains(line, prefix) {
			return pinLineProblems(line, prefix, packageVersion)
		}
	}
	return selfPin{}, []string{"no " + prefix + " self-pin found"}
}

func pinLineProblems(line, prefix, packageVersion string) (selfPin, []string) {
	start := strings.Index(line, prefix)
	tail := strings.Fields(line[start+len(prefix):])
	var pin selfPin
	if len(tail) > 0 {
		pin.sha = tail[0]
	}
	var problems []string
	if !selfPinSHA.MatchString(pin.sha) {
		problems = append(problems, fmt.Sprintf("self-pin %q is not a full 40-hex commit SHA", pin.sha))
	}
	comment := strings.Join(tail[min(len(tail), 1):], " ")
	pin.version = pinCommentVersion(comment)
	if pin.version == "" {
		problems = append(problems, fmt.Sprintf("self-pin comment %q, want `# vX.Y.Z`", comment))
	}
	problems = append(problems, versionProblems(pin.version, packageVersion)...)
	if hasRepinMarker(line) {
		problems = append(problems, "self-pin still carries an open re-pin TODO")
	}
	return pin, problems
}

func pinCommentVersion(comment string) string {
	match := pinCommentPattern.FindStringSubmatch(comment)
	if match == nil {
		return ""
	}
	return match[1]
}

func versionProblems(pinVersion, packageVersion string) []string {
	if !semverValid(packageVersion) {
		return []string{fmt.Sprintf("package.json version %q is not X.Y.Z", packageVersion)}
	}
	if pinVersion == "" {
		return nil
	}
	if !semverValid(pinVersion) {
		return []string{fmt.Sprintf("self-pin version %q is not X.Y.Z", pinVersion)}
	}
	if semverGreater(pinVersion, packageVersion) {
		return []string{fmt.Sprintf("self-pin v%s is newer than package.json version %s", pinVersion, packageVersion)}
	}
	return nil
}

func semverValid(version string) bool {
	_, ok := semverParts(version)
	return ok
}

func semverGreater(left, right string) bool {
	a, aOK := semverParts(left)
	b, bOK := semverParts(right)
	if !aOK || !bOK {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}

func semverParts(version string) ([3]int, bool) {
	var parts [3]int
	match := semverPattern.FindStringSubmatch(version)
	if match == nil {
		return parts, false
	}
	for i := range parts {
		if len(match[i+1]) > 1 && match[i+1][0] == '0' {
			return parts, false
		}
		number, err := strconv.Atoi(match[i+1])
		if err != nil {
			return parts, false
		}
		parts[i] = number
	}
	return parts, true
}

func hasRepinMarker(line string) bool {
	lower := strings.ToLower(line)
	markers := []string{"re-pin", "repin", "as introduced", "todo"}
	return slices.ContainsFunc(markers, func(marker string) bool {
		return strings.Contains(lower, marker)
	})
}

type pinCase struct {
	name       string
	line       string
	pkgVersion string
	want       []string
}

func TestPinProblems(t *testing.T) {
	t.Parallel()
	const prefix = "stainless-code/verdict/setup-verdict@"
	const release = "b4c36344f03375985a8edf859b9092ba7d59f077"
	const introduced = "471d2985b29060caa90036fefac565e97a12bb2c"
	tests := []pinCase{
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
			name: "older pinned release stays valid after a version bump",
			line: "      uses: " + prefix + release + " # v0.6.1",
		},
		{
			name:       "version PR: pin behind package.json",
			line:       "      uses: " + prefix + release + " # v0.7.0",
			pkgVersion: "0.7.1",
		},
		{
			name:       "pin newer than package.json",
			line:       "      uses: " + prefix + release + " # v0.7.2",
			pkgVersion: "0.7.1",
			want:       []string{"newer than package.json version"},
		},
		{
			name: "moving tag",
			line: "      uses: " + prefix + "latest # v0.7.0",
			want: []string{"40-hex"},
		},
		{
			name: "malformed version comment",
			line: "      uses: " + prefix + release + " # v0.7",
			want: []string{"comment"},
		},
		{
			name: "leading zero in pin version",
			line: "      uses: " + prefix + release + " # v0.07.0",
			want: []string{"not X.Y.Z"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkPinCase(t, tc, prefix)
		})
	}
}

func checkPinCase(t *testing.T, tc pinCase, prefix string) {
	t.Helper()
	pkgVersion := tc.pkgVersion
	if pkgVersion == "" {
		pkgVersion = "0.7.0"
	}
	_, problems := pinProblems(tc.line, prefix, pkgVersion)
	assertPinProblems(t, problems, tc.want)
}

func assertPinProblems(t *testing.T, problems, want []string) {
	t.Helper()
	if len(want) == 0 && len(problems) != 0 {
		t.Fatalf("pinProblems() = %v; want no problems", problems)
	}
	for _, expected := range want {
		if !slices.ContainsFunc(problems, func(problem string) bool {
			return strings.Contains(problem, expected)
		}) {
			t.Errorf("pinProblems() = %v; want a problem containing %q", problems, expected)
		}
	}
}

func TestSemverGreater(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		left, right string
		want        bool
	}{
		{name: "patch", left: "0.7.1", right: "0.7.0", want: true},
		{name: "older patch", left: "0.7.0", right: "0.7.1"},
		{name: "equal", left: "0.7.0", right: "0.7.0"},
		{name: "minor is numeric", left: "0.10.0", right: "0.9.9", want: true},
		{name: "major wins", left: "1.0.0", right: "0.99.99", want: true},
		{name: "leading zero is not semver", left: "0.07.0", right: "0.7.0"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := semverGreater(tc.left, tc.right); got != tc.want {
				t.Errorf("semverGreater(%q, %q) = %v; want %v", tc.left, tc.right, got, tc.want)
			}
		})
	}
}
