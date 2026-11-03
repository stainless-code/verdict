package readcommon

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Measured-usage harness sources and limits. The readers are deliberately
// local-only: they open session files the harness already wrote, never a
// network endpoint, and an unreadable or unrecognized file simply reads as
// no measurement (the budget gate then falls back to the self-reported
// record).
const (
	usageEnv             = "VERDICT_HARNESS_USAGE"
	harnessClaude        = "claude-code"
	harnessOpencode      = "opencode"
	maxUsageFileBytes    = 64 << 20
	maxTranscriptLine    = 8 << 20
	transcriptScanBuffer = 64 << 10
	jsonlSuffix          = ".jsonl"
	jsonSuffix           = ".json"
)

// HarnessUsage is one measured agent-harness session: who measured it, the
// session id, the window it covers, and the session's reported USD cost.
// USD is nil when the harness exposed tokens but no priced total — never
// invented, because the tool never prices tokens itself.
type HarnessUsage struct { //nolint:govet // scanning metadata; field order favors readability
	Source  string
	Session string
	Started time.Time
	Ended   time.Time
	USD     *float64
}

// ReadHarnessUsage reads measured session usage for cwd from local harness
// stores:
//
//   - Claude Code transcripts under $CLAUDE_CONFIG_DIR/projects/<slug>/ (or
//     ~/.claude/projects/<slug>/) for the current working directory. Each
//     *.jsonl transcript is one session; its last cost-state entry is the
//     priced total, falling back to summed per-request costs.
//   - Explicit session-usage files named by $VERDICT_HARNESS_USAGE (one path
//     or a path list joined by [filepath.ListSeparator]; a directory reads
//     its *.json and *.jsonl entries). Supported shapes are opencode's
//     `opencode export <session>` JSON and Claude Code JSONL transcripts, so
//     any harness with an export command can feed the gate without a new
//     adapter.
//
// Sessions from another directory are ignored. Missing or malformed files
// are skipped silently: absent history is normal, not a degraded run.
func ReadHarnessUsage(cwd string, env map[string]string) []HarnessUsage {
	root, err := filepath.Abs(cwd)
	if err != nil {
		root = filepath.Clean(cwd)
	}
	var out []HarnessUsage
	out = append(out, claudeProjectUsage(root, env)...)
	out = append(out, explicitUsage(root, env)...)
	return dedupeUsage(out)
}

// claudeProjectUsage reads the transcripts Claude Code already stores for
// this directory.
func claudeProjectUsage(cwd string, env map[string]string) []HarnessUsage {
	config, ok := claudeConfigDir(env)
	if !ok {
		return nil
	}
	files := claudeTranscriptFiles(filepath.Join(config, "projects", ClaudeProjectSlug(cwd)))
	var out []HarnessUsage
	for _, path := range files {
		if !readableUsageFile(path) {
			continue
		}
		if usage, ok := readClaudeTranscript(path, cwd); ok {
			out = append(out, usage)
		}
	}
	return out
}

// claudeConfigDir resolves the Claude Code config root from the environment.
func claudeConfigDir(env map[string]string) (string, bool) {
	if config := env["CLAUDE_CONFIG_DIR"]; config != "" {
		return config, true
	}
	home := env["HOME"]
	if home == "" {
		return "", false
	}
	return filepath.Join(home, ".claude"), true
}

// claudeTranscriptFiles lists a project's transcript paths in stable order.
func claudeTranscriptFiles(projectDir string) []string {
	entries, err := os.ReadDir(projectDir)
	if err != nil {
		return nil
	}
	var files []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), jsonlSuffix) {
			continue
		}
		files = append(files, filepath.Join(projectDir, entry.Name()))
	}
	slices.Sort(files)
	return files
}

// ClaudeProjectSlug encodes cwd the way Claude Code names project folders:
// every non-alphanumeric byte (except a dash) becomes a dash.
func ClaudeProjectSlug(cwd string) string {
	var slug strings.Builder
	slug.Grow(len(cwd))
	for i := range len(cwd) {
		c := cwd[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' {
			slug.WriteByte(c)
			continue
		}
		slug.WriteByte('-')
	}
	return slug.String()
}

