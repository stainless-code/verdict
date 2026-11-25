package scaffold

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/cli"
)

// fakeLookPath answers the installed-binary set a test declares.
func fakeLookPath(installed ...string) func(string) (string, error) {
	return func(bin string) (string, error) {
		if slices.Contains(installed, bin) {
			return "/usr/local/bin/" + bin, nil
		}
		return "", fmt.Errorf("%s: not found", bin)
	}
}

// remoteOf answers one fixed origin URL.
func remoteOf(url string) func(context.Context) (string, error) {
	return func(context.Context) (string, error) { return url, nil }
}

// scriptedPicker is the seam every picker test installs: scripted answers, a
// declared host and PATH, and both terminal flags named by the case.
func scriptedPicker(answers, remote string, installed []string, stdinTTY, stdoutTTY bool) (*Picker, *bytes.Buffer) {
	out := &bytes.Buffer{}
	return &Picker{
		In:        strings.NewReader(answers),
		Out:       out,
		StdinTTY:  stdinTTY,
		StdoutTTY: stdoutTTY,
		LookPath:  fakeLookPath(installed...),
		Remote:    remoteOf(remote),
	}, out
}

// testRunner is the CLI state `verdict init` runs with: every stream on one
// buffer, and a clock that cannot move a golden.
func testRunner() (*cli.Runner, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	return &cli.Runner{Out: buf, Human: buf, Diag: buf, Now: func() time.Time { return time.Unix(0, 0).UTC() }}, buf
}

// pickScript is one scripted picker session: the answers, the terminal and
// probe answers, and the flags and prompts the session must produce.
type pickScript struct {
	name      string
	answers   string
	remote    string
	installed []string
	want      flags
	prompts   []string
}

// TestPickFlagsResolvesTheScriptedAnswers pins the picker's descent as literal
// flags: Enter keeps each default, the detected host proposes the workflow, an
// explicit answer wins over it, and the comment trigger is only asked of a
// GitHub run.
func TestPickFlagsResolvesTheScriptedAnswers(t *testing.T) {
	t.Parallel()
	const githubRemote = "git@github.com:stainless-code/verdict.git"
	for _, tc := range []pickScript{
		{
			name:      "Enter keeps every default",
			answers:   "\n\n\n\n",
			remote:    githubRemote,
			installed: []string{"claude"},
			want:      flags{preset: "standard", ci: "github", agent: "claude"},
			prompts: []string{
				"preset (chill|standard|strict) [standard]: ",
				"ci workflow (none|github|forgejo|gitlab) [github]: ",
				"/verdict comment re-run? (y|n) [n]: ",
				"agent (claude) [claude]: ",
			},
		},
		{
			name:      "every answer explicit",
			answers:   "strict\nforgejo\ncodex\n",
			remote:    githubRemote,
			installed: []string{"codex"},
			want:      flags{preset: "strict", ci: "forgejo", agent: "codex"},
			prompts:   []string{"ci workflow (none|github|forgejo|gitlab) [github]: "},
		},
		{
			name:      "the comment trigger opted in",
			answers:   "chill\ngithub\ny\nclaude\n",
			remote:    githubRemote,
			installed: []string{"claude"},
			want:      flags{preset: "chill", ci: "github", agent: "claude", commentTrigger: true},
		},
		{
			name:    "no workflow leaves the agent default alone",
			answers: "standard\nnone\n",
			remote:  "git@example.com:o/r.git",
			want:    flags{preset: "standard"},
			prompts: []string{"ci workflow (none|github|forgejo|gitlab) [none]: "},
		},
		{
			name:      "a gitlab host proposes gitlab",
			answers:   "\n\n\n",
			remote:    "https://gitlab.com/group/project.git",
			installed: []string{"claude"},
			want:      flags{preset: "standard", ci: "gitlab", agent: "claude"},
			prompts:   []string{"ci workflow (none|github|forgejo|gitlab) [gitlab]: "},
		},
		{
			name:    "nothing installed keeps the built-in agent default",
			answers: "\n\n\n",
			remote:  githubRemote,
			want:    flags{preset: "standard", ci: "github", agent: "claude"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkPickedFlags(t, &tc)
		})
	}
}

