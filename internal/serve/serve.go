// Package serve is the `serve` verb's transport adapter: one read-only,
// loopback HTTP surface over the query layer the CLI verbs share.
package serve

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/query"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

const (
	// defaultHost keeps every default bind on loopback.
	defaultHost = loopbackV4
	// defaultCheck is the gate tier a detail composes.
	defaultCheck = "critical"
	// readHeaderTimeout bounds one request's header read.
	readHeaderTimeout = 5 * time.Second
	// readTimeout bounds one request's read, so a slow client cannot hold a
	// connection open.
	readTimeout = 30 * time.Second
	// idleTimeout bounds a keep-alive connection between requests.
	idleTimeout = 60 * time.Second
	// shutdownTimeout bounds the graceful drain on cancellation.
	shutdownTimeout = 5 * time.Second

	flagRoot       = "--root"
	flagHost       = "--host"
	flagPort       = "--port"
	flagToken      = "--token"
	flagCheck      = "--check"
	flagMaxBundles = "--max-bundles"
	flagJSON       = "--json"

	codeBadRoot           = "bad-root"
	codeServeListenFailed = "serve-listen-failed"

	maxPort = 65535
)

// startError is one start-up sentinel; the caller maps it to a typed refusal.
type startError string

// Error renders the sentinel.
func (e startError) Error() string { return string(e) }

// Start-up sentinels.
var (
	errNoRoot     error = startError("serve: no root to serve")
	errNoListener error = startError("serve: listener is not bound")
	errRange      error = startError("value out of range")
)

// Layer is the read model the transport serves. The query package implements
// it; tests substitute a failing commander through its runtime seam.
type Layer interface {
	Health(ctx context.Context) *query.View
	Roots(ctx context.Context) *query.View
	Bundles(ctx context.Context, pattern string) (*query.View, error)
	Detail(ctx context.Context, rootID, bundle string) (*query.View, error)
	Show(ctx context.Context, rootID, bundle, sig string) (*query.View, error)
}

// Config is one server's resolved start state.
type Config struct { //nolint:govet // start snapshot; field order mirrors the flag table
	Root       string
	Host       string
	Port       int
	Token      string
	Check      string
	MaxBundles int
	Version    string
	Now        func() time.Time
	Layer      Layer
}

// Server is one bound (or about-to-bind) listener. Its route table is the
// only registration point: a handler outside it cannot exist.
type Server struct { //nolint:govet // grouped by listener, allowlists, and injected seams
	cfg      Config
	token    string
	now      func() time.Time
	layer    Layer
	mu       sync.RWMutex
	listener net.Listener
	port     int
	hosts    map[string]bool
	origins  map[string]bool
}

// New builds a server over one root (or the injected layer) and mints its
// token when none was supplied.
func New(cfg *Config) (*Server, error) {
	if cfg.Host == "" {
		cfg.Host = defaultHost
	}
	if cfg.Check == "" {
		cfg.Check = defaultCheck
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	layer := cfg.Layer
	if layer == nil {
		if cfg.Root == "" {
			return nil, errNoRoot
		}
		layer = query.New(query.Snapshot{
			Version: cfg.Version,
			Roots:   []query.Root{{Path: cfg.Root}},
			Runtime: &core.Runtime{Now: cfg.Now},
		}, query.Options{Check: cfg.Check, MaxBundles: cfg.MaxBundles})
	}
	token := cfg.Token
	if token == "" {
		token = newToken()
	}
	server := &Server{
		cfg:   *cfg,
		token: token,
		now:   cfg.Now,
		layer: layer,
		port:  cfg.Port,
	}
	server.refreshAllowlists()
	return server, nil
}

// Listen binds the configured address and records the kernel-assigned port.
func (s *Server) Listen(ctx context.Context) error {
	address := net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.Port))
	var config net.ListenConfig
	listener, err := config.Listen(ctx, "tcp", address)
	if err != nil {
		return jq.Sealed("serve: listen on "+address, err)
	}
	port := s.cfg.Port
	if tcp, ok := listener.Addr().(*net.TCPAddr); ok {
		port = tcp.Port
	}
	s.mu.Lock()
	s.listener = listener
	s.port = port
	s.mu.Unlock()
	s.refreshAllowlists()
	return nil
}

// Addr is the host:port the start line and the JSON envelope publish.
func (s *Server) Addr() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.port))
}

// URL is the token-bearing start URL. It is the only place the capability is
// printed.
func (s *Server) URL() string {
	return "http://" + s.Addr() + "/?token=" + url.QueryEscape(s.token)
}

// Close releases the listener; an already-closed listener is not an error.
func (s *Server) Close() error {
	s.mu.RLock()
	listener := s.listener
	s.mu.RUnlock()
	if listener == nil {
		return nil
	}
	if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		return jq.Sealed("serve: close listener", err)
	}
	return nil
}

