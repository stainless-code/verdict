package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core/query"
	"github.com/stainless-code/verdict/internal/lsp"
	"github.com/stainless-code/verdict/internal/readcommon"
)

const (
	flagLSPRoot       = "--root"
	flagLSPBundle     = "--bundle"
	flagLSPCheck      = "--check"
	flagLSPMaxBundles = "--max-bundles"
	flagLSPMaxRoots   = "--max-roots"
	flagLSPInterval   = "--interval"
	flagLSPSweep      = "--sweep"
	flagLSPNoWatch    = "--no-watch"
	flagLSPJSON       = "--json"
	defaultLSPCheck   = "informational"
	defaultLSPRoots   = 8
	maxLSPRoots       = 16
	minLSPInterval    = 100 * time.Millisecond
	codeLSPBadRoot    = "bad-root"
	codeLSPNotARepo   = "not-a-repo"
)

// lspUsageText is `verdict lsp --help`: the stdio transport contract, its
// flags, and the LSP surface an editor counts on.
func lspUsageText() string {
	return "usage: verdict lsp [--root <dir>]... [--bundle <name>] [--check <tier>] [--max-bundles <n>]\n" +
		"                   [--max-roots <n>] [--interval <duration>] [--sweep <duration>] [--no-watch] [--json]\n" +
		"  Serve a review bundle's findings as LSP 3.17 diagnostics over stdio\n" +
		"  (JSON-RPC 2.0, Content-Length framed). stdout is the protocol channel;\n" +
		"  warnings go to stderr. Experimental until 1.0.\n" +
		"  Capabilities: textDocumentSync (open/close, full change, save with text),\n" +
		"  push textDocument/publishDiagnostics, workspace/executeCommand verdict.refresh.\n" +
		"  Diagnostics move on didOpen, didSave, a bundle watch change, and verdict.refresh;\n" +
		"  never on didChange. No hover or code actions yet, no model calls, no writes,\n" +
		"  no workspace file reads; the process exits on exit or when stdin closes.\n" +
		"  --root <dir>  serve the bundles under <dir>; repeat for several roots\n" +
		"                (default: the current repository)\n" +
		"  --bundle <name>  publish this bundle (default: the newest local- bundle with findings,\n" +
		"                   then the newest PR bundle)\n" +
		"  --check <tier>  gate tier the detail composes (critical|important|informational;\n" +
		"                  default informational); it never filters findings\n" +
		"  --max-bundles <n>  bundle listing cap per root (default 500)\n" +
		"  --max-roots <n>  refuse more than <n> --root values (default 8, at most 16)\n" +
		"  --interval <duration>  watch tick (default 1s, at least 100ms)\n" +
		"  --sweep <duration>  watch content sweep (default 30s, at least --interval)\n" +
		"  --no-watch  publish on open, save, and verdict.refresh only\n" +
		"  --json  accepted for CLI-convention parity; stdio is LSP either way\n" +
		"  Exit 0 after shutdown+exit or on stdin EOF, 1 on exit without shutdown or a start\n" +
		"  refusal, 2 on argv shape, 3 on a caught signal."
}

// runLSP executes `verdict lsp [flags]`: parse the transport flags, resolve
// the served roots, and speak LSP until exit or EOF.
func runLSP(ctx context.Context, r *cli.Runner, args []string, version string) int {
	opts, code, done := parseLSPFlags(r, args)
	if done {
		return code
	}
	roots, ref := resolveLSPRoots(ctx, opts.roots)
	if ref != nil {
		return r.Refuse(ref)
	}
	server := lsp.New(&lsp.Config{
		Version:    version,
		Roots:      roots,
		Bundle:     opts.bundle,
		Check:      opts.check,
		MaxBundles: opts.maxBundles,
		Interval:   opts.interval,
		Sweep:      opts.sweep,
		NoWatch:    opts.noWatch,
		Now:        r.Now,
		Stderr:     r.Diag,
	})
	if ref := server.Preflight(); ref != nil {
		return r.Refuse(ref)
	}
	err := server.ServeInterruptible(ctx, closableReader(os.Stdin), os.Stdout)
	if ctx.Err() != nil {
		return cli.ExitBroke
	}
	if errors.Is(err, lsp.ErrExitWithoutShutdown) {
		return cli.ExitRefused
	}
	if err != nil {
		fmt.Fprintf(r.Diag, "lsp: %v\n", err)
		return cli.ExitRefused
	}
	return cli.ExitOK
}

type lspOptions struct {
	bundle     string
	check      string
	roots      []string
	interval   time.Duration
	sweep      time.Duration
	maxBundles int
	maxRoots   int
	noWatch    bool
}

// parseLSPFlags reads the transport flags left to right, then checks the
// cross-flag bounds.
func parseLSPFlags(r *cli.Runner, args []string) (lspOptions, int, bool) {
	opts := lspOptions{
		check: defaultLSPCheck, maxBundles: query.DefaultMaxBundles, maxRoots: defaultLSPRoots,
		interval: time.Second, sweep: query.DefaultSweep,
	}
	for i := 0; i < len(args); i++ {
		next, code, done := parseLSPFlag(r, &opts, args, i)
		if done {
			return opts, code, true
		}
		i = next
	}
	if len(opts.roots) > opts.maxRoots {
		return opts, lspUsage(r, fmt.Sprintf("lsp: %d --root values exceed --max-roots %d", len(opts.roots), opts.maxRoots)), true
	}
	if opts.sweep < opts.interval {
		return opts, lspUsage(r, "lsp: --sweep must be at least --interval"), true
	}
	return opts, 0, false
}

