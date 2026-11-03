package main

import (
	"context"
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
	return "usage: verdict mcp [--read-only]\n" +
		"       verdict mcp config --agent <claude|cursor|codex> [--write] [--dry-run] [--json]\n" +
		"  Serve the review verbs as MCP tools over stdio (JSON-RPC 2.0, one frame per line).\n" +
		"  stdout is the protocol channel; progress and warnings go to stderr.\n" +
		"  Protocol: initialize, notifications/initialized, tools/list, tools/call, ping,\n" +
		"  notifications/cancelled. The process exits when stdin closes: no daemon, no HTTP.\n" + //nolint:misspell // MCP method name
		"  --read-only  expose only non-writing tools (report, next, show, trend, ready, doctor, check-tree)\n" +
		"\n" +
		"  Tools: adopt, calibrate, check-tree, doctor, fetch, fetch-local, fix, init, learn,\n" +
		"  next, post, ready, record, report, show, trend, triage, verify.\n" +
		"  Each tool takes the core request shape: repo/pr plus verb fields; schemas are generated\n" +
		"  from those structs. Success data matches the CLI --json envelope's data; typed refusals\n" +
		"  return isError=true with code/message/hint.\n" +
		"\n" +
		"  mcp config prints the exact host snippet; --write merges it idempotently into\n" +
		"  .mcp.json (claude), .cursor/mcp.json (cursor), or $CODEX_HOME/config.toml (codex),\n" +
		"  backing up the old file and refusing unparseable or foreign configs. It never\n" +
		"  prompts and never starts the server. --dry-run is the default (print only).\n"
}

// runMCP starts the stdio MCP server. It is the only caller of Serve in the
// binary; the transport owns no CLI state beyond resolved prefs.
//
//nolint:revive // the hidden transport dispatch and its flag loop read as one entry point
func runMCP(
	args []string, stdout, stderr *os.File,
	getenv func(string) string, readFile func(string) ([]byte, error),
) int {
	if len(args) > 0 && args[0] == "config" {
		return runMCPConfig(args[1:], stdout, stderr, getenv)
	}
	readOnly := false
	for _, arg := range args {
		switch arg {
		case "-h", "--help":
			fmt.Fprint(stdout, mcpUsageText())
			return cli.ExitOK
		case "--read-only":
			readOnly = true
		default:
			fmt.Fprintf(stderr, "unknown flag '%s' for mcp (want --read-only)\n", arg)
			return cli.ExitUsage
		}
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
		Stderr:    stderr,
	})
	if err := server.Serve(context.Background(), os.Stdin, stdout); err != nil {
		fmt.Fprintf(stderr, "mcp: %v\n", err)
		return cli.ExitRefused
	}
	return cli.ExitOK
}