// Serve blocks until ctx is canceled, then drains the listener within the
// shutdown bound. The server owns no goroutine beyond the listener loop.
func (s *Server) Serve(ctx context.Context) error {
	s.mu.RLock()
	listener := s.listener
	s.mu.RUnlock()
	if listener == nil {
		return errNoListener
	}
	server := newHTTPServer(s.Handler())
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	select {
	case <-ctx.Done():
		drain, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
		defer cancel()
		if err := server.Shutdown(drain); err != nil {
			return jq.Sealed("serve: shutdown", err)
		}
		return nil
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return jq.Sealed("serve: http listener", err)
	}
}

// newHTTPServer bounds every connection: header and request reads, and an idle
// keep-alive. No WriteTimeout: the SSE route will stream for as long as a
// client stays connected.
func newHTTPServer(handler http.Handler) *http.Server {
	return &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		IdleTimeout:       idleTimeout,
	}
}

// Request carries the CLI's resolved build version into the adapter; the
// root comes from the parsed flags.
type Request struct {
	Version string
}

// serveOptions are the S1 flag values. Discovery, watch, and registry flags
// arrive with their slices; an unknown flag stays a usage error rather than a
// silently ignored promise.
type serveOptions struct { //nolint:govet // field order mirrors the flag table
	root       string
	host       string
	port       int
	token      string
	tokenSet   bool
	check      string
	maxBundles int
}

// Run executes `serve [flags]`, returning the process exit code: 0 after a
// clean shutdown, 3 when the root context was canceled, 2 for argv shape,
// and 1 for a start refusal.
func Run(ctx context.Context, r *cli.Runner, args []string, req Request) int {
	opts, code, done := parseFlags(r, args)
	if done {
		return code
	}
	root, ref := resolveRoot(ctx, opts.root)
	if ref != nil {
		return r.Refuse(ref)
	}
	warnExposure(r, &opts)
	server, code, ok := start(ctx, r, &opts, root, req)
	if !ok {
		return code
	}
	defer func() { _ = server.Close() }()
	announce(r, server)
	if err := server.Serve(ctx); err != nil {
		fmt.Fprintln(r.Diag, err)
		return cli.ExitRefused
	}
	if ctx.Err() != nil {
		return cli.ExitBroke
	}
	return cli.ExitOK
}

// warnExposure writes the two start warnings the flags can earn.
func warnExposure(r *cli.Runner, opts *serveOptions) {
	if opts.tokenSet {
		r.Warn("serve: --token is visible in your process list and shell history")
	}
	if !isLoopback(opts.host) {
		r.Warn("serve: binding " + opts.host + " exposes the read-only surface beyond loopback " +
			"over plain HTTP; the token and session cookie are not encrypted")
	}
}

// start builds and binds the listener, returning the exit code on refusal.
func start(
	ctx context.Context, r *cli.Runner, opts *serveOptions, root string, req Request,
) (*Server, int, bool) {
	server, err := New(&Config{
		Root: root, Host: opts.host, Port: opts.port, Token: opts.token,
		Check: opts.check, MaxBundles: opts.maxBundles, Version: req.Version, Now: r.Now,
	})
	if err != nil {
		return nil, r.Refuse(&cli.RefusalError{Code: codeServeListenFailed, Message: err.Error()}), false
	}
	if err := server.Listen(ctx); err != nil {
		return nil, r.Refuse(&cli.RefusalError{Code: codeServeListenFailed, Message: err.Error()}), false
	}
	return server, 0, true
}

// announce prints the one start surface: the JSON envelope when requested,
// otherwise one tokenised URL line.
func announce(r *cli.Runner, server *Server) {
	if r.JSONMode {
		data := jq.NewObj(
			"url", server.URL(),
			"listen", server.Addr(),
			"read_only", true,
			"partial", false,
			"sources", []any{},
			"errors", []any{},
		)
		env := core.RenderEnvelope("serve", "", "", cli.NowUTC(r.Now()), "",
			r.Warnings, data, nil)
		fmt.Fprint(r.Out, jq.PrettyOrdered(env))
		return
	}
	fmt.Fprintln(r.Human, "serve: "+server.URL())
	fmt.Fprintln(r.Diag, "serve: listening on "+server.Addr()+" (read-only; Ctrl-C stops)")
}

// resolveRoot resolves the served root: --root first (clean, absolute,
// existing directory), then the repository probe every read verb shares.
func resolveRoot(ctx context.Context, given string) (string, *cli.RefusalError) {
	if given != "" {
		return resolveGivenRoot(given)
	}
	return resolveProbeRoot(ctx)
}

// resolveGivenRoot validates an explicit --root.
func resolveGivenRoot(given string) (string, *cli.RefusalError) {
	abs, err := filepath.Abs(given)
	if err != nil {
		return "", &cli.RefusalError{Code: codeBadRoot, Message: "serve: --root " + given + " is not addressable: " + err.Error()}
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", &cli.RefusalError{Code: codeBadRoot, Message: "serve: --root " + abs + " does not exist or is unreadable"}
	}
	if !info.IsDir() {
		return "", &cli.RefusalError{Code: codeBadRoot, Message: "serve: --root " + abs + " is not a directory"}
	}
	return abs, nil
}