// explicitUsage reads the session-usage files the operator names.
func explicitUsage(cwd string, env map[string]string) []HarnessUsage {
	spec := env[usageEnv]
	if spec == "" {
		return nil
	}
	var out []HarnessUsage
	for _, part := range filepath.SplitList(spec) {
		if part == "" {
			continue
		}
		out = append(out, usageFromPath(part, cwd)...)
	}
	return out
}

// usageFromPath reads one explicit file or a directory of usage files.
func usageFromPath(path, cwd string) []HarnessUsage {
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}
	if !info.IsDir() {
		if usage, ok := readUsageFile(path, cwd); ok {
			return []HarnessUsage{usage}
		}
		return nil
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil
	}
	return readUsageDir(path, cwd, entries)
}

// readUsageDir reads the usage files directly under dir.
func readUsageDir(dir, cwd string, entries []os.DirEntry) []HarnessUsage {
	var out []HarnessUsage
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || (!strings.HasSuffix(name, jsonSuffix) && !strings.HasSuffix(name, jsonlSuffix)) {
			continue
		}
		if usage, ok := readUsageFile(filepath.Join(dir, name), cwd); ok {
			out = append(out, usage)
		}
	}
	return out
}

// readUsageFile sniffs the supported session shapes: opencode's export JSON
// (one object with info+messages) or a Claude Code JSONL transcript.
func readUsageFile(path, cwd string) (HarnessUsage, bool) {
	if !readableUsageFile(path) {
		return HarnessUsage{}, false
	}
	if strings.HasSuffix(path, jsonlSuffix) {
		return readClaudeTranscript(path, cwd)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return HarnessUsage{}, false
	}
	if doc, ok := decodeOpencodeExport(raw); ok {
		return opencodeUsage(doc, path, cwd)
	}
	return readClaudeTranscript(path, cwd)
}

// readableUsageFile rejects missing and oversized files before parsing.
func readableUsageFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Size() <= maxUsageFileBytes
}

// claudeLine is the subset of one Claude Code transcript line the reader
// needs. The transcript is harness-written, so unknown fields are ignored
// and unparsable lines are skipped rather than failing the run.
type claudeLine struct {
	CostUSD      *float64       `json:"costUSD"`
	TotalCostUSD *float64       `json:"totalCostUSD"`
	Message      *claudeMessage `json:"message"`
	Type         string         `json:"type"`
	SessionID    string         `json:"sessionId"`
	Timestamp    string         `json:"timestamp"`
	Cwd          string         `json:"cwd"`
	RequestID    string         `json:"requestId"`
	StartTime    int64          `json:"startTime"`
}

// claudeMessage is the subset of a transcript line's message object.
type claudeMessage struct {
	CostUSD *float64 `json:"costUSD"`
	ID      string   `json:"id"`
}

// claudeScan carries the running parse state for one transcript.
type claudeScan struct { //nolint:govet // scanning accumulator; field order favors readability
	sessionID string
	cwd       string
	first     time.Time
	last      time.Time
	total     *float64
	seen      map[string]struct{}
	entrySum  float64
	startMS   int64
	entryCost bool
}

// readClaudeTranscript parses one JSONL transcript. The last cost-state
// entry wins (it is the authoritative cumulative snapshot); without one,
// deduplicated per-request costUSD entries sum. Tokens without a priced cost
// read as no measurement.
func readClaudeTranscript(path, cwd string) (HarnessUsage, bool) {
	scan, ok := scanClaudeTranscript(path)
	if !ok || !scan.matches(cwd) {
		return HarnessUsage{}, false
	}
	usage := scan.usage(path)
	usage.Source = harnessClaude
	usage.Session = scan.sessionID
	return usage, true
}

// scanClaudeTranscript walks the JSONL file, tolerating bad lines.
func scanClaudeTranscript(path string) (*claudeScan, bool) {
	file, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer file.Close()
	scan := &claudeScan{seen: map[string]struct{}{}}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, transcriptScanBuffer), maxTranscriptLine)
	for scanner.Scan() {
		var line claudeLine
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			continue
		}
		scan.observe(&line)
	}
	if scanner.Err() != nil {
		return nil, false
	}
	return scan, true
}

