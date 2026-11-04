// Package mcpconfig renders one host's MCP server snippet and merges it into
// that host's config file (Claude/Cursor JSON, Codex TOML). The merge is
// deliberately minimal: ordered JSON via internal/jq and a plain
// [mcp_servers.verdict] table for TOML, never a full parser. Anything it
// cannot merge safely refuses typed instead of rewriting the operator's file.
// It never prompts, writes stdout, or starts the server.
package mcpconfig

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// Supported agents, formats, merge statuses, and refusal codes.
const (
	AgentClaude = "claude"
	AgentCursor = "cursor"
	AgentCodex  = "codex"

	FormatJSON = "json"
	FormatTOML = "toml"

	StatusCreate  = "create"
	StatusUpdate  = "update"
	StatusKeep    = "keep"
	StatusPreview = "preview"

	CodeUnknownAgent = "mcp-config-unknown-agent"
	CodeNoHome       = "mcp-config-no-home"
	CodeUnparseable  = "mcp-config-unparseable"
	CodeForeign      = "mcp-config-foreign"
	CodeUnmergeable  = "mcp-config-unmergeable"
	CodeRead         = "mcp-config-read-failed"
	CodeWrite        = "mcp-config-write-failed"
	CodeBackup       = "mcp-config-backup-failed"

	// BackupSuffix names the copy taken before a changing write.
	BackupSuffix = ".verdict.bak"

	serverName     = "verdict"
	command        = "verdict"
	mcpArg         = "mcp"
	configPerm     = 0o600
	dirPerm        = 0o755
	tomlName       = "mcp_servers.verdict"
	jsonServersKey = "mcpServers"
	keyCommand     = "command"
	keyArgs        = "args"
	newline        = "\n"
	openBracket    = "["
	closeBracket   = "]"
	tripleSingle   = "'''"
	tripleDouble   = `"""`
)

// Request is one `mcp config` invocation. Cwd is the project root Claude and
// Cursor paths resolve against; Env supplies HOME and CODEX_HOME.
//
//nolint:govet // field order documents the invocation surface; alignment is irrelevant
type Request struct {
	Agent  string
	Write  bool
	DryRun bool
	Cwd    string
	Env    map[string]string
}

// Result is the snippet to print plus the merge outcome.
type Result struct {
	Agent   string
	Path    string
	Format  string
	Snippet string
	Status  string
	Backup  string
	Changed bool
}

// host is one resolved config location.
type host struct {
	path   string
	format string
}

// Run resolves the host, renders its snippet, and (unless this is a preview)
// merges and writes the config.
func Run(req *Request) (Result, *core.RefusalError) {
	where, ref := resolve(req)
	if ref != nil {
		return Result{}, ref
	}
	res := Result{
		Agent: req.Agent, Path: where.path, Format: where.format,
		Snippet: snippet(where.format), Status: StatusPreview,
	}
	if !req.Write || req.DryRun {
		return res, nil
	}
	old, existed, mode, ref := readConfig(where.path)
	if ref != nil {
		return Result{}, ref
	}
	merged, changed, ref := merge(where.format, old, where.path)
	if ref != nil {
		return Result{}, ref
	}
	if !changed {
		res.Status = StatusKeep
		return res, nil
	}
	if ref := apply(where.path, old, merged, existed, mode, &res); ref != nil {
		return Result{}, ref
	}
	return res, nil
}

// apply backs up the old file (when it had content) and atomically writes the
// merged config.
func apply(path string, old, merged []byte, existed bool, mode os.FileMode, res *Result) *core.RefusalError {
	if existed && len(bytes.TrimSpace(old)) > 0 {
		backup := path + BackupSuffix
		if err := atomic.WriteFileMode(backup, old, configPerm); err != nil {
			return refuse(CodeBackup, fmt.Sprintf("cannot back up %s: %v", path, err))
		}
		res.Backup = backup
	}
	if err := os.MkdirAll(filepath.Dir(path), dirPerm); err != nil {
		return refuse(CodeWrite, fmt.Sprintf("cannot create %s: %v", filepath.Dir(path), err))
	}
	if err := atomic.WriteFileMode(path, merged, mode); err != nil {
		return refuse(CodeWrite, fmt.Sprintf("cannot write %s: %v", path, err))
	}
	res.Changed = true
	res.Status = StatusUpdate
	if !existed {
		res.Status = StatusCreate
	}
	return nil
}

// resolve maps the agent name to its documented config path.
func resolve(req *Request) (host, *core.RefusalError) {
	if req == nil {
		return host{}, refuse(CodeUnknownAgent, "mcp config needs an agent")
	}
	switch req.Agent {
	case AgentClaude:
		return host{filepath.Join(req.Cwd, ".mcp.json"), FormatJSON}, nil
	case AgentCursor:
		return host{filepath.Join(req.Cwd, ".cursor", "mcp.json"), FormatJSON}, nil
	case AgentCodex:
		base := req.Env["CODEX_HOME"]
		if base == "" {
			home := req.Env["HOME"]
			if home == "" {
				return host{}, refuse(CodeNoHome, "codex config needs HOME or CODEX_HOME; set one and re-run")
			}
			base = filepath.Join(home, ".codex")
		}
		return host{filepath.Join(base, "config.toml"), FormatTOML}, nil
	default:
		return host{}, refuse(CodeUnknownAgent,
			fmt.Sprintf("unknown agent %q (want claude, cursor, or codex)", req.Agent))
	}
}

