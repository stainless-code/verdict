package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/mcpconfig"
)

// mcpConfigFlags is the parsed `mcp config` option set.
type mcpConfigFlags struct {
	agent string
	write bool
	dry   bool
	json  bool
}

// runMCPConfig prints one host's MCP snippet or merges it into that host's
// config. It never prompts and never starts the server.
func runMCPConfig(args []string, stdout, stderr *os.File, getenv func(string) string) int {
	flags, code, done := parseMCPConfigFlags(args, stdout, stderr)
	if done {
		return code
	}
	r := newRunner("mcp", "", "", stdout, stderr, getenv)
	r.JSONMode = flags.json
	cwd, err := os.Getwd()
	if err != nil {
		return r.Refuse(&cli.RefusalError{
			Code: "mcp-config-read-failed", Message: "cannot resolve the current directory: " + err.Error(),
		})
	}
	res, ref := mcpconfig.Run(&mcpconfig.Request{
		Agent: flags.agent, Write: flags.write, DryRun: flags.dry,
		Cwd: cwd, Env: envMap(getenv),
	})
	if ref != nil {
		return r.Refuse(ref)
	}
	if flags.json {
		writeMCPConfigEnvelope(r, &res, flags)
		return cli.ExitOK
	}
	if !flags.write || flags.dry {
		fmt.Fprint(r.Out, res.Snippet)
		return cli.ExitOK
	}
	fmt.Fprintf(r.Out, "mcp config: %s %s\n", res.Status, res.Path)
	if res.Backup != "" {
		fmt.Fprintf(r.Out, "mcp config: previous file at %s\n", res.Backup)
	}
	return cli.ExitOK
}

// parseMCPConfigFlags parses the config grammar.
//
//nolint:revive // the flag switch is the parser; splitting it hides the grammar
func parseMCPConfigFlags(args []string, stdout, stderr *os.File) (mcpConfigFlags, int, bool) {
	var f mcpConfigFlags
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; {
		case arg == "--agent":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "--agent needs a value (claude, cursor, or codex)")
				return f, cli.ExitUsage, true
			}
			i++
			f.agent = args[i]
		case strings.HasPrefix(arg, "--agent="):
			f.agent = strings.TrimPrefix(arg, "--agent=")
		case arg == "--write":
			f.write = true
		case arg == flagDryRun:
			f.dry = true
		case arg == flagJSON:
			f.json = true
		case arg == flagShortHelp || arg == flagHelp:
			fmt.Fprint(stdout, mcpConfigUsageText())
			return f, cli.ExitOK, true
		default:
			fmt.Fprintf(stderr, "unknown flag '%s' for mcp config (want --agent, --write, --dry-run, --json)\n", arg)
			return f, cli.ExitUsage, true
		}
	}
	if f.agent == "" {
		fmt.Fprintln(stderr, "mcp config needs --agent <claude|cursor|codex>")
		return f, cli.ExitUsage, true
	}
	return f, 0, false
}

// writeMCPConfigEnvelope prints exactly one envelope for a config call.
func writeMCPConfigEnvelope(r *cli.Runner, res *mcpconfig.Result, f mcpConfigFlags) {
	data := jq.NewObj(
		"agent", res.Agent,
		"path", res.Path,
		"format", res.Format,
		"snippet", res.Snippet,
		"status", res.Status,
		"write", f.write && !f.dry,
		"dry_run", !f.write || f.dry,
		"changed", res.Changed,
	)
	if res.Backup != "" {
		data.Set("backup", res.Backup)
	}
	env := jq.NewObj(
		"schema_version", "1",
		"verb", "mcp",
		"repo", "",
		"pr", nil,
		"at", cli.NowUTC(r.Now()),
		"next", "",
		"warnings", cliWarnings(r.Warnings),
		"data", data,
		"error", nil,
	)
	fmt.Fprintln(r.Out, env.MarshalPretty())
}

// mcpConfigUsageText is the `verdict mcp config` help block.
func mcpConfigUsageText() string {
	return "usage: verdict mcp config --agent <claude|cursor|codex> [--write] [--dry-run] [--json]\n" +
		"  Print the exact MCP server entry for the host (the default; --dry-run too).\n" +
		"  --write merges it idempotently into the host's config, backing the old file up\n" +
		"  first; an unparseable or foreign entry refuses typed instead of rewriting it.\n" +
		"  Paths: claude .mcp.json, cursor .cursor/mcp.json (project), codex\n" +
		"  $CODEX_HOME/config.toml else ~/.codex/config.toml (user).\n" +
		"  Never prompts, never starts the server. --json prints one envelope.\n"
}
