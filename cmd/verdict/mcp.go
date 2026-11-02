package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/mcp"
)

// mcpUsageText documents the hidden stdio transport. `mcp` stays out of the
// review usage block so ticket 08's usage bytes and evals do not move.
func mcpUsageText() string {
	return "usage: verdict mcp [--read-only]\n" +
		"  Serve the review verbs as MCP tools over stdio (JSON-RPC 2.0, one frame per line).\n" +
		"  stdout is the protocol channel; progress and warnings go to stderr.\n" +
		"  Protocol: initialize, notifications/initialized, tools/list, tools/call, ping,\n" +
		"  notifications/cancelled. The process exits when stdin closes: no daemon, no HTTP.\n" + //nolint:misspell // MCP method name
		"  --read-only  expose only non-writing tools (report, next, show, trend, ready, doctor, check-tree)\n" +
		"\n" +
		"  Tools: adopt, calibrate, check-tree, doctor, fetch, fetch-local, fix, init, learn,\n" +
		"  next, ready, record, report, show, trend, triage, verify (post arrives with ticket 17b).\n" +
		"  Each tool takes the core request shape: repo/pr plus verb fields; schemas are generated\n" +
		"  from those structs. Success data matches the CLI --json envelope's data; typed refusals\n" +
		"  return isError=true with code/message/hint.\n" +
		"\n" +
		"  claude/cursor config: {\"mcpServers\":{\"verdict\":{\"command\":\"verdict\",\"args\":[\"mcp\"]}}}\n"
}

// runMCP starts the stdio MCP server. It is the only caller of Serve in the
// binary; the transport owns no CLI state beyond resolved prefs.
func runMCP(
	args []string, stdout, stderr *os.File,
	getenv func(string) string, readFile func(string) ([]byte, error),
) int {
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
