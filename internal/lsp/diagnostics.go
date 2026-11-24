package lsp

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"unicode"
	"unicode/utf16"

	"github.com/stainless-code/verdict/internal/untrusted"
)

const (
	diagnosticSource = "verdict"
	maxMessageRunes  = 160
	sigShort         = 8
	statusOpen       = "open"
	statusDeferred   = "deferred"
	quoteVerified    = "verified"
	messageSep       = " · "
	// maxDocBytes bounds the text tracked for one document; a larger buffer
	// is tracked by version only and its diagnostics keep whole-line ranges.
	maxDocBytes = 4 << 20
	// maxDocBytesTotal bounds the text tracked across every open document.
	maxDocBytesTotal = 32 << 20
)

// severityFor maps a finding severity onto DiagnosticSeverity. An absent or
// unrecognized severity is untagged and publishes as a Hint: honest, never
// inferred.
func severityFor(severity string) int {
	switch severity {
	case "critical":
		return severityError
	case "important":
		return severityWarning
	case "informational":
		return severityInformation
	default:
		return severityHint
	}
}

// publishable reports whether a finding's status publishes, and the severity
// it publishes at. Open findings publish at their severity; deferred ones
// only when the client asked for them, always as a Hint; addressed and
// triaged findings never publish.
func publishable(status, severity string, showDeferred bool) (int, bool) {
	switch status {
	case statusOpen:
		return severityFor(severity), true
	case statusDeferred:
		return severityHint, showDeferred
	default:
		return 0, false
	}
}

// document is one client-tracked buffer. Its text arrives only from the
// client: the server never reads a workspace file.
type document struct {
	uri     string
	rootID  string
	key     string
	text    string
	version int
	hasText bool
}

// lines splits the tracked text into lines without their `\r\n` or `\n`.
func (d *document) lines() []string {
	out := strings.Split(d.text, "\n")
	for i, l := range out {
		out[i] = strings.TrimSuffix(l, "\r")
	}
	return out
}

// diagnosticsFor maps every publishable finding on one path to a Diagnostic,
// ordered by (start line, start character, code).
func (s *Server) diagnosticsFor(root *servedRoot, state *bundleState, key string, doc *document) []Diagnostic {
	var docLines []string
	if doc != nil && doc.hasText {
		docLines = doc.lines()
	}
	found := state.findingsFor(key)
	out := []Diagnostic{}
	for i := range found {
		severity, ok := publishable(found[i].rec.Status, found[i].rec.Severity, s.initOpts.ShowDeferred)
		if ok {
			out = append(out, diagnostic(root, state, &found[i], severity, docLines))
		}
	}
	slices.SortFunc(out, compareDiagnostics)
	return out
}

// compareDiagnostics orders a set by (start line, start character, code), so
// a frame's bytes never depend on store order.
//
//nolint:gocritic // slices.SortFunc fixes the by-value comparator signature
func compareDiagnostics(a, b Diagnostic) int {
	if c := cmp.Compare(a.Range.Start.Line, b.Range.Start.Line); c != 0 {
		return c
	}
	if c := cmp.Compare(a.Range.Start.Character, b.Range.Start.Character); c != 0 {
		return c
	}
	return cmp.Compare(a.Code, b.Code)
}

// diagnostic renders one joined finding. The range is whole-line and needs no
// document text; with tracked text and an exact anchor its end tightens to
// the last line's UTF-16 length.
func diagnostic(root *servedRoot, state *bundleState, f *finding, severity int, docLines []string) Diagnostic {
	rng := lspRange{
		Start: position{Line: f.start - 1},
		End:   position{Line: f.line},
	}
	data := diagnosticData{
		Sig: f.rec.Sig, Bundle: state.bundle, Root: root.ID,
		Status: f.rec.Status, Severity: f.rec.Severity, Rigor: f.rec.Rigor,
		QuoteStatus: f.rec.Verification.Quote, Lens: f.rec.Lens, Category: f.rec.Category,
		Side: f.side, Anchor: f.anchor, Span: span{StartLine: f.start, Line: f.line},
		HunkSHA256: f.hunkSHA256, Suggestion: f.suggestion,
		Deferred: f.rec.Status == statusDeferred,
	}
	if docLines != nil && f.line <= len(docLines) {
		spanText := docLines[f.start-1 : f.line]
		data.SpanSHA256 = textHash(spanText)
		if f.anchor == anchorExact {
			rng.End = position{Line: f.line - 1, Character: utf16Len(docLines[f.line-1])}
		}
	}
	if docLines != nil && f.hunkSHA256 != "" {
		matches := data.SpanSHA256 == f.hunkSHA256
		data.SpanMatches = &matches
	}
	return Diagnostic{
		Range:    rng,
		Severity: severity,
		Code:     f.rec.Sig,
		Source:   diagnosticSource,
		Message:  message(&f.rec),
		Data:     data,
	}
}

// message is the one-line, plain-text diagnostic message: the body's first
// line, flattened and capped, then the evidence rung and any quote status
// other than verified.
func message(rec *record) string {
	first, _, _ := strings.Cut(strings.TrimSpace(rec.Body), "\n")
	text := untrusted.CapRunes(plainLine(first), maxMessageRunes)
	if text == "" {
		severity := rec.Severity
		if severity == "" {
			severity = "untagged"
		}
		text = severity + " finding " + shortSig(rec.Sig) + " at " + plainLine(rec.Locator)
	}
	if rec.Rigor != "" {
		text += messageSep + "rigor " + plainLine(rec.Rigor)
	}
	if q := rec.Verification.Quote; q != "" && q != quoteVerified {
		text += messageSep + "quote " + plainLine(q)
	}
	return text
}

