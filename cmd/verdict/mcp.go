package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/mcp"
)

// mcpUsageText documents the stdio transport. The top-level usage block now
// lists the experimental entry points; this remains the transport's full
// flag reference.
func mcpUsageText() string {
	return "usage: verdict mcp [--read-only | --operator]\n" +
		"       verdict mcp config --agent <claude|cursor|codex> [--write] [--dry-run] [--json]\n" +
		"  Serve the review verbs as MCP tools over stdio (JSON-RPC 2.0, one frame per line).\n" +
		"  stdout is the protocol channel; progress and warnings go to stderr.\n" +
		"  Protocol: initialize, notifications/initialized, tools/list, tools/call, ping,\n" +
		"  notifications/cancelled. The process exits when stdin closes: no daemon, no HTTP.\n" + //nolint:misspell // MCP method name
		"  --read-only  expose only non-writing tools (report, next, show, trend, ready, doctor, check-tree)\n" +
		"  --operator   also expose the human-owned `triage` tool; mutually exclusive with --read-only\n" +
		"\n" +
		"  Default tools (17): adopt, calibrate, check-tree, doctor, fetch, fetch-local, fix, init,\n" +
		"  learn, next, post, ready, record, report, show, trend, verify. `triage` joins them only\n" +
		"  with --operator: status flips are human-owned, and agents propose `triage[]` in\n" +
		"  review.json instead.\n" +
		"  Each tool takes the core request shape: repo/pr plus verb fields; schemas are generated\n" +
		"  from those structs. tools/list adds a title and the four MCP behavior hints; initialize\n" +
		"  returns the review doctrine in `instructions`. Success data matches the CLI --json\n" +
		"  envelope's data; typed refusals return isError=true with code/message/hint.\n" +
		"\n" +
		"  mcp config prints the exact host snippet; --write merges it idempotently into\n" +
		"  .mcp.json (claude), .cursor/mcp.json (cursor), or $CODEX_HOME/config.toml (codex),\n" +
		"  backing up the old file and refusing unparseable or foreign configs. It never\n" +
		"  prompts and never starts the server. --dry-run is the default (print only).\n"
}

// runMCP starts the stdio MCP server. It is the only caller of Serve in the
// binary; the transport owns no CLI state beyond resolved prefs.
//
//nolint:revive,cyclop // the hidden transport dispatch and its flag loop read as one entry point
func runMCP(
	args []string, stdout, stderr *os.File,
	getenv func(string) string, readFile func(string) ([]byte, error),
) int {
	if len(args) > 0 && args[0] == "config" {
		return runMCPConfig(args[1:], stdout, stderr, getenv)
	}
	readOnly := false
	operator := false
	for _, arg := range args {
		switch arg {
		case "-h", "--help":
			fmt.Fprint(stdout, mcpUsageText())
			return cli.ExitOK
		case "--read-only":
			readOnly = true
		case "--operator":
			operator = true
		default:
			fmt.Fprintf(stderr, "unknown flag '%s' for mcp (want --read-only or --operator)\n", arg)
			return cli.ExitUsage
		}
	}
	if readOnly && operator {
		fmt.Fprintln(stderr, "--read-only and --operator are mutually exclusive")
		return cli.ExitUsage
	}
	root := exeRoot()
	env := envMap(getenv)
	runner := newRunner("mcp", "", "", stdout, stderr, getenv)
	if code, done := resolveRunnerPrefs(runner, env, readFile); done {
		return code
	}
	version := strings.TrimPrefix(version, "v")
	if version == "" {
		version = versionOf(root, readFile)
	}
	server := mcp.New(&mcp.Config{
		Version:   version,
		Env:       env,
		Environ:   os.Environ(),
		Now:       runner.Now,
		Effective: runner.Effective,
		Warnings:  append([]string{}, runner.Warnings...),
		Root:      root,
		ReadOnly:  readOnly,
		Operator:  operator,
		Stderr:    stderr,
	})
	// A shutdown signal must reach in-flight tool calls the same way it reaches
	// a verb: their child process trees (a running `verify`) are killed, and the
	// server returns once the handlers do. Stdin EOF keeps its own semantics.
	ctx, stop := shutdownSignalContext()
	defer stop()
	err := server.Serve(ctx, os.Stdin, stdout)
	if ctx.Err() != nil {
		// Shutting down: a transport error here (a closed stdin, a hosted
		// stdout pipe) is the symptom of the signal, not a failure to report.
		return cli.ExitBroke
	}
	if err != nil {
		fmt.Fprintf(stderr, "mcp: %v\n", err)
		return cli.ExitRefused
	}
	return cli.ExitOK
}