// snippet renders the exact entry the host needs.
func snippet(format string) string {
	if format == FormatTOML {
		return tomlTable()
	}
	return jq.NewObj(jsonServersKey, jq.NewObj(serverName, entry())).MarshalPretty() + newline
}

// tomlTable renders the canonical TOML table block.
func tomlTable() string {
	return strings.Join([]string{
		openBracket + tomlName + closeBracket,
		keyCommand + ` = "` + command + `"`,
		keyArgs + ` = ["` + mcpArg + `"]`,
	}, newline) + newline
}

// entry is the JSON server object.
func entry() *jq.Obj { return jq.NewObj(keyCommand, command, keyArgs, []any{mcpArg}) }

// readConfig reads the existing file, treating absence as an empty new file.
func readConfig(path string) ([]byte, bool, os.FileMode, *core.RefusalError) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, configPerm, nil
	}
	if err != nil {
		return nil, false, 0, refuse(CodeRead, fmt.Sprintf("cannot inspect %s: %v", path, err))
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, false, 0, refuse(CodeWrite, path+" is a symlink; refusing to replace it")
	}
	if info.IsDir() {
		return nil, false, 0, refuse(CodeRead, path+" is a directory")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, false, 0, refuse(CodeRead, fmt.Sprintf("cannot read %s: %v", path, err))
	}
	return raw, true, info.Mode().Perm(), nil
}

// merge dispatches on the host format and reports whether bytes changed.
func merge(format string, old []byte, path string) ([]byte, bool, *core.RefusalError) {
	if format == FormatTOML {
		return mergeTOML(old, path)
	}
	return mergeJSON(old, path)
}

// mergeJSON upserts mcpServers.verdict without dropping unknown keys. A
// different existing command/argv is foreign, never silently rewritten.
//
//nolint:revive // the ordered upsert reads as one block
func mergeJSON(old []byte, path string) ([]byte, bool, *core.RefusalError) {
	root := jq.NewObj()
	if len(bytes.TrimSpace(old)) > 0 {
		value, err := jq.DecodeOrdered(old)
		if err != nil {
			return nil, false, refuse(CodeUnparseable, fmt.Sprintf("cannot parse %s: %v", path, err))
		}
		decoded, ok := value.(*jq.Obj)
		if !ok {
			return nil, false, refuse(CodeUnparseable, path+" is not a JSON object")
		}
		root = decoded
	}
	servers := jq.NewObj()
	if value, has := root.Get(jsonServersKey); has {
		decoded, ok := value.(*jq.Obj)
		if !ok {
			return nil, false, refuse(CodeUnparseable, path+" has a non-object mcpServers")
		}
		servers = decoded
	} else {
		root.Set(jsonServersKey, servers)
	}
	if value, has := servers.Get(serverName); has {
		server, ok := value.(*jq.Obj)
		if !ok || !matches(server) {
			return nil, false, refuse(CodeForeign,
				path+" already configures mcpServers.verdict differently; refusing to rewrite it")
		}
		return nil, false, nil
	}
	servers.Set(serverName, entry())
	return []byte(root.MarshalPretty() + newline), true, nil
}

// matches proves the existing entry launches `verdict mcp`; extra keys stay.
func matches(server *jq.Obj) bool {
	cmd, ok := server.Get(keyCommand)
	if !ok || cmd != command {
		return false
	}
	value, ok := server.Get(keyArgs)
	if !ok {
		return false
	}
	args, ok := value.([]any)
	return ok && len(args) == 1 && args[0] == mcpArg
}

// mergeTOML appends the verdict table, or fills a missing command/args pair in
// an existing canonical table. Anything it cannot read confidently refuses.
func mergeTOML(old []byte, path string) ([]byte, bool, *core.RefusalError) {
	lines := strings.Split(string(old), newline)
	start, end, ref := findTable(lines, path)
	if ref != nil {
		return nil, false, ref
	}
	if start < 0 {
		return appendTable(string(old)), true, nil
	}
	hasCommand, hasArgs, ref := tableKeys(lines[start+1:end], path)
	if ref != nil {
		return nil, false, ref
	}
	if hasCommand && hasArgs {
		return nil, false, nil
	}
	if ref := multiLineRefusal(lines[:end], path); ref != nil {
		return nil, false, ref
	}
	return []byte(strings.Join(fillTable(lines, start, hasCommand, hasArgs), newline)), true, nil
}