// plainLine drops control runes and bidirectional formatting characters, so
// finding text cannot reorder or hide what an editor shows next to code.
func plainLine(s string) string {
	var b strings.Builder
	for _, c := range s {
		switch {
		case c == '\t':
			b.WriteRune(' ')
		case unicode.IsControl(c), unicode.Is(unicode.Bidi_Control, c):
		default:
			b.WriteRune(c)
		}
	}
	return strings.TrimSpace(b.String())
}

func shortSig(sig string) string {
	if len(sig) > sigShort {
		return sig[:sigShort]
	}
	return sig
}

// utf16Len counts one line's UTF-16 code units: LSP's default position unit.
func utf16Len(s string) int {
	n := 0
	for _, c := range s {
		n += utf16.RuneLen(c)
	}
	return n
}

// published is the last set sent for one URI: which root it belongs to, and
// the digest of the exact params bytes, so an identical set is never resent.
type published struct {
	rootID string
	digest string
}

// publishKey publishes one root-relative path's set, under the open
// document's URI when there is one (clients match diagnostics by URI
// spelling), else the root's rendering of the path.
func (s *Server) publishKey(root *servedRoot, key, rel string) {
	doc := s.docForKey(root.ID, key)
	s.publishURI(root.ID, s.uriForKey(root, key, rel), doc, s.diagnosticsFor(root, s.states[root.ID], key, doc))
}

// publishURI sends one URI's set. An empty set is sent only to clear a set
// this session published before; an identical set is never resent. It
// reports whether a frame went out.
func (s *Server) publishURI(rootID, uri string, doc *document, diags []Diagnostic) bool {
	params := publishParams{URI: uri, Diagnostics: diags}
	if doc != nil {
		v := doc.version
		params.Version = &v
	}
	prior, had := s.sent[uri]
	if len(diags) == 0 && !had {
		return false
	}
	raw, err := json.Marshal(params)
	if err != nil {
		s.logf(msgError, "verdict: marshal diagnostics: %v", err)
		return false
	}
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	if had && prior.digest == digest {
		return false
	}
	s.notify(methodPublish, params)
	if len(diags) == 0 {
		delete(s.sent, uri)
	} else {
		s.sent[uri] = published{rootID: rootID, digest: digest}
	}
	return true
}

// republishRoot sends every path with findings in the root's current state,
// plus every open document under it, then clears each URI this session
// published for the root that no longer carries a finding. It returns the
// number of frames sent; URIs go out in sorted order.
func (s *Server) republishRoot(root *servedRoot) int {
	state := s.states[root.ID]
	targets := s.rootTargets(root, state)
	count := 0
	for _, uri := range slices.Sorted(maps.Keys(targets)) {
		t := targets[uri]
		if s.publishURI(root.ID, uri, t.doc, s.diagnosticsFor(root, state, t.key, t.doc)) {
			count++
		}
	}
	return count + s.clearStale(root.ID, targets)
}

// clearStale publishes an empty set for every URI this session last sent
// for the root that the root no longer covers.
func (s *Server) clearStale(rootID string, keep map[string]target) int {
	count := 0
	for _, uri := range slices.Sorted(maps.Keys(s.sent)) {
		_, kept := keep[uri]
		if !kept && s.sent[uri].rootID == rootID && s.publishURI(rootID, uri, s.docs[uri], []Diagnostic{}) {
			count++
		}
	}
	return count
}

// target is one URI a root republishes: its open document, if any, and the
// path key its findings are indexed by.
type target struct {
	doc *document
	key string
}

// rootTargets maps every URI a root's republish covers: each path with
// findings (spelled as the open document spells it, else as the root renders
// it) and each open document under the root.
func (s *Server) rootTargets(root *servedRoot, state *bundleState) map[string]target {
	out := map[string]target{}
	if state != nil {
		for key, rel := range state.paths {
			out[s.uriForKey(root, key, rel)] = target{doc: s.docForKey(root.ID, key), key: key}
		}
	}
	for uri, doc := range s.docs {
		if doc.rootID == root.ID {
			out[uri] = target{doc: doc, key: doc.key}
		}
	}
	return out
}

// uriForKey spells one path's URI: the open document's, else the root's.
func (s *Server) uriForKey(root *servedRoot, key, rel string) string {
	if doc := s.docForKey(root.ID, key); doc != nil {
		return doc.uri
	}
	return root.uriFor(rel)
}

func (s *Server) docForKey(rootID, key string) *document {
	uri, ok := s.docKeys[rootID+"\x00"+key]
	if !ok {
		return nil
	}
	return s.docs[uri]
}

// trackedText reports whether one more buffer of n bytes fits the per-doc and
// session text bounds, given the bytes other documents already hold.
func (s *Server) trackedText(n int, except string) bool {
	if n > maxDocBytes {
		return false
	}
	total := n
	for uri, doc := range s.docs {
		if uri != except && doc.hasText {
			total += len(doc.text)
		}
	}
	return total <= maxDocBytesTotal
}