func parseLSPFlag(r *cli.Runner, opts *lspOptions, args []string, i int) (int, int, bool) {
	name := args[i]
	switch name {
	case flagShortHelp, flagHelp:
		fmt.Fprint(r.Out, lspUsageText())
		return i, cli.ExitOK, true
	case flagLSPJSON:
		return i, 0, false
	case flagLSPNoWatch:
		opts.noWatch = true
		return i, 0, false
	case flagLSPRoot, flagLSPBundle, flagLSPCheck, flagLSPMaxBundles, flagLSPMaxRoots, flagLSPInterval, flagLSPSweep:
		value, next, uerr := cli.FlagValue(args, i, name, lspFlagWant(name))
		if uerr != nil {
			return i, r.ReportUsage(uerr), true
		}
		code, done := applyLSPFlag(r, opts, name, value)
		return next, code, done
	default:
		return i, lspUsage(r, fmt.Sprintf("unknown flag '%s' for lsp (want --root, --bundle, --check, "+
			"--max-bundles, --max-roots, --interval, --sweep, --no-watch, --json)", name)), true
	}
}

func lspFlagWant(name string) string {
	switch name {
	case flagLSPCheck:
		return "critical|important|informational"
	case flagLSPMaxBundles, flagLSPMaxRoots:
		return "<n>"
	case flagLSPInterval, flagLSPSweep:
		return "<duration>"
	case flagLSPBundle:
		return "<name>"
	default:
		return "<dir>"
	}
}

func applyLSPFlag(r *cli.Runner, opts *lspOptions, name, value string) (int, bool) {
	switch name {
	case flagLSPRoot:
		opts.roots = append(opts.roots, value)
	case flagLSPBundle:
		if value == "" {
			return lspUsage(r, "lsp: --bundle wants a non-empty bundle name"), true
		}
		opts.bundle = value
	case flagLSPCheck:
		switch value {
		case "critical", "important", "informational":
			opts.check = value
		default:
			return lspUsage(r, "lsp: --check "+value+" wants critical|important|informational"), true
		}
	case flagLSPMaxBundles:
		return applyLSPCount(r, name, value, 0, &opts.maxBundles)
	case flagLSPMaxRoots:
		return applyLSPCount(r, name, value, maxLSPRoots, &opts.maxRoots)
	case flagLSPInterval, flagLSPSweep:
		return applyLSPDuration(r, opts, name, value)
	}
	return 0, false
}

// applyLSPCount reads one positive count, bounded above when ceiling > 0.
func applyLSPCount(r *cli.Runner, name, value string, ceiling int, target *int) (int, bool) {
	n, err := strconv.Atoi(value)
	if err != nil || n < 1 || (ceiling > 0 && n > ceiling) {
		bound := "a positive count"
		if ceiling > 0 {
			bound = fmt.Sprintf("a count from 1 to %d", ceiling)
		}
		return lspUsage(r, "lsp: "+name+" "+value+" is not "+bound), true
	}
	*target = n
	return 0, false
}

func applyLSPDuration(r *cli.Runner, opts *lspOptions, name, value string) (int, bool) {
	d, err := time.ParseDuration(value)
	if err != nil || d < minLSPInterval {
		return lspUsage(r, "lsp: "+name+" wants a duration of at least 100ms"), true
	}
	if name == flagLSPInterval {
		opts.interval = d
	} else {
		opts.sweep = d
	}
	return 0, false
}

func lspUsage(r *cli.Runner, message string) int {
	return r.ReportUsage(&cli.UsageError{Message: message})
}

// resolveLSPRoots resolves every served root: each --root must be an
// existing directory; with none, the repository probe the read verbs share.
func resolveLSPRoots(ctx context.Context, given []string) ([]string, *cli.RefusalError) {
	if len(given) == 0 {
		root, ref := probeLSPRoot(ctx)
		if ref != nil {
			return nil, ref
		}
		given = []string{root}
	}
	roots := make([]string, 0, len(given))
	for _, g := range given {
		abs, ref := resolveLSPRoot(g)
		if ref != nil {
			return nil, ref
		}
		roots = append(roots, abs)
	}
	return roots, nil
}

// probeLSPRoot finds the current repository, as every read verb does.
func probeLSPRoot(ctx context.Context) (string, *cli.RefusalError) {
	root, err := readcommon.RepoRoot(ctx)
	if err == nil {
		return root, nil
	}
	if ctx.Err() != nil {
		return "", &cli.RefusalError{Code: cli.CodeCanceled, Message: "canceled before lsp completed"}
	}
	return "", &cli.RefusalError{Code: codeLSPNotARepo, Message: err.Error()}
}

// resolveLSPRoot validates one root: absolute, existing, directory.
func resolveLSPRoot(given string) (string, *cli.RefusalError) {
	abs, err := filepath.Abs(given)
	if err != nil {
		return "", &cli.RefusalError{Code: codeLSPBadRoot, Message: "lsp: --root " + given + " is not addressable: " + err.Error()}
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", &cli.RefusalError{Code: codeLSPBadRoot, Message: "lsp: --root " + abs + " does not exist or is unreadable"}
	}
	if !info.IsDir() {
		return "", &cli.RefusalError{Code: codeLSPBadRoot, Message: "lsp: --root " + abs + " is not a directory"}
	}
	return abs, nil
}