// fillTable inserts the missing canonical keys directly under the table header,
// so the rest of the body keeps its order and comments.
func fillTable(lines []string, start int, hasCommand, hasArgs bool) []string {
	insert := make([]string, 0, len(lines)+2)
	insert = append(insert, lines[:start+1]...)
	if !hasCommand {
		insert = append(insert, keyCommand+` = "`+command+`"`)
	}
	if !hasArgs {
		insert = append(insert, keyArgs+` = ["`+mcpArg+`"]`)
	}
	return append(insert, lines[start+1:]...)
}

// findTable reports the canonical verdict table's line range, refusing any
// non-table spelling it cannot merge without a parser.
//
//nolint:gocognit,revive // one-pass table scan; splitting the header/body branches hides it
func findTable(lines []string, path string) (int, int, *core.RefusalError) {
	start := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(stripComment(line))
		if trimmed == "" {
			continue
		}
		parts, header := headerParts(trimmed)
		if header {
			if start >= 0 && i > start {
				return start, i, nil
			}
			if start < 0 && slices.Equal(parts, strings.Split(tomlName, ".")) {
				start = i
			}
			continue
		}
		if start < 0 && strings.Contains(trimmed, "mcp_servers") && strings.Contains(trimmed, "verdict") {
			return -1, -1, refuse(CodeUnmergeable, path+" defines "+tomlName+
				" outside a plain ["+tomlName+"] table; refusing to guess")
		}
	}
	if start >= 0 {
		return start, len(lines), nil
	}
	return -1, -1, nil
}

// tableKeys reads the canonical keys, refusing a value that differs or cannot
// be read simply.
//
//nolint:revive // the two canonical keys read clearer inline than behind helpers
func tableKeys(body []string, path string) (bool, bool, *core.RefusalError) {
	var hasCommand, hasArgs bool
	for _, line := range body {
		key, value, ok := tomlPair(line)
		if !ok {
			continue
		}
		switch key {
		case keyCommand:
			if strings.Trim(value, `"'`) != command {
				return false, false, foreignTable(path, keyCommand)
			}
			hasCommand = true
		case keyArgs:
			if compactArgs(value) != `["mcp"]` {
				return false, false, foreignTable(path, keyArgs)
			}
			hasArgs = true
		}
	}
	return hasCommand, hasArgs, nil
}

// multiLineRefusal explains why a file using multi-line strings cannot be
// merged: the scan cannot tell a header inside one from a real header.
func multiLineRefusal(lines []string, path string) *core.RefusalError {
	if !hasMultiLineString(lines) {
		return nil
	}
	return refuse(CodeUnmergeable, path+" uses a TOML multi-line string ("+
		tripleSingle+" or "+tripleDouble+") at or before ["+tomlName+
		"], so a table header cannot be told apart from text inside it; add the table by hand")
}

// hasMultiLineString reports whether any of these lines opens a TOML
// multi-line string. The scan is deliberately plain: it does not track quoting
// state, because a line-based merge cannot tell a header inside a literal from
// a real one, so the caller refuses instead of guessing.
func hasMultiLineString(lines []string) bool {
	for _, line := range lines {
		if strings.Contains(line, tripleSingle) || strings.Contains(line, tripleDouble) {
			return true
		}
	}
	return false
}

// tomlPair splits one `key = value` line, dropping any trailing comment.
func tomlPair(line string) (string, string, bool) {
	key, value, ok := strings.Cut(strings.TrimSpace(stripComment(line)), "=")
	if !ok {
		return "", "", false
	}
	return strings.TrimSpace(key), strings.TrimSpace(value), true
}

// compactArgs normalizes the simple argv array the snippet ships.
func compactArgs(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, " ", ""), "'", `"`)
}

// foreignTable refuses a table whose existing values differ.
func foreignTable(path, key string) *core.RefusalError {
	return refuse(CodeForeign, path+" already configures ["+tomlName+"]."+key+" differently; refusing to rewrite it")
}

// appendTable appends the canonical table, separating it from existing text.
func appendTable(text string) []byte {
	trimmed := strings.TrimRight(text, newline)
	if trimmed != "" {
		trimmed += newline + newline
	}
	return []byte(trimmed + tomlTable())
}

// headerParts parses a table header into dotted key parts. Inline comments must
// already be stripped.
func headerParts(line string) ([]string, bool) {
	if !strings.HasPrefix(line, openBracket) || strings.HasPrefix(line, "[[") || !strings.HasSuffix(line, closeBracket) {
		return nil, false
	}
	inner := strings.TrimSpace(line[1 : len(line)-1])
	if inner == "" {
		return nil, true
	}
	parts := strings.Split(inner, ".")
	for i, part := range parts {
		unquoted := strings.Trim(strings.TrimSpace(part), `"'`)
		if unquoted == "" {
			return nil, false
		}
		parts[i] = unquoted
	}
	return parts, true
}

// stripComment removes a trailing # comment. Config-table strings are simple
// enough that a plain cut is safe, and an exotic value refuses when unreadable.
func stripComment(line string) string {
	before, _, found := strings.Cut(line, "#")
	if found {
		return before
	}
	return line
}

// refuse builds one typed refusal.
func refuse(code, message string) *core.RefusalError {
	return &core.RefusalError{Code: code, Message: message}
}