// checkPickedFlags grades one scripted session: the resolved flags and the
// prompts the operator sees.
func checkPickedFlags(t *testing.T, tc *pickScript) {
	t.Helper()
	picker, prompts := scriptedPicker(tc.answers, tc.remote, tc.installed, true, true)
	got, err := pickFlags(t.Context(), picker)
	if err != nil {
		t.Fatalf("pickFlags aborted with answers %q: %v", tc.answers, err)
	}
	if got != tc.want {
		t.Fatalf("pickFlags = %+v, want %+v", got, tc.want)
	}
	for _, want := range tc.prompts {
		if !strings.Contains(prompts.String(), want) {
			t.Fatalf("prompts %q carry no %q", prompts.String(), want)
		}
	}
}

// TestPickFlagsReasksAnAnswerItCannotUse proves an unlisted answer is not
// silently dropped: the picker asks again and grades the second answer.
func TestPickFlagsReasksAnAnswerItCannotUse(t *testing.T) {
	t.Parallel()
	picker, prompts := scriptedPicker("chill\nyaml\nnone\n", "", nil, true, true)
	got, err := pickFlags(t.Context(), picker)
	if err != nil {
		t.Fatalf("pickFlags aborted instead of re-asking: %v", err)
	}
	if got.preset != "chill" || got.ci != "" {
		t.Fatalf("pickFlags = %+v, want preset chill and no workflow", got)
	}
	if !strings.Contains(prompts.String(), "choose one of none|github|forgejo|gitlab, or press Enter for none") {
		t.Fatalf("prompts %q do not re-ask the workflow", prompts.String())
	}
}

// TestPickFlagsAbortsAtEndOfInput proves a closed input aborts instead of
// writing half a plan: every prefix of the answers ends with ok=false.
func TestPickFlagsAbortsAtEndOfInput(t *testing.T) {
	t.Parallel()
	for _, answers := range []string{"", "standard\n", "standard\ngithub\n", "standard\ngithub\ny\n"} {
		picker, _ := scriptedPicker(answers, "git@github.com:o/r.git", []string{"claude"}, true, true)
		if got, err := pickFlags(t.Context(), picker); !errors.Is(err, errPickAborted) {
			t.Fatalf("pickFlags(%q) = %+v, %v; want errPickAborted", answers, got, err)
		}
	}
}

// pickCase is one scripted session beside the argv that means the same thing
// explicitly: the picker must produce the same files, not a similar plan.
type pickCase struct {
	name      string
	answers   string
	remote    string
	installed []string
	args      []string
}

// TestPickedRunWritesTheExplicitBytes is the equivalence contract: a picked run
// writes byte-identical files to the invocation that spells the same answers as
// flags — the workflow, its comment trigger, and the agent it launches.
func TestPickedRunWritesTheExplicitBytes(t *testing.T) {
	t.Parallel()
	for _, tc := range []pickCase{
		{
			name:      "Enter keeps every default",
			answers:   "\n\n\n\n",
			remote:    "git@github.com:stainless-code/verdict.git",
			installed: []string{"claude"},
			args:      []string{"--preset", "standard", "--ci", "github", "--agent", "claude"},
		},
		{
			name:      "strict on gitlab with the installed codex",
			answers:   "strict\ngitlab\ncodex\n",
			remote:    "https://gitlab.com/group/project.git",
			installed: []string{"codex"},
			args:      []string{"--preset", "strict", "--ci", "gitlab", "--agent", "codex"},
		},
		{
			name:      "the comment trigger opted in",
			answers:   "standard\ngithub\ny\nclaude\n",
			remote:    "git@github.com:stainless-code/verdict.git",
			installed: []string{"claude"},
			args:      []string{"--preset", "standard", "--ci", "github", "--agent", "claude", "--comment-trigger"},
		},
		{
			name:    "no workflow at all",
			answers: "chill\nnone\n",
			remote:  "git@example.com:o/r.git",
			args:    []string{"--preset", "chill"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			picked := runPickedInit(t, &tc)
			explicit := runExplicitInit(t, tc.args)
			if got, want := readTree(t, picked), readTree(t, explicit); !mapsEqual(got, want) {
				t.Fatalf("picked run wrote %s, explicit run wrote %s", keys(got), keys(want))
			}
		})
	}
}

