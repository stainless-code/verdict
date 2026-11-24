package lsp

import "encoding/json"

// LSP 3.17 method names this server answers.
const (
	methodInitialize     = "initialize"
	methodInitialized    = "initialized"
	methodShutdown       = "shutdown"
	methodExit           = "exit"
	methodCancel         = "$/cancelRequest"
	methodDidOpen        = "textDocument/didOpen"
	methodDidChange      = "textDocument/didChange"
	methodDidSave        = "textDocument/didSave"
	methodDidClose       = "textDocument/didClose"
	methodExecuteCommand = "workspace/executeCommand"
	methodDidChangeRoots = "workspace/didChangeWorkspaceFolders"
	methodPublish        = "textDocument/publishDiagnostics"
	methodLogMessage     = "window/logMessage"
	protocolPrefix       = "$/"
)

// CommandRefresh is the one executeCommand this server advertises.
const CommandRefresh = "verdict.refresh"

// JSON-RPC and LSP error codes.
const (
	rpcParseError       = -32700
	rpcInvalidRequest   = -32600
	rpcMethodNotFound   = -32601
	rpcInvalidParams    = -32602
	rpcInternalError    = -32603
	rpcNotInitialized   = -32002
	rpcRequestCancelled = -32800
	// rpcServerCancelled is LSP's ServerCancelled: the server dropped a
	// request it could not queue.
	rpcServerCancelled = -32802
)

// MessageType values for window/logMessage.
const (
	msgError   = 1
	msgWarning = 2
	msgInfo    = 3
)

// DiagnosticSeverity values.
const (
	severityError       = 1
	severityWarning     = 2
	severityInformation = 3
	severityHint        = 4
)

// textDocumentSyncFull is TextDocumentSyncKind.Full: a didChange carries the
// whole buffer.
const textDocumentSyncFull = 1

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct { //nolint:govet // field order mirrors the wire frame
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result"`
	Error   *rpcError       `json:"error,omitempty"`
}

// rpcErrorResponse omits result: JSON-RPC forbids both members at once.
type rpcErrorResponse struct { //nolint:govet // field order mirrors the wire frame
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Error   *rpcError       `json:"error"`
}

