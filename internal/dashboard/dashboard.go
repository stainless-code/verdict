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
	"strconv"
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

	flagRoot   = "--root"
	flagOnce   = "--once"
	flagWidth  = "--width"
	flagHeight = "--height"
	flagJSON   = "--json"

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

// options are the parsed flags.
type options struct {
	root          string
	width, height int
	once          bool
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
		return r.Refuse(&cli.RefusalError{Code: codeNoTTY, Message: errNoTTY.Error()}), true
	}
	if r.JSONMode {
		return usage(r, "dashboard: draws a terminal frame and has no --json form; "+
			"read the same views as JSON through `verdict serve` or the MCP query tools"), true
	}
	return 0, false
}

// fileOf is the runner's stdout as a file, or nil when a test substituted a
// buffer (which is never a terminal).
// stdoutFile is the process stdout under the runner's inert wrapper. The live
// view paints its own cursor and color sequences straight to that file, so it
// must see past the fold; every stranger-written cell string is flattened
// before it is drawn (grid.text), which is what keeps that raw path safe.
func stdoutFile(r *cli.Runner) *os.File {
	return fileOf(untrusted.Raw(r.Out))
}

func fileOf(w io.Writer) *os.File {
	if f, ok := w.(*os.File); ok {
		return f
	}
	return nil
}

func isTerminal(f *os.File) bool {
	return f != nil && cli.IsTerminal(f)
}

// runOnce composes the Overview once and prints it as plain text. Refused or
// partial bundles are content, drawn as rows and markers, so the exit is 0.
func runOnce(ctx context.Context, r *cli.Runner, src source, opts *options, term string, out *os.File) int {
	snap, err := load(ctx, src)
	if code, done := r.Canceled(ctx, verb); done {
		return code
	}
	if err != nil {
		return r.Refuse(err)
	}
	for _, w := range snap.warnings {
		r.Warn(w)
	}
	w, h := onceSize(opts, out)
	lk := pickLook(core.ColorNever, term)
	fmt.Fprint(r.Out, compose(&frameState{snap: snap, tab: tabOverview}, w, h, lk).lines())
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
		return r.Refuse(&cli.RefusalError{Code: codeNoVT, Message: err.Error()})
	}
	if err != nil {
		return r.Refuse(&cli.RefusalError{Code: codeNoTTY, Message: err.Error()})
	}
	lk := pickLook(r.Effective.Color, req.Term)
	err = live(ctx, t, out, lk, opts, func(ctx context.Context) (*snapshot, error) { return load(ctx, src) })
	if code, done := r.Canceled(ctx, verb); done {
		return code
	}
	if err != nil {
		return r.Refuse(err)
	}
	return cli.ExitOK
}

// live runs one interactive session on one goroutine: compose, paint the
// difference, poll a key for one tick, repeat. Restore runs on every way out,
// a panic included, before the caller writes anything to the main screen.
func live(
	ctx context.Context, t tty, out io.Writer, lk look, opts *options,
	loadSnap func(context.Context) (*snapshot, error),
) (err error) {
	s := &session{term: t, out: out}
	defer func() {
		if rerr := s.restore(); err == nil {
			err = rerr
		}
	}()
	v := &liveView{
		term: t, opts: opts, lk: lk, st: &frameState{live: true}, s: s,
		p: &painter{out: out, pal: lk.pal}, keys: make([]byte, keyBuf),
	}
	if err := v.start(ctx, s, loadSnap); err != nil {
		return err
	}
	for {
		done, err := v.tick(ctx)
		if done || err != nil {
			return err
		}
	}
}

// liveView is one session's loop state.
type liveView struct {
	term tty
	opts *options
	st   *frameState
	p    *painter
	s    *session
	keys []byte
	lk   look
	kb   keyState
}

// start enters the alternate screen, paints the composing frame, and runs
// the first compose.
func (v *liveView) start(ctx context.Context, s *session, loadSnap func(context.Context) (*snapshot, error)) error {
	if err := s.enter(); err != nil {
		return err
	}
	if err := v.paint(); err != nil {
		return err
	}
	snap, err := loadSnap(ctx)
	v.st.snap = snap
	return err
}

func (v *liveView) paint() error {
	cols, rows, err := frameSize(v.term, v.opts)
	if err != nil {
		return err
	}
	return v.p.paint(compose(v.st, cols, rows, v.lk))
}

// tick paints whatever changed, then waits one read tick for a key. done
// reports a quit, a cancel, or a terminal failure.
func (v *liveView) tick(ctx context.Context) (done bool, err error) {
	if err := v.paint(); err != nil {
		return true, err
	}
	if err := ctx.Err(); err != nil {
		return true, err
	}
	n, err := v.term.read(v.keys)
	if err != nil {
		return true, err
	}
	if v.kb.handle(v.st, v.keys[:n]) {
		return true, nil
	}
	if v.term.resized() {
		v.p.invalidate()
	}
	return v.suspendIfAsked(ctx)
}