// runPickedInit runs the shipped adapter with the scripted picker and asserts
// the run succeeded and prompted.
func runPickedInit(t *testing.T, tc *pickCase) string {
	t.Helper()
	dir := t.TempDir()
	picker, prompts := scriptedPicker(tc.answers, tc.remote, tc.installed, true, true)
	r, _ := testRunner()
	if code := Run(t.Context(), r, nil, Request{RepoRoot: dir, Picker: picker}); code != cli.ExitOK {
		t.Fatalf("picked init exit = %d\n%s", code, prompts)
	}
	if prompts.Len() == 0 {
		t.Fatal("the picked run asked nothing")
	}
	return dir
}

// runExplicitInit runs the same adapter with the answers spelled as flags.
func runExplicitInit(t *testing.T, args []string) string {
	t.Helper()
	dir := t.TempDir()
	r, _ := testRunner()
	if code := Run(t.Context(), r, args, Request{RepoRoot: dir}); code != cli.ExitOK {
		t.Fatalf("explicit init %q exit = %d", args, code)
	}
	return dir
}

// readTree answers every file below dir, keyed by its slash-normalized path.
func readTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	tree := map[string]string{}
	if err := filepath.WalkDir(dir, collectFile(dir, tree)); err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	return tree
}

// collectFile records one walked file under its path relative to dir.
func collectFile(dir string, tree map[string]string) fs.WalkDirFunc {
	return func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		tree[filepath.ToSlash(rel)] = string(body)
		return nil
	}
}

// mapsEqual compares two file trees by key and byte body.
func mapsEqual(got, want map[string]string) bool {
	if len(got) != len(want) {
		return false
	}
	for key, body := range want {
		if got[key] != body {
			return false
		}
	}
	return true
}

// keys answers one tree's paths, for the mismatch report.
func keys(tree map[string]string) []string {
	paths := make([]string, 0, len(tree))
	for path := range tree {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	return paths
}

// gateCase is one invocation that must never reach the picker: the argv, the
// two terminal answers, and the preset the non-interactive path writes.
type gateCase struct {
	name      string
	preset    string
	args      []string
	json      bool
	stdinTTY  bool
	stdoutTTY bool
}

// TestPickerStaysOutOfEveryNonInteractiveRun pins the gate: a named selection
// flag, --json, or either stream away from a terminal never reads stdin. The
// picker's input answers EOF immediately, so a run that asked would abort with
// exit 1 and write nothing — the cases assert exit 0 and the defaults instead.
func TestPickerStaysOutOfEveryNonInteractiveRun(t *testing.T) {
	t.Parallel()
	for _, tc := range []gateCase{
		{name: "stdin is a pipe", preset: "standard", stdoutTTY: true},
		{name: "stdout is a file", preset: "standard", stdinTTY: true},
		{name: "--json", preset: "standard", json: true, stdinTTY: true, stdoutTTY: true},
		{name: "--yes", args: []string{flagYes}, preset: "standard", stdinTTY: true, stdoutTTY: true},
		{
			name: "--preset chill", args: []string{flagPreset, "chill"},
			preset: "chill", stdinTTY: true, stdoutTTY: true,
		},
		{
			name: "--ci github", args: []string{flagCI, "github"},
			preset: "standard", stdinTTY: true, stdoutTTY: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkPickerStaysOut(t, &tc)
		})
	}
}

// checkPickerStaysOut runs one gate case and proves it neither prompted nor
// changed which preset landed.
func checkPickerStaysOut(t *testing.T, tc *gateCase) {
	t.Helper()
	dir := t.TempDir()
	picker, prompts := scriptedPicker("", "", nil, tc.stdinTTY, tc.stdoutTTY)
	r, _ := testRunner()
	r.JSONMode = tc.json
	if code := Run(t.Context(), r, tc.args, Request{RepoRoot: dir, Picker: picker}); code != cli.ExitOK {
		t.Fatalf("exit = %d, want 0 (the picker read stdin)", code)
	}
	if prompts.Len() != 0 {
		t.Fatalf("the run prompted: %q", prompts.String())
	}
	assertPresetWritten(t, dir, tc.preset)
}

