// Package mcpconfig renders one host's MCP server snippet and merges it into
// that host's config file (Claude/Cursor JSON, Codex TOML). The merge is
// deliberately minimal: ordered JSON via internal/jq and a plain
// [mcp_servers.verdict] table for TOML, placed by a line scan that tracks TOML
// strings and comments, never a full parser. Anything it cannot merge safely
// refuses typed instead of rewriting the operator's file. It never prompts,
// writes stdout, or starts the server.
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
	argsValue      = `["mcp"]`
	newline        = "\n"
	openBracket    = "["
	closeBracket   = "]"
	tripleSingle   = "'''"
	tripleDouble   = `"""`
	doubleQuote    = `"`
	singleQuote    = "'"
	quotes         = doubleQuote + singleQuote
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

func snippet(format string) string {
	if format == FormatTOML {
		return tomlTable()
	}
	return jq.NewObj(jsonServersKey, jq.NewObj(serverName, entry())).MarshalPretty() + newline
}

func tomlTable() string {
	return strings.Join(tableLines(), newline) + newline
}

func entry() *jq.Obj { return jq.NewObj(keyCommand, command, keyArgs, []any{mcpArg}) }

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

func mergeTOML(old []byte, path string) ([]byte, bool, *core.RefusalError) {
	doc := scanTOML(string(old))
	if ref := doc.unterminated(path); ref != nil {
		return nil, false, ref
	}
	start, end := doc.verdictTable()
	if start < 0 {
		return doc.appendNewTable(path)
	}
	return doc.completeTable(start, end, path)
}

// appendNewTable adds the whole table to a file that has none, unless a
// non-table spelling of the server is already there to define it.
func (d *tomlDoc) appendNewTable(path string) ([]byte, bool, *core.RefusalError) {
	if ref := d.spellingRefusal(path); ref != nil {
		return nil, false, ref
	}
	return d.appendTable(), true, nil
}

// completeTable fills the keys the table is missing, and treats a table that
// carries both as configured: the operator's spelling stays byte-identical.
func (d *tomlDoc) completeTable(start, end int, path string) ([]byte, bool, *core.RefusalError) {
	if ref := d.headerString(start, path); ref != nil {
		return nil, false, ref
	}
	hasCommand, hasArgs, ref := d.tableKeys(start, end, path)
	if ref != nil {
		return nil, false, ref
	}
	if hasCommand && hasArgs {
		return nil, false, nil
	}
	return d.fillTable(start, hasCommand, hasArgs), true, nil
}

// TOML's triple-quoted forms are the only strings that may span a newline, so a
// file that ends inside one has no line outside a string to append to; a file
// that ends inside a single-line quote is a syntax error either way.
func (d *tomlDoc) unterminated(path string) *core.RefusalError {
	if d.open.delim == "" {
		return nil
	}
	if d.open.multi {
		return refuse(CodeUnmergeable, path+" ends inside a TOML multi-line string ("+
			tripleDouble+" or "+tripleSingle+"), so no line outside a string is left for ["+
			tomlName+"]; close it and re-run")
	}
	return refuse(CodeUnmergeable, path+" has an unterminated TOML string; fix the quoting and re-run")
}

// A header line that leaves a string open puts every key added under it inside
// that string, where the next run would not read it, so the merge cannot fill
// the table in.
func (d *tomlDoc) headerString(start int, path string) *core.RefusalError {
	if !d.scan[start].open {
		return nil
	}
	return refuse(CodeUnmergeable, path+" opens a TOML string on the ["+tomlName+
		"] line, so a key added under it would land inside that string; close the string and re-run")
}

func (d *tomlDoc) verdictTable() (int, int) {
	want := strings.Split(tomlName, ".")
	start := slices.IndexFunc(d.scan, func(line tomlLine) bool {
		return line.isHeader && slices.Equal(line.parts, want)
	})
	if start < 0 {
		return -1, -1
	}
	next := slices.IndexFunc(d.scan[start+1:], func(line tomlLine) bool { return line.isHeader })
	if next < 0 {
		return start, len(d.scan)
	}
	return start, start + 1 + next
}

