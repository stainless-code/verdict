// Package dashboard is the `dashboard` verb: a zero-dependency terminal view
// over the same query layer `serve` renders as HTTP. It reads review stores
// and draws them; it never writes a store, posts, or runs anything a bundle
// names. The live view needs a terminal on both stdin and stdout; --once
// draws one plain-text frame to stdout for everyone else.
package dashboard

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/query"
	"github.com/stainless-code/verdict/internal/readcommon"
	"github.com/stainless-code/verdict/internal/untrusted"
)

const (
	verb = "dashboard"

	codeNoTTY   = "dashboard-no-tty"
	codeNoVT    = "dashboard-no-vt"
	codeBadRoot = "bad-root"
	// msgErrText is a refusal that is an error's own text, folded to one line.
	msgErrText = "%v"

	keyEsc = 0x1b
	// keyBuf holds one read's worth of keys; a paste longer than this is
	// simply read over the next ticks.
	keyBuf = 64
)

// Request carries what the CLI front door resolved for the verb.
type Request struct {
	// In is the terminal the live view reads keys from; nil means none.
	In *os.File
	// Term is $TERM; "dumb" selects ASCII glyphs and no color.
	Term    string
	Version string
}

// Run executes `dashboard [flags]`: 0 after q or a --once frame, 1 for a
// refusal, 2 for argv shape, and 3 when Ctrl-C or SIGTERM cancels it.
func Run(ctx context.Context, r *cli.Runner, args []string, req Request) int {
	opts, code, done := parseFlags(r, args)
	if done {
		return code
	}
	out := stdoutFile(r)
	if code, refused := preflight(r, &opts, isTerminal(req.In), isTerminal(out)); refused {
		return code
	}
	src, ref := openLayer(ctx, r, &opts, req)
	if ref != nil {
		return r.Refuse(ref)
	}
	if opts.once {
		return runOnce(ctx, r, src, &opts, req.Term, out)
	}
	return runLive(ctx, r, src, &opts, req, out)
}

// preflight refuses what cannot work before any store is read. Without a
// terminal the live view refuses dashboard-no-tty (an envelope under --json,
// the one thing --json shapes); with one, --json is a usage error, since
// neither the live view nor --once has a JSON form.
func preflight(r *cli.Runner, opts *options, inTTY, outTTY bool) (int, bool) {
	if !opts.once && (!inTTY || !outTTY) {
		return r.Refuse(&cli.RefusalError{Code: codeNoTTY, Message: core.Msgf(msgErrText, errNoTTY)}), true
	}
	if r.JSONMode {
		return usage(r, "dashboard: draws a terminal frame and has no --json form; "+
			"read the same views as JSON through `verdict serve` or the MCP query tools"), true
	}
	return 0, false
}

// stdoutFile is the process stdout under the runner's inert wrapper. The live
// view paints its own cursor, color, and OSC 52 clipboard sequences straight
// to that file, so it must see past the fold; every stranger-written cell
// string is flattened before it is drawn (grid.text), and the clipboard
// payload is base64, which is what keeps that raw path safe.
func stdoutFile(r *cli.Runner) *os.File {
	return fileOf(untrusted.Raw(r.Out))
}

// fileOf is the writer as a file, or nil when a test substituted a buffer
// (which is never a terminal).
func fileOf(w io.Writer) *os.File {
	if f, ok := w.(*os.File); ok {
		return f
	}
	return nil
}

func isTerminal(f *os.File) bool {
	return f != nil && cli.IsTerminal(f)
}

// runOnce composes one tab (--tab, the Overview by default) and prints it as
// plain text. Refused or partial bundles, and a refused tab view, are
// content drawn in the frame, so the exit is 0.
func runOnce(ctx context.Context, r *cli.Runner, src source, opts *options, term string, out *os.File) int {
	st := &frameState{tab: opts.tab, repo: opts.repo}
	res := runJob(ctx, src, pickTarget(st))
	if code, done := r.Canceled(ctx, verb); done {
		return code
	}
	if res.snap == nil {
		return r.Refuse(res.err)
	}
	st.snap = filterRepo(res.snap, opts.repo)
	installView(st, &res.want, res.view, res.err)
	for _, w := range res.snap.warnings {
		r.Warn(w)
	}
	w, h := onceSize(opts, out)
	lk := pickLook(core.ColorNever, term)
	fmt.Fprint(r.Out, compose(st, w, h, lk).lines())
	return cli.ExitOK
}

// onceSize is the --once frame: the flags, else the terminal stdout is, else
// 80×24. A terminal smaller than that still gets the minimum frame, since a
// snapshot has no resize to wait for.
func onceSize(opts *options, out *os.File) (cols, rows int) {
	cols, rows = minCols, minRows
	if out != nil {
		if c, r, ok := termSize(out); ok {
			cols, rows = max(c, minCols), max(r, minRows)
		}
	}
	if opts.width > 0 {
		cols = opts.width
	}
	if opts.height > 0 {
		rows = opts.height
	}
	return cols, rows
}

