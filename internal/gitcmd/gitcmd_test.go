package gitcmd_test

import (
	"os"
	"slices"
	"testing"

	"github.com/stainless-code/verdict/internal/gitcmd"
)

// hooksOff is the hooks-path setting every argv carries.
var hooksOff = "core.hooksPath=" + os.DevNull

// TestArgvIsHookFree pins the argv shapes: the repository named with -C, the
// caller's own global options kept ahead of the safety settings so the
// settings win, a diff, log, or show running no external diff or textconv
// driver, blame no textconv, and --version still read as the command.
func TestArgvIsHookFree(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		root string
		args []string
		want []string
	}{
		{
			name: "status", root: "/repo", args: []string{"status", "--porcelain"},
			want: []string{"git", "-C", "/repo", "-c", hooksOff, "-c", "core.fsmonitor=false", "status", "--porcelain"},
		},
		{
			name: "diff", root: "", args: []string{"diff", "HEAD"},
			want: []string{
				"git", "-C", ".", "-c", hooksOff, "-c", "core.fsmonitor=false",
				"diff", "--no-ext-diff", "--no-textconv", "HEAD",
			},
		},
		{
			name: "caller options", root: "/repo",
			args: []string{"--literal-pathspecs", "-c", "core.hooksPath=.husky", "show", "-s", "HEAD"},
			want: []string{
				"git", "-C", "/repo", "--literal-pathspecs", "-c", "core.hooksPath=.husky",
				"-c", hooksOff, "-c", "core.fsmonitor=false",
				"show", "--no-ext-diff", "--no-textconv", "-s", "HEAD",
			},
		},
		{
			name: "blame", root: "/repo", args: []string{"blame", "--porcelain", "a.go"},
			want: []string{
				"git", "-C", "/repo", "-c", hooksOff, "-c", "core.fsmonitor=false",
				"blame", "--no-textconv", "--porcelain", "a.go",
			},
		},
		{
			name: "version", root: "/repo", args: []string{"--version"},
			want: []string{"git", "-C", "/repo", "-c", hooksOff, "-c", "core.fsmonitor=false", "--version"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkArgv(t, tc.root, tc.args, tc.want)
		})
	}
}

// checkArgv proves Argv builds want and Split gives back the root (the
// working directory for an empty one) and the caller's args.
func checkArgv(t *testing.T, root string, args, want []string) {
	t.Helper()
	got := gitcmd.Argv(root, args...)
	if !slices.Equal(got, want) {
		t.Fatalf("Argv = %q\nwant %q", got, want)
	}
	if root == "" {
		root = "."
	}
	if gotRoot, gotArgs, ok := gitcmd.Split(got); !ok || gotRoot != root || !slices.Equal(gotArgs, args) {
		t.Errorf("Split = %q %q %t, want %q %q true", gotRoot, gotArgs, ok, root, args)
	}
}

// TestSplitRejectsAnUnsafeArgv pins that only an argv carrying the safety
// settings splits, so a test seam built on Split also catches a git call
// that skipped the helper.
func TestSplitRejectsAnUnsafeArgv(t *testing.T) {
	t.Parallel()
	for _, argv := range [][]string{
		{"git", "status"},
		{"git", "-C", "/repo", "status"},
		{"git", "-C", "/repo", "-c", hooksOff, "status"},
		{"git", "-C", "/repo", "-c", hooksOff, "-c", "core.fsmonitor=false", "diff", "HEAD"},
		{"gh", "-C", "/repo", "-c", hooksOff, "-c", "core.fsmonitor=false", "status"},
	} {
		if root, args, ok := gitcmd.Split(argv); ok {
			t.Errorf("Split(%q) = %q %q, want it refused", argv, root, args)
		}
	}
}

// TestEnvDropsRetargetingVariables pins the environment: the location
// variables, GIT_EXEC_PATH, and GIT_CONFIG_PARAMETERS a parent git exports
// are gone, the operator's transport helper is kept, an existing
// GIT_CONFIG_COUNT entry is kept, and the safety settings follow it.
func TestEnvDropsRetargetingVariables(t *testing.T) {
	t.Parallel()
	base := []string{
		"PATH=/bin", "GIT_INDEX_FILE=/repo/.git/index", "GIT_DIR=/elsewhere", "GIT_WORK_TREE=/elsewhere",
		"GIT_COMMON_DIR=/elsewhere", "GIT_OBJECT_DIRECTORY=/elsewhere", "GIT_ALTERNATE_OBJECT_DIRECTORIES=/elsewhere",
		"GIT_NAMESPACE=ns", "GIT_PREFIX=sub/", "GIT_EXEC_PATH=/elsewhere/libexec",
		"GIT_CONFIG_PARAMETERS='core.hooksPath'='.husky'", "GIT_SSH_COMMAND=ssh -i key",
		"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=color.ui", "GIT_CONFIG_VALUE_0=always",
	}
	want := []string{
		"PATH=/bin", "GIT_SSH_COMMAND=ssh -i key",
		"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=color.ui", "GIT_CONFIG_VALUE_0=always",
		"GIT_CONFIG_KEY_1=core.hooksPath", "GIT_CONFIG_VALUE_1=" + os.DevNull,
		"GIT_CONFIG_KEY_2=core.fsmonitor", "GIT_CONFIG_VALUE_2=false",
		"GIT_CONFIG_COUNT=3",
	}
	if got := gitcmd.Env(base); !slices.Equal(got, want) {
		t.Fatalf("Env = %q\nwant %q", got, want)
	}
}

// TestFilterOptionsSwitchEveryDriverOff pins the checkout options: each
// driver the listing names, a dotted name included, is switched off once.
func TestFilterOptionsSwitchEveryDriverOff(t *testing.T) {
	t.Parallel()
	listing := "filter.lfs.clean\x00filter.lfs.smudge\x00filter.lfs.process\x00filter.lfs.required\x00" +
		"filter.a.b.smudge\x00"
	want := []string{
		"-c", "filter.lfs.clean=", "-c", "filter.lfs.smudge=", "-c", "filter.lfs.process=", "-c", "filter.lfs.required=false",
		"-c", "filter.a.b.clean=", "-c", "filter.a.b.smudge=", "-c", "filter.a.b.process=", "-c", "filter.a.b.required=false",
	}
	if got := gitcmd.FilterOptions(listing); !slices.Equal(got, want) {
		t.Fatalf("FilterOptions = %q\nwant %q", got, want)
	}
	if got := gitcmd.FilterOptions(""); len(got) != 0 {
		t.Errorf("FilterOptions of no drivers = %q, want none", got)
	}
}