// A mention of the server inside a string or a comment is prose, not a
// definition, so only the code outside both can name a non-table spelling.
func (d *tomlDoc) spellingRefusal(path string) *core.RefusalError {
	for _, line := range d.scan {
		if line.isHeader {
			continue
		}
		if strings.Contains(line.code, "mcp_servers") && strings.Contains(line.code, "verdict") {
			return refuse(CodeUnmergeable, path+" defines "+tomlName+
				" outside a plain ["+tomlName+"] table; refusing to guess")
		}
	}
	return nil
}

func (d *tomlDoc) tableKeys(start, end int, path string) (bool, bool, *core.RefusalError) {
	var hasCommand, hasArgs bool
	for _, line := range d.scan[start+1 : end] {
		if !line.hasPair {
			continue
		}
		if ref := line.keyRefusal(path); ref != nil {
			return false, false, ref
		}
		hasCommand = hasCommand || line.key == keyCommand
		hasArgs = hasArgs || line.key == keyArgs
	}
	return hasCommand, hasArgs, nil
}

func (l *tomlLine) keyRefusal(path string) *core.RefusalError {
	switch l.key {
	case keyCommand:
		if strings.Trim(l.value, quotes) != command {
			return foreignTable(path, keyCommand)
		}
	case keyArgs:
		if compactArgs(l.value) != argsValue {
			return foreignTable(path, keyArgs)
		}
	}
	return nil
}

func (d *tomlDoc) fillTable(start int, hasCommand, hasArgs bool) []byte {
	lines := make([]string, 0, len(d.lines)+2)
	lines = append(lines, d.lines[:start+1]...)
	if !hasCommand {
		lines = append(lines, keyCommand+` = "`+command+`"`)
	}
	if !hasArgs {
		lines = append(lines, keyArgs+` = `+argsValue)
	}
	return d.render(append(lines, d.lines[start+1:]...))
}

