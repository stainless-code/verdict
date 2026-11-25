package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/mcp"
	"github.com/stainless-code/verdict/internal/posture"
)

func mcpUsageText() string {
	return "usage: verdict mcp [--read-only]\n" +
		"       verdict mcp config --agent <claude|cursor|codex|gemini|qwen|windsurf|opencode|goose|copilot>\n" +
		"       [--write] [--dry-run] [--json] [--read-only]\n" +
		"  Serve the review verbs as MCP tools over stdio (JSON-RPC 2.0, one frame per line).\n" +
		"  stdout is the protocol channel; progress and warnings go to stderr.\n" +
		"  Protocol: initialize, notifications/initialized, tools/list, tools/call, ping,\n" +
		"  notifications/cancelled. The process exits when stdin closes: no daemon, no HTTP.\n" + //nolint:misspell // MCP method name
		"  --read-only  the one write-posture flag (shared with serve and lsp): expose read tools plus\n" +
		"               record dry_run (report, next, show, risk, handoff, journal, trend, index, pr_wait,\n" +
		"               ready, doctor, check-tree, config, pack, record, query-bundles, query-copy,\n" +
		"               query-cost, query-finding, query-findings, query-health); every local write and\n" +
		"               post is refused\n" +
		"\n" +
		"  Default tools (33): adopt, calibrate, check-tree, config, doctor, fetch, fetch-local, fix,\n" +
		"  handoff, index, ingest, init, journal, learn, next, pack, post, pr_wait, query-bundles, query-copy,\n" +
		"  query-cost, query-finding, query-findings, query-health, ready, record, report, risk, show, threads,\n" +
		"  trend, triage, verify.\n" +
		"  `triage` is human-owned: agents propose `triage[]` in review.json instead of calling it.\n" +
		"  Each tool takes the core request shape: repo/pr plus verb fields; schemas are generated\n" +
		"  from those structs. tools/list adds a title and the four MCP behavior hints; initialize\n" +
		"  returns the review doctrine in `instructions`. Success data matches the CLI --json\n" +
		"  envelope's data; typed refusals return isError=true with code/message/hint.\n" +
		"\n" +
		"  mcp config prints the exact host snippet for --read-only too; --write merges it idempotently into\n" +
		"  .mcp.json (claude), .cursor/mcp.json (cursor), $CODEX_HOME/config.toml (codex),\n" +
		"  ~/.gemini/settings.json (gemini), ~/.qwen/settings.json (qwen),\n" +
		"  ~/.codeium/windsurf/mcp_config.json (windsurf), backing up the old file\n" +
		"  and refusing unparseable or foreign configs. opencode.json (opencode),\n" +
		"  .vscode/mcp.json (copilot), and ~/.config/goose/config.yaml (goose) are\n" +
		"  print-only. It never prompts and never starts the server. --dry-run is the default (print only).\n"
}

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
		case posture.FlagReadOnly:
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
	ctx, stop := shutdownSignalContext()
	defer stop()
	err := server.ServeInterruptible(ctx, closableReader(os.Stdin), stdout)
	if ctx.Err() != nil {
		return cli.ExitBroke
	}
	if err != nil {
		fmt.Fprintf(stderr, "mcp: %v\n", err)
		return cli.ExitRefused
	}
	return cli.ExitOK
}

// closableReader exists because Close cannot interrupt a blocked terminal
// read. ServeInterruptible reads from the pipe, whose Close unblocks Read.
// The copier stays parked in r.Read and dies with the process.
func closableReader(r io.Reader) io.ReadCloser {
	pr, pw := io.Pipe()
	go func() {
		_, err := io.Copy(pw, r)
		_ = pw.CloseWithError(err)
	}()
	return pr
}