func runLive(ctx context.Context, r *cli.Runner, src source, opts *options, req Request, out *os.File) int {
	t, err := openTTY(req.In, out)
	if errors.Is(err, errNoVT) {
		return r.Refuse(&cli.RefusalError{Code: codeNoVT, Message: core.Msgf(msgErrText, err)})
	}
	if err != nil {
		return r.Refuse(&cli.RefusalError{Code: codeNoTTY, Message: core.Msgf(msgErrText, err)})
	}
	lk := pickLook(r.Effective.Color, req.Term)
	err = live(ctx, t, out, lk, opts, src)
	if code, done := r.Canceled(ctx, verb); done {
		return code
	}
	if err != nil {
		return r.Refuse(err)
	}
	return cli.ExitOK
}

// frameSize is the terminal's current size, narrowed by --width/--height
// when given: a frame wider or taller than the terminal would wrap and
// scroll, so the flags can only shrink the live frame.
func frameSize(t tty, opts *options) (cols, rows int, err error) {
	cols, rows, err = t.size()
	if err != nil {
		return 0, 0, err
	}
	if opts.width > 0 {
		cols = min(cols, opts.width)
	}
	if opts.height > 0 {
		rows = min(rows, opts.height)
	}
	return cols, rows, nil
}

// openLayer builds the read model: the current checkout, or every repository
// discovery finds below --root (through the same registry cache serve keeps).
func openLayer(ctx context.Context, r *cli.Runner, opts *options, req Request) (source, *cli.RefusalError) {
	// A --once frame is one compose, so it never starts the watcher.
	layerOpts := query.Options{
		NoWatch: opts.noWatch || opts.once, Interval: opts.interval, Sweep: opts.sweep,
		Check: opts.check, MaxBundles: query.DefaultMaxBundles, Clock: r.Now,
	}
	runtime := &core.Runtime{Now: r.Now}
	if opts.root == "" {
		root, ref := probeRoot(ctx)
		if ref != nil {
			return nil, ref
		}
		return query.New(query.Snapshot{Version: req.Version, Roots: []query.Root{{Path: root}}, Runtime: runtime}, layerOpts), nil
	}
	root, ref := givenRoot(opts.root)
	if ref != nil {
		return nil, ref
	}
	discovery, err := discover(root, r.Now)
	if err != nil {
		return nil, &cli.RefusalError{Code: codeBadRoot, Message: core.Msgf("dashboard: discover repositories: %s", err.Error())}
	}
	return query.New(query.Snapshot{
		Version: req.Version, Roots: discovery.Roots, Runtime: runtime, Discovery: discovery,
	}, layerOpts), nil
}

// discover walks root with serve's defaults and its 24-hour registry cache:
// the dashboard's one write, outside every repository.
func discover(root string, now func() time.Time) (query.Discovery, error) {
	opts := query.DiscoveryOptions{
		Depth: query.DefaultDiscoveryDepth, MaxVisits: query.DefaultMaxVisits, MaxRepos: query.DefaultMaxRepos, Now: now(),
	}
	home, err := os.UserHomeDir()
	if err != nil {
		d, derr := query.Discover(root, opts)
		if derr == nil {
			d.Warnings = append(d.Warnings, "dashboard: home directory unavailable; discovery registry disabled")
		}
		return d, derr //nolint:wrapcheck // openLayer adds the dashboard context
	}
	path := config.RegistryPath(home, os.Getenv("XDG_CONFIG_HOME"))
	return query.DiscoverCached(root, opts, path, false, false) //nolint:wrapcheck // openLayer adds the dashboard context
}

func probeRoot(ctx context.Context) (string, *cli.RefusalError) {
	root, err := readcommon.RepoRoot(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return "", &cli.RefusalError{Code: cli.CodeCanceled, Message: "canceled before dashboard completed"}
		}
		return "", &cli.RefusalError{Code: "not-a-repo", Message: core.Msgf(msgErrText, err)}
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", &cli.RefusalError{
			Code: codeBadRoot, Message: core.Msgf("dashboard: repository root %s is not addressable: %s", root, err.Error()),
		}
	}
	return abs, nil
}

func givenRoot(given string) (string, *cli.RefusalError) {
	abs, err := filepath.Abs(given)
	if err != nil {
		return "", &cli.RefusalError{Code: codeBadRoot, Message: core.Msgf(
			"dashboard: --root %s is not addressable: %s", given, err.Error())}
	}
	if info, err := os.Stat(abs); err != nil || !info.IsDir() {
		return "", &cli.RefusalError{Code: codeBadRoot, Message: core.Msgf("dashboard: --root %s is not an existing directory", abs)}
	}
	return abs, nil
}