func (d *tomlDoc) appendTable() []byte {
	lines := slices.Clone(d.lines)
	for len(lines) > 0 && blankLine(lines[len(lines)-1]) {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > 0 {
		lines = append(lines, "")
	}
	return d.render(append(lines, tableLines()...))
}

func (d *tomlDoc) render(lines []string) []byte {
	return []byte(strings.Join(lines, d.eol) + d.eol)
}

// TOML's four string forms: only the triple-quoted pair may span a newline, and
// only the double-quoted pair reads a backslash escape.
type tomlString struct {
	delim  string
	escape bool
	multi  bool
}

type tomlCursor struct {
	str     tomlString
	comment bool
}

// tomlLine is one physical line as the merge reads it. inString records a line
// that starts inside a string, where no header, key or comment can begin, and
// open one that ends inside a string, where no line the merge inserts is code.
type tomlLine struct {
	code     string
	key      string
	value    string
	parts    []string
	isHeader bool
	hasPair  bool
	inString bool
	open     bool
}

type tomlDoc struct {
	lines []string
	scan  []tomlLine
	eol   string
	open  tomlString
}

// scanTOML reads the file the way TOML does, one physical line at a time, and
// keeps the ending the file starts with so an added line never introduces a
// second style.
func scanTOML(text string) tomlDoc {
	doc := tomlDoc{eol: detectEOL(text)}
	if text == "" {
		return doc
	}
	lines := strings.Split(text, newline)
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	doc.lines = make([]string, len(lines))
	doc.scan = make([]tomlLine, len(lines))
	cur := tomlCursor{}
	for i, raw := range lines {
		doc.lines[i] = strings.TrimSuffix(raw, "\r")
		doc.scan[i], cur = scanTOMLLine(doc.lines[i], cur)
	}
	doc.open = cur.str
	return doc
}

func detectEOL(text string) string {
	if i := strings.IndexByte(text, '\n'); i > 0 && text[i-1] == '\r' {
		return "\r\n"
	}
	return newline
}

func scanTOMLLine(raw string, cur tomlCursor) (tomlLine, tomlCursor) {
	starts := cur.str.delim != ""
	cur.comment = false
	acc := tomlRaw{eq: -1, eqAt: -1, comment: len(raw), first: -1}
	for i := 0; i < len(raw); {
		next, action := tomlAdvance(raw, i, &cur)
		if action == tomlCode {
			acc.write(raw, i)
		}
		if action == tomlCommentStart {
			acc.comment = i
		}
		i = next
	}
	line := acc.line(raw, starts)
	line.open = cur.str.delim != ""
	return line, cur
}

type tomlAction int

const (
	tomlSkip tomlAction = iota
	tomlCode
	tomlCommentStart
)

type tomlRaw struct {
	code    strings.Builder
	eq      int
	eqAt    int
	comment int
	first   int
	last    int
}

func (r *tomlRaw) write(raw string, i int) {
	r.code.WriteByte(raw[i])
	if r.first < 0 {
		r.first = i
	}
	r.last = i
	if raw[i] == '=' && r.eq < 0 {
		r.eq, r.eqAt = r.code.Len()-1, i
	}
}

func (r *tomlRaw) line(raw string, inString bool) tomlLine {
	line := tomlLine{inString: inString, code: r.code.String()}
	if r.first >= 0 && !inString {
		line.parts, line.isHeader = headerParts(trimBlank(raw[r.first : r.last+1]))
	}
	if r.eq >= 0 && !inString {
		line.hasPair = true
		line.key = trimBlank(line.code[:r.eq])
		line.value = trimBlank(raw[r.eqAt+1 : r.comment])
	}
	return line
}

// tomlAdvance consumes the byte at raw[i]. TOML ends a string only at its own
// closing delimiter and starts a comment only on a `#` outside every string.
func tomlAdvance(raw string, i int, cur *tomlCursor) (int, tomlAction) {
	if cur.comment {
		return len(raw), tomlSkip
	}
	if cur.str.delim != "" {
		return tomlSkipString(raw, i, cur), tomlSkip
	}
	switch raw[i] {
	case '#':
		cur.comment = true
		return i + 1, tomlCommentStart
	case '"', '\'':
		cur.str, i = tomlOpen(raw, i)
		return i, tomlSkip
	}
	return i + 1, tomlCode
}

func tomlSkipString(raw string, i int, cur *tomlCursor) int {
	if cur.str.escape && raw[i] == '\\' && i+1 < len(raw) {
		return i + 2
	}
	if strings.HasPrefix(raw[i:], cur.str.delim) {
		width := len(cur.str.delim)
		cur.str = tomlString{}
		return i + width
	}
	return i + 1
}

func tomlOpen(raw string, i int) (tomlString, int) {
	switch {
	case strings.HasPrefix(raw[i:], tripleDouble):
		return tomlString{delim: tripleDouble, escape: true, multi: true}, i + len(tripleDouble)
	case strings.HasPrefix(raw[i:], tripleSingle):
		return tomlString{delim: tripleSingle, multi: true}, i + len(tripleSingle)
	case strings.HasPrefix(raw[i:], doubleQuote):
		return tomlString{delim: doubleQuote, escape: true}, i + len(doubleQuote)
	default:
		return tomlString{delim: singleQuote}, i + len(singleQuote)
	}
}

func tableLines() []string {
	return []string{
		openBracket + tomlName + closeBracket,
		keyCommand + ` = "` + command + `"`,
		keyArgs + ` = ` + argsValue,
	}
}

// blanks are the only whitespace TOML allows around a key, a value or a
// header; the merge trims these and never a byte it could not read, so a
// line of Unicode spaces stays where the operator put it.
const blanks = " \t"

func trimBlank(s string) string { return strings.Trim(s, blanks) }

func blankLine(line string) bool { return trimBlank(line) == "" }

func compactArgs(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, " ", ""), singleQuote, doubleQuote)
}

func foreignTable(path, key string) *core.RefusalError {
	return refuse(CodeForeign, path+" already configures ["+tomlName+"]."+key+" differently; refusing to rewrite it")
}

func headerParts(line string) ([]string, bool) {
	if !strings.HasPrefix(line, openBracket) || strings.HasPrefix(line, "[[") || !strings.HasSuffix(line, closeBracket) {
		return nil, false
	}
	inner := trimBlank(line[1 : len(line)-1])
	if inner == "" {
		return nil, true
	}
	parts := strings.Split(inner, ".")
	for i, part := range parts {
		unquoted := strings.Trim(trimBlank(part), quotes)
		if unquoted == "" {
			return nil, false
		}
		parts[i] = unquoted
	}
	return parts, true
}

func refuse(code, message string) *core.RefusalError {
	return &core.RefusalError{Code: code, Message: message}
}
