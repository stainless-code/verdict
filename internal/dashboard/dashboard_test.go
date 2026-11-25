package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core/query"
)

func runner(jsonMode bool) (r *cli.Runner, out, diag *bytes.Buffer) {
	out, diag = &bytes.Buffer{}, &bytes.Buffer{}
	r = &cli.Runner{
		Cmd: "dashboard", Out: out, Human: out, Diag: diag, JSONMode: jsonMode,
		Now:       func() time.Time { return time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC) },
		Effective: cli.Defaults(),
	}
	return r, out, diag
}

// TestRunRefusesWithoutATerminal is the GO-CLI-03 contract: the live view on
// a pipe refuses with a typed code and a try: line naming --once, instead of
// waiting for keys that will never come.
func TestRunRefusesWithoutATerminal(t *testing.T) {
	t.Parallel()
	r, out, diag := runner(false)
	if code := Run(t.Context(), r, nil, Request{}); code != cli.ExitRefused {
		t.Fatalf("exit = %d, want %d", code, cli.ExitRefused)
	}
	want := "error: dashboard: stdin and stdout must both be a terminal\n" +
		"try: run verdict dashboard in a terminal, or pass --once for a single plain-text frame\n"
	if diag.String() != want || out.Len() != 0 {
		t.Fatalf("stderr = %q, stdout = %q; want stderr %q and empty stdout", diag.String(), out.String(), want)
	}
}