type rpcNotification struct { //nolint:govet // field order mirrors the wire frame
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

type rpcError struct { //nolint:govet // field order mirrors the wire frame
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type initializeParams struct {
	RootURI               *string           `json:"rootUri"`
	RootPath              *string           `json:"rootPath"`
	InitializationOptions json.RawMessage   `json:"initializationOptions"`
	WorkspaceFolders      []workspaceFolder `json:"workspaceFolders"`
}

type workspaceFolder struct {
	URI  string `json:"uri"`
	Name string `json:"name"`
}

type didChangeRootsParams struct {
	Event struct {
		Added   []workspaceFolder `json:"added"`
		Removed []workspaceFolder `json:"removed"`
	} `json:"event"`
}

// initOptions are the optional, non-security-relevant session preferences a
// client may send. The CLI flag wins over each.
type initOptions struct {
	Bundle       string `json:"bundle"`
	ShowDeferred bool   `json:"showDeferred"`
}

type initializeResult struct {
	Capabilities serverCapabilities `json:"capabilities"`
	ServerInfo   serverInfo         `json:"serverInfo"`
}

type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// serverCapabilities advertises exactly what this build implements: push
// diagnostics fed by open/save, and the refresh command. No hover, code
// action, completion, or pull diagnostics.
type serverCapabilities struct { //nolint:govet // field order mirrors the wire frame
	PositionEncoding       string                `json:"positionEncoding"`
	TextDocumentSync       textDocumentSync      `json:"textDocumentSync"`
	ExecuteCommandProvider executeCommandOptions `json:"executeCommandProvider"`
	Workspace              *workspaceOptions     `json:"workspace,omitempty"`
}

// workspaceOptions is advertised only when the client's folders are the
// served roots (no --root).
type workspaceOptions struct {
	WorkspaceFolders workspaceFoldersOptions `json:"workspaceFolders"`
}

type workspaceFoldersOptions struct {
	Supported           bool `json:"supported"`
	ChangeNotifications bool `json:"changeNotifications"`
}

type textDocumentSync struct { //nolint:govet // field order mirrors the wire frame
	OpenClose bool        `json:"openClose"`
	Change    int         `json:"change"`
	Save      saveOptions `json:"save"`
}

type saveOptions struct {
	IncludeText bool `json:"includeText"`
}

type executeCommandOptions struct {
	Commands []string `json:"commands"`
}

type textDocumentItem struct {
	URI     string `json:"uri"`
	Text    string `json:"text"`
	Version int    `json:"version"`
}

type versionedDocument struct {
	URI     string `json:"uri"`
	Version int    `json:"version"`
}

type didOpenParams struct {
	TextDocument textDocumentItem `json:"textDocument"`
}

type didChangeParams struct {
	ContentChanges []contentChange   `json:"contentChanges"`
	TextDocument   versionedDocument `json:"textDocument"`
}

type contentChange struct {
	Range *json.RawMessage `json:"range"`
	Text  string           `json:"text"`
}

type didSaveParams struct {
	Text         *string `json:"text"`
	TextDocument struct {
		Version *int   `json:"version"`
		URI     string `json:"uri"`
	} `json:"textDocument"`
}

type didCloseParams struct {
	TextDocument versionedDocument `json:"textDocument"`
}

type cancelParams struct {
	ID json.RawMessage `json:"id"`
}

type executeCommandParams struct {
	Command   string            `json:"command"`
	Arguments []json.RawMessage `json:"arguments"`
}

type position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type lspRange struct {
	Start position `json:"start"`
	End   position `json:"end"`
}

// Diagnostic is one published finding. Field order mirrors the wire frame.
type Diagnostic struct { //nolint:govet // field order mirrors the wire frame
	Range    lspRange       `json:"range"`
	Severity int            `json:"severity"`
	Code     string         `json:"code"`
	Source   string         `json:"source"`
	Message  string         `json:"message"`
	Data     diagnosticData `json:"data"`
}

// diagnosticData is the verdict-specific payload a client round-trips.
type diagnosticData struct { //nolint:govet // field order mirrors the wire frame
	Sig         string `json:"sig"`
	Bundle      string `json:"bundle"`
	Root        string `json:"root"`
	Status      string `json:"status"`
	Severity    string `json:"severity"`
	Rigor       string `json:"rigor,omitempty"`
	QuoteStatus string `json:"quote_status,omitempty"`
	Lens        string `json:"lens,omitempty"`
	Category    string `json:"category,omitempty"`
	Side        string `json:"side"`
	Anchor      string `json:"anchor"`
	Span        span   `json:"span"`
	HunkSHA256  string `json:"hunk_sha256,omitempty"`
	SpanSHA256  string `json:"span_sha256,omitempty"`
	SpanMatches *bool  `json:"span_matches,omitempty"`
	Suggestion  bool   `json:"suggestion"`
	Deferred    bool   `json:"deferred"`
	Truncated   bool   `json:"truncated,omitempty"`
}

type span struct {
	StartLine int `json:"start_line"`
	Line      int `json:"line"`
}

type publishParams struct {
	URI         string       `json:"uri"`
	Version     *int         `json:"version,omitempty"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

type logMessageParams struct {
	Message string `json:"message"`
	Type    int    `json:"type"`
}

// refreshResult answers verdict.refresh: one row per served root and the
// number of publishDiagnostics frames the refresh sent.
type refreshResult struct {
	At        string       `json:"at"`
	Roots     []refreshRow `json:"roots"`
	Published int          `json:"published"`
}

type refreshRow struct {
	Root      string `json:"root"`
	Bundle    string `json:"bundle"`
	Findings  int    `json:"findings"`
	Truncated bool   `json:"truncated"`
}

// errorData is the typed payload a refused request carries: the refusal code
// and the hint the CLI would print for it.
type errorData struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint"`
}