// suspendIfAsked honors Ctrl-Z: the shell gets its screen and cooked mode
// back while the job is stopped, and the frame repaints in full on return.
func (v *liveView) suspendIfAsked(ctx context.Context) (done bool, err error) {
	if !v.term.stopRequested() {
		return false, nil
	}
	if err := v.s.suspend(ctx); err != nil {
		return true, err
	}
	v.p.invalidate()
	return false, nil
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

// keyState carries a lone Esc across one read tick: a terminal can split an
// arrow key's ESC from its `[A`, so Esc quits only when the next tick brings
// nothing (or something that does not continue a sequence).
type keyState struct {
	escPending bool
}

// handle applies one read's bytes: a digit picks a tab, q quits, and a lone
// Esc quits one tick later. An escape sequence (an arrow, a function key) is
// skipped whole, so its bytes never read as a digit.
func (k *keyState) handle(st *frameState, keys []byte) (quit bool) {
	if k.escPending {
		k.escPending = false
		if len(keys) == 0 || (keys[0] != '[' && keys[0] != 'O') {
			return true
		}
		return false // the rest of a split sequence
	}
	if len(keys) == 1 && keys[0] == keyEsc {
		k.escPending = true
		return false
	}
	return applyKeys(st, keys)
}

func applyKeys(st *frameState, keys []byte) (quit bool) {
	for _, b := range keys {
		switch b {
		case keyEsc:
			return false
		case 'q':
			return true
		case '1':
			st.tab = tabOverview
		case '2':
			st.tab = tabQueue
		}
	}
	return false
}

// openLayer builds the read model: the current checkout, or every repository
// discovery finds below --root (through the same registry cache serve keeps).
func openLayer(ctx context.Context, r *cli.Runner, opts *options, req Request) (source, *cli.RefusalError) {
	// No watcher in this slice: the session draws one composed snapshot.
	layerOpts := query.Options{NoWatch: true, MaxBundles: query.DefaultMaxBundles, Clock: r.Now}
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
		return nil, &cli.RefusalError{Code: codeBadRoot, Message: "dashboard: discover repositories: " + err.Error()}
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
		return "", &cli.RefusalError{Code: "not-a-repo", Message: err.Error()}
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", &cli.RefusalError{
			Code: codeBadRoot, Message: "dashboard: repository root " + root + " is not addressable: " + err.Error(),
		}
	}
	return abs, nil
}

func givenRoot(given string) (string, *cli.RefusalError) {
	abs, err := filepath.Abs(given)
	if err != nil {
		return "", &cli.RefusalError{Code: codeBadRoot, Message: "dashboard: --root " + given + " is not addressable: " + err.Error()}
	}
	if info, err := os.Stat(abs); err != nil || !info.IsDir() {
		return "", &cli.RefusalError{Code: codeBadRoot, Message: "dashboard: --root " + abs + " is not an existing directory"}
	}
	return abs, nil
}

// parseFlags reads the flags left to right.
func parseFlags(r *cli.Runner, args []string) (options, int, bool) {
	var opts options
	for i := 0; i < len(args); i++ {
		next, code, done := parseFlag(r, &opts, args, i)
		if done {
			return opts, code, true
		}
		i = next
	}
	return opts, 0, false
}

// parseFlag reads the flag at args[i] and returns the last index it consumed.
func parseFlag(r *cli.Runner, opts *options, args []string, i int) (next, code int, done bool) {
	switch name := args[i]; name {
	case flagJSON:
		return i, 0, false
	case flagOnce:
		opts.once = true
		return i, 0, false
	case flagRoot, flagWidth, flagHeight:
		value, next, uerr := cli.FlagValue(args, i, name, flagWants(name))
		if uerr != nil {
			return i, r.ReportUsage(uerr), true
		}
		code, done := applyFlag(r, opts, name, value)
		return next, code, done
	default:
		return i, usage(r, "unknown flag '"+name+"' for dashboard (want --root, --once, --width, --height, --json)"), true
	}
}

func flagWants(name string) string {
	if name == flagRoot {
		return "<dir>"
	}
	return "<n>"
}

func applyFlag(r *cli.Runner, opts *options, name, value string) (int, bool) {
	switch name {
	case flagWidth:
		return applySize(r, name, value, minCols, maxCols, &opts.width)
	case flagHeight:
		return applySize(r, name, value, minRows, maxRows, &opts.height)
	default:
		if value == "" {
			return usage(r, "dashboard: --root wants a non-empty directory"), true
		}
		opts.root = value
		return 0, false
	}
}

func applySize(r *cli.Runner, name, value string, lo, hi int, target *int) (int, bool) {
	n, err := strconv.Atoi(value)
	if err != nil || n < lo || n > hi {
		return usage(r, "dashboard: "+name+" wants a whole number from "+strconv.Itoa(lo)+" to "+strconv.Itoa(hi)), true
	}
	*target = n
	return 0, false
}

func usage(r *cli.Runner, message string) int {
	return r.ReportUsage(&cli.UsageError{Message: message})
}