// matches rejects another checkout's transcript and empty files that only
// look like transcripts.
func (s *claudeScan) matches(cwd string) bool {
	if s.cwd != "" && filepath.Clean(s.cwd) != cwd {
		return false
	}
	return s.sessionID != "" || !s.first.IsZero()
}

// observe folds one transcript line into the scan state.
func (s *claudeScan) observe(line *claudeLine) {
	s.observeIdentity(line)
	s.observeTimestamp(line.Timestamp)
	s.observeStart(line.StartTime)
	if line.Type == "cost-state" {
		s.observeTotal(line.TotalCostUSD)
		return
	}
	s.observeEntryCost(line)
}

// observeIdentity keeps the latest session id and cwd.
func (s *claudeScan) observeIdentity(line *claudeLine) {
	if line.SessionID != "" {
		s.sessionID = line.SessionID
	}
	if line.Cwd != "" {
		s.cwd = line.Cwd
	}
}

// observeTimestamp widens the timestamp window.
func (s *claudeScan) observeTimestamp(raw string) {
	ts, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return
	}
	if s.first.IsZero() || ts.Before(s.first) {
		s.first = ts
	}
	if s.last.IsZero() || ts.After(s.last) {
		s.last = ts
	}
}

// observeStart keeps the epoch-ms start marker for timestamp-less files.
func (s *claudeScan) observeStart(startMS int64) {
	if startMS > 0 {
		s.startMS = startMS
	}
}

// observeTotal keeps the last cost-state total.
func (s *claudeScan) observeTotal(total *float64) {
	if total == nil {
		return
	}
	value := *total
	s.total = &value
}

// observeEntryCost sums per-request costs, deduplicating the repeated
// assistant lines Claude Code writes for one request.
func (s *claudeScan) observeEntryCost(line *claudeLine) {
	cost := entryCost(line)
	if cost == nil || !s.markEntry(entryKey(line)) {
		return
	}
	s.entrySum += *cost
	s.entryCost = true
}

// entryCost reads the per-request cost from either supported field.
func entryCost(line *claudeLine) *float64 {
	if line.CostUSD != nil {
		return line.CostUSD
	}
	if line.Message != nil {
		return line.Message.CostUSD
	}
	return nil
}

// entryKey is the dedupe key for one request line ("" counts every line).
func entryKey(line *claudeLine) string {
	if line.RequestID != "" {
		return line.RequestID
	}
	if line.Message != nil {
		return line.Message.ID
	}
	return ""
}

// markEntry reports whether this request line is new.
func (s *claudeScan) markEntry(key string) bool {
	if key == "" {
		return true
	}
	if _, dup := s.seen[key]; dup {
		return false
	}
	s.seen[key] = struct{}{}
	return true
}

// usage builds the session record, preferring the cost-state total, then
// the transcript timestamps, then the file itself.
func (s *claudeScan) usage(path string) HarnessUsage {
	mod := fileModTime(path)
	started, ended := s.window(mod)
	usd := s.total
	if usd == nil && s.entryCost {
		sum := s.entrySum
		usd = &sum
	}
	return HarnessUsage{Started: started, Ended: ended, USD: usd}
}

// window resolves the session window: transcript timestamps win, an epoch-ms
// startTime fills a missing start, and the file mtime fills a missing end.
func (s *claudeScan) window(mod time.Time) (time.Time, time.Time) {
	started, ended := s.first, s.last
	if started.IsZero() && s.startMS > 0 {
		started = time.UnixMilli(s.startMS)
	}
	if ended.IsZero() {
		ended = mod
	}
	if started.IsZero() || started.After(ended) {
		started = ended
	}
	return started, ended
}

// opencodeExportJSON is the shape `opencode export <session>` writes: one
// info object with the session total plus the message list.
type opencodeExportJSON struct {
	Info     *opencodeInfoJSON `json:"info"`
	Messages []opencodeMessage `json:"messages"`
}

// opencodeInfoJSON is the export's session metadata.
type opencodeInfoJSON struct {
	Cost      *float64         `json:"cost"`
	ID        string           `json:"id"`
	Directory string           `json:"directory"`
	Time      opencodeTimeJSON `json:"time"`
}