// assertPresetWritten proves the standard/chill budget landed, whichever the
// case expects: the non-interactive path is the flag path.
func assertPresetWritten(t *testing.T, dir, preset string) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(dir, ".verdict", "config.json"))
	if err != nil {
		t.Fatalf("no config written: %v", err)
	}
	want := map[string]string{"standard": `"budget_usd": 0.50`, "chill": `"budget_usd": 0.10`}[preset]
	if !strings.Contains(string(body), want) {
		t.Fatalf("config = %q, want %s", body, want)
	}
}

// promptWait bounds one prompt wait: a healthy picker asks immediately, and a
// picker that ignores the context must fail the test, not hang it.
const promptWait = 5 * time.Second

// promptSink is a writer that publishes what the picker asks for, so a test can
// wait for a prompt without racing the session's own goroutine.
type promptSink chan string

// Write publishes one prompt chunk, never blocking: the buffer is sized for a
// whole session's prompts.
func (s promptSink) Write(p []byte) (int, error) {
	select {
	case s <- string(p):
	default:
	}
	return len(p), nil
}

// waitPrompt waits for one prompt carrying want, bounded so a picker that never
// asks fails the test instead of hanging it.
func waitPrompt(t *testing.T, prompts promptSink, want string) {
	t.Helper()
	deadline := time.After(promptWait)
	for {
		select {
		case got := <-prompts:
			if strings.Contains(got, want) {
				return
			}
		case <-deadline:
			t.Fatalf("no %q prompt appeared", want)
		}
	}
}

// TestPickFlagsCancelUnblocksAPendingPrompt proves a canceled context ends a
// prompt that is already waiting for input: the process-wide SIGINT context has
// no other way to stop a blocking terminal read.
func TestPickFlagsCancelUnblocksAPendingPrompt(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	pr, pw := io.Pipe()
	t.Cleanup(func() { pw.Close() })
	prompts := make(promptSink, 16)
	picker := &Picker{
		In: pr, Out: prompts, StdinTTY: true, StdoutTTY: true,
		LookPath: fakeLookPath(), Remote: remoteOf(""),
	}
	done := make(chan error, 1)
	go func() {
		_, err := pickFlags(ctx, picker)
		done <- err
	}()
	waitPrompt(t, prompts, "preset (")
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("pickFlags = %v, want context.Canceled", err)
		}
	case <-time.After(promptWait):
		// Release the piped read so a leftover reader cannot outlive the test,
		// then fail: the prompt ignored the context.
		pw.Close()
		t.Fatal("a canceled context did not unblock the prompt")
	}
}

// TestRunCancelInsideAPromptExitsCanceled proves the whole contract: Ctrl-C
// while a question waits exits 3 through the frozen cancel refusal and writes
// nothing, instead of waiting for a newline that never comes.
func TestRunCancelInsideAPromptExitsCanceled(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	pr, pw := io.Pipe()
	t.Cleanup(func() { pw.Close() })
	prompts := make(promptSink, 16)
	picker := &Picker{
		In: pr, Out: prompts, StdinTTY: true, StdoutTTY: true,
		LookPath: fakeLookPath("claude"), Remote: remoteOf("git@github.com:o/r.git"),
	}
	r, out := testRunner()
	done := make(chan int, 1)
	go func() { done <- Run(ctx, r, nil, Request{RepoRoot: dir, Picker: picker}) }()
	waitPrompt(t, prompts, "preset (")
	cancel()
	select {
	case code := <-done:
		if code != cli.ExitBroke {
			t.Fatalf("exit = %d, want %d (canceled)", code, cli.ExitBroke)
		}
	case <-time.After(promptWait):
		pw.Close()
		t.Fatal("a canceled context did not end the run")
	}
	if _, err := os.Lstat(filepath.Join(dir, ".verdict")); !os.IsNotExist(err) {
		t.Fatalf("the canceled run created .verdict (err=%v)", err)
	}
	if !strings.Contains(out.String(), "error: canceled before init completed") {
		t.Fatalf("output = %q, want the frozen cancel line", out.String())
	}
	if strings.Contains(out.String(), "aborted") {
		t.Fatalf("a cancellation reported the Ctrl-D abort instead: %q", out.String())
	}
}