// resolveProbeRoot resolves the current repository like every read verb.
func resolveProbeRoot(ctx context.Context) (string, *cli.RefusalError) {
	root, err := readcommon.RepoRoot(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return "", &cli.RefusalError{Code: cli.CodeCanceled, Message: "canceled before serve completed"}
		}
		return "", &cli.RefusalError{Code: "not-a-repo", Message: err.Error()}
	}
	abs, aerr := filepath.Abs(root)
	if aerr != nil {
		return "", &cli.RefusalError{Code: codeBadRoot, Message: "serve: repository root " + root + " is not addressable: " + aerr.Error()}
	}
	return abs, nil
}

// parseFlags reads the S1 flag set left to right.
func parseFlags(r *cli.Runner, args []string) (serveOptions, int, bool) {
	opts := serveOptions{host: defaultHost, check: defaultCheck, maxBundles: query.DefaultMaxBundles}
	for i := 0; i < len(args); i++ {
		next, code, done := parseFlag(r, &opts, args, i)
		if done {
			return opts, code, true
		}
		i = next
	}
	return opts, 0, false
}

// parseFlag reads one flag, returning the operand index it consumed.
func parseFlag(r *cli.Runner, opts *serveOptions, args []string, i int) (int, int, bool) {
	name := args[i]
	switch name {
	case flagJSON:
		return i, 0, false
	case flagRoot, flagHost, flagPort, flagToken, flagCheck, flagMaxBundles:
		value, next, uerr := cli.FlagValue(args, i, name, flagWants(name))
		if uerr != nil {
			return i, r.ReportUsage(uerr), true
		}
		code, done := applyFlag(r, opts, name, value)
		return next, code, done
	default:
		return i, r.ReportUsage(&cli.UsageError{Message: fmt.Sprintf(
			"unknown flag '%s' for serve (want --root, --host, --port, --token, --check, "+
				"--max-bundles, --json)", name)}), true
	}
}

// flagWants names one value flag's operand for the missing-value message.
func flagWants(name string) string {
	switch name {
	case flagPort:
		return "<n>"
	case flagMaxBundles:
		return "<n>"
	case flagCheck:
		return "critical|important|informational"
	case flagToken:
		return "<token>"
	default:
		return "<dir>"
	}
}

// applyFlag validates one value flag. The operand index is the loop's next
// iteration; done reports an argv-shape refusal.
func applyFlag(r *cli.Runner, opts *serveOptions, name, value string) (int, bool) {
	switch name {
	case flagRoot:
		opts.root = value
	case flagHost:
		return applyHost(r, opts, value)
	case flagToken:
		return applyToken(r, opts, value)
	case flagPort:
		return applyPort(r, opts, value)
	case flagMaxBundles:
		return applyMaxBundles(r, opts, value)
	case flagCheck:
		return applyCheck(r, opts, value)
	}
	return 0, false
}

// applyHost sets the bind address, refusing an empty one.
func applyHost(r *cli.Runner, opts *serveOptions, value string) (int, bool) {
	if value == "" {
		return usage(r, "serve: --host wants a non-empty address"), true
	}
	opts.host = value
	return 0, false
}

// applyToken sets a fixed capability and records that it came from argv.
func applyToken(r *cli.Runner, opts *serveOptions, value string) (int, bool) {
	if value == "" {
		return usage(r, "serve: --token wants a non-empty value (omit the flag for a random token)"), true
	}
	//nolint:gosec // the cookie is never written; Valid only checks the value grammar net/http will serialize.
	if err := (&http.Cookie{Name: "verdict_session", Value: value}).Valid(); err != nil {
		return usage(r, "serve: --token is not a valid session-cookie value "+
			"(no quote, backslash, semicolon, or control byte)"), true
	}
	opts.token = value
	opts.tokenSet = true
	return 0, false
}

// applyPort sets the listen port.
func applyPort(r *cli.Runner, opts *serveOptions, value string) (int, bool) {
	port, err := parseNumber(value, 0, maxPort)
	if err != nil {
		return usage(r, "serve: --port "+value+" is not a port (want 0-65535)"), true
	}
	opts.port = port
	return 0, false
}

// applyMaxBundles sets the listing cap.
func applyMaxBundles(r *cli.Runner, opts *serveOptions, value string) (int, bool) {
	bundles, err := parseNumber(value, 1, 0)
	if err != nil {
		return usage(r, "serve: --max-bundles "+value+" is not a positive count"), true
	}
	opts.maxBundles = bundles
	return 0, false
}

// applyCheck sets the detail's gate tier.
func applyCheck(r *cli.Runner, opts *serveOptions, value string) (int, bool) {
	if !core.ValidTier(value) {
		return usage(r, "serve: unknown --check tier '"+value+"' (want critical|important|informational)"), true
	}
	opts.check = value
	return 0, false
}

// parseNumber parses one decimal flag value, optionally bounded.
func parseNumber(value string, minimum, maximum int) (int, error) {
	number, err := strconv.Atoi(value)
	if err != nil {
		return 0, jq.Sealed("parse "+value, err)
	}
	if number < minimum || (maximum > 0 && number > maximum) {
		return 0, errRange
	}
	return number, nil
}

// usage renders one argv-shape failure as exit 2.
func usage(r *cli.Runner, message string) int {
	return r.ReportUsage(&cli.UsageError{Message: message})
}