// opencodeTimeJSON is the export's millisecond session window.
type opencodeTimeJSON struct {
	Created int64 `json:"created"`
	Updated int64 `json:"updated"`
}

// opencodeMessage is one export message entry.
type opencodeMessage struct {
	Info *struct {
		Cost *float64 `json:"cost"`
	} `json:"info"`
}

// decodeOpencodeExport recognizes one opencode export document. A document
// with an info object is an export even when another directory owns it, so
// the caller never re-parses it as a transcript.
func decodeOpencodeExport(raw []byte) (*opencodeExportJSON, bool) {
	var doc opencodeExportJSON
	if err := json.Unmarshal(raw, &doc); err != nil || doc.Info == nil {
		return nil, false
	}
	return &doc, true
}

// opencodeUsage turns one recognized export into a session record, rejecting
// a session that belongs to another checkout.
func opencodeUsage(doc *opencodeExportJSON, path, cwd string) (HarnessUsage, bool) {
	if doc.Info.Directory != "" && filepath.Clean(doc.Info.Directory) != cwd {
		return HarnessUsage{}, false
	}
	mod := fileModTime(path)
	started := timeFromMillis(doc.Info.Time.Created, mod)
	ended := timeFromMillis(doc.Info.Time.Updated, mod)
	if started.After(ended) {
		started = ended
	}
	usd := doc.Info.Cost
	if usd == nil {
		usd = summedMessageCost(doc.Messages)
	}
	return HarnessUsage{Source: harnessOpencode, Session: doc.Info.ID, Started: started, Ended: ended, USD: usd}, true
}

// summedMessageCost totals per-message costs when the export carries no
// session-level total.
func summedMessageCost(messages []opencodeMessage) *float64 {
	var sum float64
	found := false
	for _, message := range messages {
		if message.Info == nil || message.Info.Cost == nil {
			continue
		}
		sum += *message.Info.Cost
		found = true
	}
	if !found {
		return nil
	}
	return &sum
}

// timeFromMillis converts an epoch-millisecond stamp, falling back to the
// file mtime for a zero (absent) value.
func timeFromMillis(ms int64, fallback time.Time) time.Time {
	if ms <= 0 {
		return fallback
	}
	return time.UnixMilli(ms)
}

// fileModTime reads a file's mtime, returning the zero time when it cannot.
func fileModTime(path string) time.Time {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

// dedupeUsage keeps one record per source+session. A later duplicate with a
// known USD replaces an earlier unpriced one (an explicit export may carry
// the price a transcript lacked).
func dedupeUsage(usage []HarnessUsage) []HarnessUsage {
	index := map[string]int{}
	out := make([]HarnessUsage, 0, len(usage))
	for _, record := range usage {
		key := record.Source + "\x00" + record.Session
		if at, seen := index[key]; seen {
			if out[at].USD == nil && record.USD != nil {
				out[at] = record
			}
			continue
		}
		index[key] = len(out)
		out = append(out, record)
	}
	return out
}

// MatchHarnessUsage returns the measured session whose window brackets at,
// nearest end first (a cost record is written at the end of a run, so the
// session that contains it is the run that produced it). No containing
// session reads as no measurement.
func MatchHarnessUsage(usage []HarnessUsage, at time.Time) *HarnessUsage {
	var best *HarnessUsage
	for i := range usage {
		record := &usage[i]
		if !record.contains(at) {
			continue
		}
		if best == nil || record.closer(at, best) {
			best = record
		}
	}
	return best
}

// contains reports whether at falls inside the session window.
func (u *HarnessUsage) contains(at time.Time) bool {
	if u.Started.IsZero() || u.Ended.IsZero() {
		return false
	}
	return !at.Before(u.Started) && !at.After(u.Ended)
}

// closer reports whether u should win a containment tie against best: the
// session that ends nearest at wins, then the later start.
func (u *HarnessUsage) closer(at time.Time, best *HarnessUsage) bool {
	gap, bestGap := u.Ended.Sub(at), best.Ended.Sub(at)
	if gap != bestGap {
		return gap < bestGap
	}
	return u.Started.After(best.Started)
}
