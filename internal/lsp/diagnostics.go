package lsp

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"maps"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"

	"github.com/stainless-code/verdict/internal/core"
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
	// uris is every spelling the client has this file open under; uri is
	// the one publishes go out under.
	uris     map[string]bool
	uri      string
	physical string
	key      string
	text     string
	version  int
	hasText  bool
}

// open records one more open spelling.
func (d *document) open(uri string) {
	if d.uris == nil {
		d.uris = map[string]bool{}
	}
	d.uris[uri] = true
}

// absorb takes over another document's open spellings when two documents
// turn out to name one file.
func (d *document) absorb(other *document) {
	for uri := range other.uris {
		d.open(uri)
	}
}

// lines splits the tracked text into lines without their `\r\n` or `\n`.
func (d *document) lines() []string {
	out := strings.Split(d.text, "\n")
	for i, l := range out {
		out[i] = strings.TrimSuffix(l, "\r")
	}
	return out
}

// rootDiagnostics maps every publishable finding one root holds for one
// path key to a Diagnostic.
func (s *Server) rootDiagnostics(root *servedRoot, key string, docLines []string) []Diagnostic {
	state := s.states[root.ID]
	found := state.findingsFor(key)
	out := make([]Diagnostic, 0, len(found))
	for i := range found {
		severity, ok := publishable(found[i].rec.Status, found[i].rec.Severity, s.initOpts.ShowDeferred)
		if ok {
			out = append(out, diagnostic(root, state, &found[i], severity, docLines))
		}
	}
	return out
}

// fileDiagnostics merges what every served root says about one file. Nested
// roots can both reach a file; their findings union under the one URI, each
// keeping its own data.root, and a finding two roots both carry on the same
// range (one review seen through both) appears once. The set is ordered by
// (start line, start character, code, root).
func (s *Server) fileDiagnostics(uri string, doc *document) []Diagnostic {
	var docLines []string
	if doc != nil && doc.hasText {
		docLines = doc.lines()
	}
	physical := ""
	if doc != nil {
		physical = doc.physical
	}
	merged := diagnosticSet{seen: map[string]bool{}, out: []Diagnostic{}}
	for i := range s.roots {
		root := &s.roots[i]
		if rel, ok := s.relOf(root, uri, physical); ok {
			merged.add(s.rootDiagnostics(root, root.pathKey(rel), docLines))
		}
	}
	slices.SortFunc(merged.out, compareDiagnostics)
	return merged.out
}

// diagnosticSet unions diagnostics, keeping the first of each (code, range).
type diagnosticSet struct {
	seen map[string]bool
	out  []Diagnostic
}

func (m *diagnosticSet) add(diags []Diagnostic) {
	for i := range diags {
		id := diags[i].Code + "\x00" + rangeKey(diags[i].Range)
		if !m.seen[id] {
			m.seen[id] = true
			m.out = append(m.out, diags[i])
		}
	}
}

func rangeKey(r lspRange) string {
	return strconv.Itoa(r.Start.Line) + ":" + strconv.Itoa(r.Start.Character) + "-" +
		strconv.Itoa(r.End.Line) + ":" + strconv.Itoa(r.End.Character)
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
	if c := cmp.Compare(a.Code, b.Code); c != 0 {
		return c
	}
	return cmp.Compare(a.Data.Root, b.Data.Root)
}