// TestPickerAbortWritesNothing proves Ctrl-D before the last answer exits 1 and
// leaves no .verdict/ behind: the picker refuses before the service is called.
func TestPickerAbortWritesNothing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	picker, _ := scriptedPicker("standard\n", "git@github.com:o/r.git", nil, true, true)
	r, diag := testRunner()
	if code := Run(t.Context(), r, nil, Request{RepoRoot: dir, Picker: picker}); code != cli.ExitRefused {
		t.Fatalf("exit = %d, want %d", code, cli.ExitRefused)
	}
	if _, err := os.Lstat(filepath.Join(dir, ".verdict")); !os.IsNotExist(err) {
		t.Fatalf("the aborted run created .verdict (err=%v)", err)
	}
	if want := "init: aborted (nothing written)\n"; diag.String() != want {
		t.Fatalf("diag = %q, want %q", diag.String(), want)
	}
}

// TestHostFromRemote pins the detection table as literals: both git URL forms,
// the three hosts verdict can write for, a non-forgejo host, and no remote.
func TestHostFromRemote(t *testing.T) {
	t.Parallel()
	for remote, want := range map[string]string{
		"git@github.com:stainless-code/verdict.git":     "github",
		"https://github.com/stainless-code/verdict.git": "github",
		"ssh://git@github.com/o/r.git":                  "github",
		"git@gitlab.com:group/project.git":              "gitlab",
		"https://gitlab.example.com:8443/o/r.git":       "gitlab",
		"https://codeberg.org/o/r.git":                  "forgejo",
		"git@code.forgejo.org:o/r.git":                  "forgejo",
		"git@bitbucket.org:o/r.git":                     "",
		"":                                              "",
		// A host is case-insensitive, and operators paste it capitalized.
		"https://GitHub.com/o/r.git": "github",
		"git@GitHub.com:o/r.git":     "github",
		// A path may hold an "@": cutting it before the path would read the
		// host out of the path and misdetect the forge.
		"https://example.com/a@github.com/b": "",
		"https://user@github.com/o/r.git":    "github",
	} {
		if got := hostFromRemote(remote); got != want {
			t.Errorf("hostFromRemote(%q) = %q, want %q", remote, got, want)
		}
	}
}

// TestInstalledAgentsKeepCatalogOrder proves the picker's menu is the catalog's
// own order filtered by PATH, so the first entry — the default — is stable.
func TestInstalledAgentsKeepCatalogOrder(t *testing.T) {
	t.Parallel()
	got := installedAgents(fakeLookPath("codex", "claude"))
	if want := []string{"claude", "codex"}; !slices.Equal(got, want) {
		t.Fatalf("installedAgents = %q, want %q", got, want)
	}
	// agy answers on PATH, but its row documents no key variable: offering it
	// would render a workflow the service refuses for the missing secret.
	if got, want := installedAgents(fakeLookPath("agy", "claude")), []string{"claude"}; !slices.Equal(got, want) {
		t.Fatalf("installedAgents = %q, want %q", got, want)
	}
	if got := installedAgents(fakeLookPath("nonesuch")); len(got) != 0 {
		t.Fatalf("installedAgents = %q, want none", got)
	}
	if got := installedAgents(nil); got != nil {
		t.Fatalf("installedAgents(nil) = %q, want nil", got)
	}
}

// TestAskerTrimsWindowsLineEndings proves a CRLF answer is the answer, not an
// invalid value: the picker runs on Windows too.
func TestAskerTrimsWindowsLineEndings(t *testing.T) {
	t.Parallel()
	out := &bytes.Buffer{}
	ask := &asker{in: bufio.NewReader(strings.NewReader("chill\r\n")), out: out}
	got, err := ask.value(t.Context(), "preset", "standard", []string{"chill", "standard", "strict"})
	if err != nil || got != "chill" {
		t.Fatalf("value = %q, %v, want chill", got, err)
	}
}
