package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/mcp"
)

const mcpPostureConflict = "--read-only and --operator are mutually exclusive"

func mcpUsageText() string {
	return "usage: verdict mcp [--read-only | --operator]\n" +
		"       verdict mcp config --agent <claude|cursor|codex|gemini|qwen|windsurf|opencode|goose|copilot>\n" +
		"       [--write] [--dry-run] [--json] [--read-only | --operator]\n" +
		"  Serve the review verbs as MCP tools over stdio (JSON-RPC 2.0, one frame per line).\n" +
		"  stdout is the protocol channel; progress and warnings go to stderr.\n" +
		"  Protocol: initialize, notifications/initialized, tools/list, tools/call, ping,\n" +
		"  notifications/cancelled. The process exits when stdin closes: no daemon, no HTTP.\n" + //nolint:misspell // MCP method name
		"  --read-only  expose read tools plus record dry_run (report, next, show, trend,\n" +
		"               index, ready, doctor, check-tree, config, record)\n" +
		"  --operator   also expose the human-owned `triage` tool; mutually exclusive with --read-only\n" +
		"\n" +
		"  Default tools (20): adopt, calibrate, check-tree, config, doctor, fetch, fetch-local, fix,\n" +
		"  index, init, learn, next, post, ready, record, report, show, threads, trend, verify.\n" +
		"  `triage` joins\n" +
		"  them only with --operator: status flips are human-owned, and agents propose `triage[]`\n" +
		"  in review.json instead.\n" +
		"  Each tool takes the core request shape: repo/pr plus verb fields; schemas are generated\n" +
		"  from those structs. tools/list adds a title and the four MCP behavior hints; initialize\n" +
		"  returns the review doctrine in `instructions`. Success data matches the CLI --json\n" +
		"  envelope's data; typed refusals return isError=true with code/message/hint.\n" +
		"\n" +
		"  mcp config prints the exact host snippet for --read-only/--operator too; --write merges it idempotently into\n" +
		"  .mcp.json (claude), .cursor/mcp.json (cursor), $CODEX_HOME/config.toml (codex),\n" +
		"  ~/.gemini/settings.json (gemini), ~/.qwen/settings.json (qwen),\n" +
		"  ~/.codeium/windsurf/mcp_config.json (windsurf), backing up the old file\n" +
		"  and refusing unparseable or foreign configs. opencode.json (opencode),\n" +
		"  .vscode/mcp.json (copilot), and ~/.config/goose/config.yaml (goose) are\n" +
		"  print-only. It never prompts and never starts the server. --dry-run is the default (print only).\n"
}

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
		fmt.Fprintln(stderr, mcpPostureConflict)
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
	ctx, stop := shutdownSignalContext()
	defer stop()
	err := server.Serve(ctx, os.Stdin, stdout)
	if ctx.Err() != nil {
		return cli.ExitBroke
	}
	if err != nil {
		fmt.Fprintf(stderr, "mcp: %v\n", err)
		return cli.ExitRefused
	}
	return cli.ExitOK
}