// TestRunRefusesWithoutATerminalAsEnvelope proves --json only reshapes the
// refusal: one envelope on stdout carrying the code.
func TestRunRefusesWithoutATerminalAsEnvelope(t *testing.T) {
	t.Parallel()
	r, out, _ := runner(true)
	if code := Run(t.Context(), r, []string{"--json"}, Request{}); code != cli.ExitRefused {
		t.Fatalf("exit = %d, want %d", code, cli.ExitRefused)
	}
	var env struct {
		Verb  string `json:"verb"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("stdout is not one envelope: %v\n%s", err, out.String())
	}
	if env.Verb != "dashboard" || env.Error.Code != "dashboard-no-tty" {
		t.Fatalf("envelope verb=%q code=%q, want dashboard / dashboard-no-tty", env.Verb, env.Error.Code)
	}
}

// TestRunOnceDrawsOnePlainFrame runs --once over a --root with no
// repositories: exit 0, the 80×24 Overview on stdout, no escape byte, and the
// discovery cache written only under the (isolated) config home.
func TestRunOnceDrawsOnePlainFrame(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	root := t.TempDir()
	r, out, diag := runner(false)
	if code := Run(t.Context(), r, []string{"--once", "--root", root}, Request{}); code != cli.ExitOK {
		t.Fatalf("exit = %d, want 0; stderr: %s", code, diag.String())
	}
	checkLines(t, out.String(), map[int]string{
		0: " verdict dashboard · 0 roots                                         read-only",
		1: " [1 Overview]  2 Queue",
		4: "   bundles 0   open 0   deferred 0   addressed 0   terminal 0",
		7: "   no repositories found",
	})
	if strings.ContainsRune(out.String(), keyEsc) {
		t.Fatalf("--once frame carries an escape byte:\n%q", out.String())
	}
	if entries, err := os.ReadDir(root); err != nil || len(entries) != 0 {
		t.Fatalf("--root gained %d entries (err %v); the dashboard writes nothing there", len(entries), err)
	}
}

// TestRunOnceDiscoversNestedRepositories proves --root walks below the root
// with serve's depth: a repository two directories down is served. The walk
// depth is not defaulted by the layer, so a zero depth would read the root
// alone and miss it.
func TestRunOnceDiscoversNestedRepositories(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "team", "api", ".verdict", "state"), 0o750); err != nil {
		t.Fatal(err)
	}
	r, out, diag := runner(false)
	args := []string{"--once", "--root", root, "--width", "300"}
	if code := Run(t.Context(), r, args, Request{}); code != cli.ExitOK {
		t.Fatalf("exit = %d, want 0; stderr: %s", code, diag.String())
	}
	lines := strings.Split(out.String(), "\n")
	if want := filepath.Join(root, "team", "api"); !strings.Contains(lines[7], want) || !strings.HasSuffix(lines[7], " 0") {
		t.Fatalf("roots row = %q, want the nested repository %s with 0 bundles\n%s", lines[7], want, out.String())
	}
}

func TestRunFlagRefusals(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "absent")
	cases := []struct {
		name   string
		stderr string
		args   []string
		code   int
		json   bool
	}{
		{
			name: "unknown flag", args: []string{"--watch"}, code: cli.ExitUsage,
			stderr: "unknown flag '--watch' for dashboard (want --root, --once, --width, --height, --json)\n",
		},
		{
			name: "narrow width", args: []string{"--once", "--width", "79"}, code: cli.ExitUsage,
			stderr: "dashboard: --width wants a whole number from 80 to 1000\n",
		},
		{
			name: "short height", args: []string{"--once", "--height", "abc"}, code: cli.ExitUsage,
			stderr: "dashboard: --height wants a whole number from 24 to 500\n",
		},
		{
			name: "missing value", args: []string{"--once", "--root"}, code: cli.ExitUsage,
			stderr: "--root needs a value (want <dir>)\n",
		},
		{
			name: "once with json", args: []string{"--once", "--json"}, code: cli.ExitUsage, json: true,
			stderr: "dashboard: draws a terminal frame and has no --json form; " +
				"read the same views as JSON through `verdict serve` or the MCP query tools\n",
		},
		{
			name: "missing root", args: []string{"--once", "--root", missing}, code: cli.ExitRefused,
			stderr: "error: dashboard: --root " + missing + " is not an existing directory\n" +
				"try: pass --root <existing-directory>, or run serve inside a checkout\n",
		},
	}
	for _, c := range cases {
		r, out, diag := runner(c.json)
		if code := Run(t.Context(), r, c.args, Request{}); code != c.code || diag.String() != c.stderr || out.Len() != 0 {
			t.Errorf("%s: exit %d stderr %q stdout %q, want exit %d stderr %q", c.name, code, diag.String(),
				out.String(), c.code, c.stderr)
		}
	}
}

func TestPickLook(t *testing.T) {
	t.Parallel()
	cases := []struct {
		pal         *palette
		color, term string
		cs          charset
	}{
		{&paletteANSI, "auto", "xterm-256color", charsetUnicode},
		{&paletteANSI, "always", "xterm", charsetUnicode},
		{&paletteMono, "never", "xterm", charsetUnicode},
		{&paletteMono, "auto", "dumb", charsetASCII},
	}
	for _, c := range cases {
		if got := pickLook(c.color, c.term); got.pal != c.pal || got.cs != c.cs {
			t.Errorf("pickLook(%q, %q) = %v %v", c.color, c.term, got.pal == &paletteANSI, got.cs)
		}
	}
}

// checkLines fails unless frame is minRows lines and each pinned line matches.
func checkLines(t *testing.T, frame string, want map[int]string) {
	t.Helper()
	lines := strings.Split(strings.TrimSuffix(frame, "\n"), "\n")
	if len(lines) != minRows {
		t.Fatalf("frame has %d lines, want %d:\n%s", len(lines), minRows, frame)
	}
	for i, line := range want {
		if lines[i] != line {
			t.Errorf("line %d = %q, want %q", i, lines[i], line)
		}
	}
}

// TestPreflightRefusesJSONWithATerminal proves --json never reaches the live
// view: with a terminal on both sides there is no refusal to shape, and the
// live frame has no JSON form, so it is a usage error before anything draws.
func TestPreflightRefusesJSONWithATerminal(t *testing.T) {
	t.Parallel()
	r, out, diag := runner(true)
	code, refused := preflight(r, &options{}, true, true)
	want := "dashboard: draws a terminal frame and has no --json form; " +
		"read the same views as JSON through `verdict serve` or the MCP query tools\n"
	if !refused || code != cli.ExitUsage || diag.String() != want || out.Len() != 0 {
		t.Fatalf("preflight = %d %v, stderr %q, stdout %q; want usage %q", code, refused, diag.String(), out.String(), want)
	}
	if code, refused := preflight(r, &options{}, false, true); !refused || code != cli.ExitRefused {
		t.Fatalf("no terminal under --json = %d %v, want the dashboard-no-tty refusal", code, refused)
	}
}

// cancelingSource cancels the request inside Bundles, the way Ctrl-C lands
// mid-compose, and counts what is read after it.
type cancelingSource struct {
	fixtureSource
	cancel context.CancelFunc
	roots  int
}

func (c *cancelingSource) Bundles(ctx context.Context, p string) (*query.View, error) {
	c.cancel()
	return c.fixtureSource.Bundles(ctx, p)
}

func (c *cancelingSource) Roots(ctx context.Context) *query.View {
	c.roots++
	return c.fixtureSource.Roots(ctx)
}

// TestLoadStopsAfterACancel proves a cancel during the Bundles compose ends
// the load with ctx.Err() and reads nothing more.
func TestLoadStopsAfterACancel(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	src := &cancelingSource{fixtureSource: partialFixture(), cancel: cancel}
	snap, err := load(ctx, src)
	if !errors.Is(err, context.Canceled) || snap != nil || src.roots != 0 {
		t.Fatalf("load = %v, %v with %d Roots read; want context.Canceled, nil, 0", snap, err, src.roots)
	}
}

// TestLiveViewGetsTheRawStdoutFile proves the runner's inert stdout does not
// hide the terminal from the live view: the wrapper over the real stdout
// yields the file (a wrapped writer alone yields none, which would refuse
// every terminal as no-tty), and the bytes a painter writes to it keep their
// own escape sequences while a write through the wrapper has them folded.
func TestLiveViewGetsTheRawStdoutFile(t *testing.T) {
	t.Parallel()
	file, err := os.Create(filepath.Join(t.TempDir(), "stdout"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	r := cli.NewRunner("dashboard", "", "", file, io.Discard, func(string) string { return "" })
	if fileOf(r.Out) != nil {
		t.Fatal("the wrapped writer is itself an *os.File; the unwrap would be unneeded")
	}
	out := stdoutFile(r)
	if out != file {
		t.Fatalf("stdoutFile = %v; want the underlying stdout file", out)
	}
	const paint = "\x1b[2J\x1b[1;1H"
	if _, err := out.WriteString(paint); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(r.Out, paint); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(raw), paint+" [2J [1;1H"; got != want {
		t.Fatalf("file holds %q; want the raw paint then the folded write %q", got, want)
	}
}