// diagnostic renders one joined finding. The range is whole-line and needs no
// document text; with tracked text and an exact anchor its end tightens to
// the last line's UTF-16 length, and any range is clamped to the document's
// last line so a shorter buffer never receives a position past its end.
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
		Deferred: f.rec.Status == statusDeferred, Truncated: f.truncated,
	}
	if docLines != nil && f.line <= len(docLines) {
		spanText := docLines[f.start-1 : f.line]
		data.SpanSHA256 = textHash(spanText)
		if f.anchor == anchorExact {
			rng.End = position{Line: f.line - 1, Character: utf16Len(docLines[f.line-1])}
		}
	}
	if docLines != nil {
		rng = clampRange(rng, docLines)
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

// clampRange keeps a range inside the tracked text: a position past the last
// line moves to the end of the last line.
func clampRange(rng lspRange, lines []string) lspRange {
	last := len(lines) - 1
	end := position{Line: last, Character: utf16Len(lines[last])}
	clamp := func(p position) position {
		if p.Line > last || (p.Line == last && p.Character > end.Character) {
			return end
		}
		return p
	}
	return lspRange{Start: clamp(rng.Start), End: clamp(rng.End)}
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

// plainLine drops every rune that could break, reorder, or hide what an
// editor shows next to code: control runes and line breaks (core.IsLineBreak,
// including U+2028/U+2029), the line and paragraph separator categories (Zl,
// Zp), bidirectional formatting, the zero-width space and joiners (U+200B
// to U+200D), the word joiner (U+2060), the zero-width no-break space or
// byte-order mark (U+FEFF), and the soft hyphen (U+00AD). A tab
// becomes a space.
func plainLine(s string) string {
	var b strings.Builder
	for _, c := range s {
		switch {
		case c == '\t':
			b.WriteRune(' ')
		case invisibleRune(c):
		default:
			b.WriteRune(c)
		}
	}
	return strings.TrimSpace(b.String())
}

func invisibleRune(c rune) bool {
	switch c {
	case '\u200B', '\u200C', '\u200D', '\u2060', '\uFEFF', '\u00AD':
		return true
	}
	return core.IsLineBreak(c) || unicode.In(c, unicode.Zl, unicode.Zp, unicode.Bidi_Control)
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

// published is the last set sent for one file: the URI spelling it went out
// under and the digest of the exact params bytes, so an identical set is
// never resent.
type published struct {
	uri    string
	digest string
}

// publishFile publishes one file's merged set, under the open document's
// URI when there is one (clients match diagnostics by URI spelling), else
// uri. It returns the number of frames sent.
func (s *Server) publishFile(key, uri string) int {
	doc := s.docs[key]
	if doc != nil {
		uri = doc.uri
	}
	return s.publish(key, uri, doc, s.fileDiagnostics(uri, doc))
}

// publish sends one file's set. An empty set is sent only to clear a set this
// session published before; an identical set is never resent; a set whose
// URI spelling moved first clears the old spelling. A set too large for one
// frame is cut to the frame cap. It returns the number of frames sent.
func (s *Server) publish(key, uri string, doc *document, diags []Diagnostic) int {
	frames, had := s.clearMovedSpelling(key, uri)
	if len(diags) == 0 && !had {
		return frames
	}
	params := publishParams{URI: uri, Diagnostics: diags}
	if doc != nil {
		v := doc.version
		params.Version = &v
	}
	total := len(params.Diagnostics)
	raw, ok := s.fitFrame(&params)
	if !ok {
		return frames
	}
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	if had && s.sent[key].digest == digest {
		return frames
	}
	s.logCut(uri, total, len(params.Diagnostics))
	s.writeFrame(raw)
	if len(params.Diagnostics) == 0 {
		delete(s.sent, key)
	} else {
		s.sent[key] = published{uri: uri, digest: digest}
	}
	return frames + 1
}

// clearMovedSpelling clears a file's last set when it went out under another
// URI spelling. It reports the frames sent and whether a set under uri
// itself is still on record.
func (s *Server) clearMovedSpelling(key, uri string) (int, bool) {
	prior, had := s.sent[key]
	if !had || prior.uri == uri {
		return 0, had
	}
	s.notify(methodPublish, publishParams{URI: prior.uri, Diagnostics: []Diagnostic{}})
	delete(s.sent, key)
	return 1, false
}

// logCut warns that a set was cut to the frame cap. publish calls it only
// when the cut set actually goes out: an unchanged cut republishes nothing
// and says nothing.
func (s *Server) logCut(uri string, total, kept int) {
	if kept < total {
		s.logf(msgWarning, "verdict: %d diagnostics for %s exceed the frame cap; %d are published", total, uri, kept)
	}
}

// fitFrame marshals one publish notification, cutting the set to the longest
// prefix whose frame fits the cap (each kept diagnostic marked truncated).
func (s *Server) fitFrame(params *publishParams) ([]byte, bool) {
	raw, err := marshalNotification(methodPublish, params)
	if err != nil {
		s.logf(msgError, "verdict: marshal diagnostics: %v", err)
		return nil, false
	}
	if len(raw) <= s.lim.frameBytes {
		return raw, true
	}
	all := params.Diagnostics
	fits := func(n int) ([]byte, bool) {
		cut := append([]Diagnostic(nil), all[:n]...)
		for i := range cut {
			cut[i].Data.Truncated = true
		}
		params.Diagnostics = cut
		b, merr := marshalNotification(methodPublish, params)
		return b, merr == nil && len(b) <= s.lim.frameBytes
	}
	raw, _ = fits(longestFit(len(all), func(n int) bool { _, ok := fits(n); return ok }))
	return raw, true
}

// longestFit binary-searches the largest n in [0, total) for which fits
// holds, given that fits(0) holds and fits(total) does not.
func longestFit(total int, fits func(int) bool) int {
	lo, hi := 0, total
	for hi-lo > 1 {
		mid := lo + (hi-lo)/2
		if fits(mid) {
			lo = mid
		} else {
			hi = mid
		}
	}
	return lo
}

// republishAll publishes every file any served root, open document, or
// earlier publish reaches, in URI order. A file no root contributes to any
// more is cleared once. It returns the number of frames sent.
func (s *Server) republishAll() int {
	targets := s.publishTargets()
	keys := slices.SortedFunc(maps.Keys(targets), func(a, b string) int {
		return cmp.Compare(targets[a], targets[b])
	})
	frames := 0
	for _, key := range keys {
		frames += s.publishFile(key, targets[key])
	}
	return frames
}

// publishTargets maps every file key a republish covers to the URI it would
// publish under before an open document's spelling takes over.
func (s *Server) publishTargets() map[string]string {
	targets := map[string]string{}
	for i := range s.roots {
		root := &s.roots[i]
		for _, rel := range s.states[root.ID].pathsOrNil() {
			targets[root.fileKey(rel)] = root.uriFor(rel)
		}
	}
	for key, doc := range s.docs {
		targets[key] = doc.uri
	}
	for key, p := range s.sent {
		if _, ok := targets[key]; !ok {
			targets[key] = p.uri
		}
	}
	return targets
}

// setText stores one document's text when it fits the per-document and
// session bounds, else tracks the document by version only. The session
// total is kept as a running sum, so a keystroke never walks the table.
func (s *Server) setText(doc *document, text *string) {
	if doc.hasText {
		s.docBytes -= len(doc.text)
	}
	doc.text, doc.hasText = "", false
	if text == nil || len(*text) > maxDocBytes || s.docBytes+len(*text) > maxDocBytesTotal {
		return
	}
	doc.text, doc.hasText = *text, true
	s.docBytes += len(*text)
}
