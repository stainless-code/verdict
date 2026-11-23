package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/stainless-code/verdict/internal/acp"
	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core/query"
	"github.com/stainless-code/verdict/internal/readcommon"
)

const (
	flagACPRoot       = "--root"
	flagACPCheck      = "--check"
	flagACPMaxBundles = "--max-bundles"
	flagACPJSON       = "--json"
	defaultACPCheck   = "critical"
	codeACPBRoot      = "bad-root"
)

// acpUsageText is `verdict acp --help`: the stdio transport contract, its
// flags, and the ACP method surface an editor counts on.
func acpUsageText() string {
	return "usage: verdict acp [--root <dir>] [--check <tier>] [--max-bundles <n>] [--json]\n" +
		"  Serve the read-only verdict queries as ACP v1 over stdio (JSON-RPC 2.0, one frame per line).\n" +
		"  stdout is the protocol channel; progress and warnings go to stderr.\n" +
		"  Protocol: initialize (protocolVersion 1), ping, verdict/bundles/list,\n" +
		"  verdict/findings/list, verdict/findings/get, verdict/findings/copy,\n" +
		"  $/cancel_request. The process exits when stdin closes: no daemon, no HTTP,\n" +
		"  no model calls, no writes.\n" +
		"  --root <dir>  serve the bundles under <dir> (default: the current repository)\n" +
		"  --check <tier>  gate tier a findings detail composes (critical|important|informational)\n" +
		"  --max-bundles <n>  listing cap (default 500)\n" +
		"  --json  accepted for CLI-convention parity; stdio is JSON-RPC either way\n" +
		"  Methods: initialize negotiates protocolVersion 1 and advertises read-only\n" +
		"  capabilities; verdict/bundles/list {pattern?} lists bundles; findings/list\n" +
		"  {bundle} lists findings with evidence rungs through Detail; findings/get\n" +
		"  {bundle, sig} reads one finding through Show; findings/copy {bundle, sig}\n" +
		"  returns the deterministic Prompt for AI agent plus the runnable\n" +
		"  verdict triage command through Copy. Missing bundles, unknown sigs, and\n" +
		"  traversal names refuse as JSON-RPC errors naming the refusal code."
}

// runACP executes `verdict acp [flags]`: parse the transport flags, resolve
// the served root, and speak ACP until stdin closes. Exit 0 on clean EOF, 3
// when the root context was canceled, 2 for argv shape, 1 for a start refusal.
func runACP(ctx context.Context, r *cli.Runner, args []string, version string) int {
	opts, code, done := parseACPFlags(r, args)
	if done {
		return code
	}
	root, ref := resolveACPRoot(ctx, opts.root)
	if ref != nil {
		return r.Refuse(ref)
	}
	server := acp.New(&acp.Config{
		Version:    version,
		Root:       root,
		Now:        r.Now,
		Stderr:     r.Diag,
		Check:      opts.check,
		MaxBundles: opts.maxBundles,
	})
	err := server.ServeInterruptible(ctx, closableReader(os.Stdin), os.Stdout)
	if ctx.Err() != nil {
		return cli.ExitBroke
	}
	if err != nil {
		fmt.Fprintf(r.Diag, "acp: %v\n", err)
		return cli.ExitRefused
	}
	return cli.ExitOK
}

type acpOptions struct {
	root       string
	check      string
	maxBundles int
}

// parseACPFlags reads the transport flags left to right.
func parseACPFlags(r *cli.Runner, args []string) (acpOptions, int, bool) {
	opts := acpOptions{check: defaultACPCheck, maxBundles: query.DefaultMaxBundles}
	for i := 0; i < len(args); i++ {
		next, code, done := parseACPFlag(r, &opts, args, i)
		if done {
			return opts, code, true
		}
		i = next
	}
	return opts, 0, false
}

func parseACPFlag(r *cli.Runner, opts *acpOptions, args []string, i int) (int, int, bool) {
	name := args[i]
	switch name {
	case flagShortHelp, flagHelp:
		fmt.Fprint(r.Out, acpUsageText())
		return i, cli.ExitOK, true
	case flagACPJSON:
		return i, 0, false
	case flagACPRoot, flagACPCheck, flagACPMaxBundles:
		var want string
		switch name {
		case flagACPCheck:
			want = "critical|important|informational"
		case flagACPMaxBundles:
			want = "<n>"
		default:
			want = "<dir>"
		}
		value, next, uerr := cli.FlagValue(args, i, name, want)
		if uerr != nil {
			return i, r.ReportUsage(uerr), true
		}
		code, done := applyACPFlag(r, opts, name, value)
		return next, code, done
	default:
		return i, r.ReportUsage(&cli.UsageError{Message: fmt.Sprintf(
			"unknown flag '%s' for acp (want --root, --check, --max-bundles, --json)", name)}), true
	}
}

func applyACPFlag(r *cli.Runner, opts *acpOptions, name, value string) (int, bool) {
	switch name {
	case flagACPRoot:
		opts.root = value
	case flagACPCheck:
		return applyACPCheck(r, opts, value)
	case flagACPMaxBundles:
		return applyACPMaxBundles(r, opts, value)
	}
	return 0, false
}

func applyACPCheck(r *cli.Runner, opts *acpOptions, value string) (int, bool) {
	switch value {
	case "critical", "important", "informational":
		opts.check = value
	default:
		return r.ReportUsage(&cli.UsageError{Message: "acp: --check " + value + " wants critical|important|informational"}), true
	}
	return 0, false
}

func applyACPMaxBundles(r *cli.Runner, opts *acpOptions, value string) (int, bool) {
	n, err := strconv.Atoi(value)
	if err != nil || n < 1 {
		return r.ReportUsage(&cli.UsageError{Message: "acp: --max-bundles " + value + " is not a positive count"}), true
	}
	opts.maxBundles = n
	return 0, false
}

// resolveACPRoot resolves the served root: --root first (clean, absolute,
// existing directory), then the repository probe every read verb shares.
func resolveACPRoot(ctx context.Context, given string) (string, *cli.RefusalError) {
	if given != "" {
		return resolveGivenACPRoot(given)
	}
	root, err := readcommon.RepoRoot(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return "", &cli.RefusalError{Code: cli.CodeCanceled, Message: "canceled before acp completed"}
		}
		return "", &cli.RefusalError{Code: "not-a-repo", Message: err.Error()}
	}
	abs, aerr := filepath.Abs(root)
	if aerr != nil {
		return "", &cli.RefusalError{Code: codeACPBRoot, Message: "acp: repository root " + root + " is not addressable: " + aerr.Error()}
	}
	return abs, nil
}

// resolveGivenACPRoot validates an explicit --root: absolute, existing,
// directory.
func resolveGivenACPRoot(given string) (string, *cli.RefusalError) {
	abs, err := filepath.Abs(given)
	if err != nil {
		return "", &cli.RefusalError{Code: codeACPBRoot, Message: "acp: --root " + given + " is not addressable: " + err.Error()}
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", &cli.RefusalError{Code: codeACPBRoot, Message: "acp: --root " + abs + " does not exist or is unreadable"}
	}
	if !info.IsDir() {
		return "", &cli.RefusalError{Code: codeACPBRoot, Message: "acp: --root " + abs + " is not a directory"}
	}
	return abs, nil
}
