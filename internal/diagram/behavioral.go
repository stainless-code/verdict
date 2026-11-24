// Package diagram owns the two mermaid surfaces a review body can carry: the
// structural topology computed from a bundle's diff, which no agent draws,
// and the behavioral diagram a review supplies as structure, which verdict
// verifies quote by quote against the pinned diff and renders itself. Raw
// mermaid is never an input, and neither block can fail a review.
package diagram

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/evidence"
)

// Behavioral diagram vocabulary, caps, and published wording.
const (
	// DiagramSequence, DiagramState, and DiagramFlow are the only types a
	// review may supply. Everything else is out of scope: verdict omits the
	// block instead of drawing a diagram it cannot render honestly.
	DiagramSequence = "sequence"
	DiagramState    = "state"
	DiagramFlow     = "flow"

	// MinVerifiedPercent is the share of supplied elements the diff has to
	// prove before the block renders at all. A diagram whose anchors are
	// mostly wrong is not a picture of this change, so it is left out whole
	// with a one-line note rather than drawn half-invented.
	MinVerifiedPercent = 50
	// percentScale is the denominator MinVerifiedPercent is read against.
	percentScale = 100
	// checkFactor sets how far past the render caps verdict still runs the
	// quote matcher: ten times the node and edge caps. Past that an element is
	// counted as supplied and dropped without a match, so a review carrying
	// thousands of elements costs a bounded amount of work.
	checkFactor = 10

	// maxDiagramBytes is the hard ceiling one rendered block may occupy. The
	// topology block spends at most 12,000 of the same 65,536-byte body, so
	// 8,000 here keeps both blocks and the findings well inside one comment.
	maxDiagramBytes = 8000
	// maxDiagramIDRunes caps one agent-supplied node id. The id is a lookup
	// key, never rendered: the block draws generated identifiers, so only the
	// map's memory is at stake.
	maxDiagramIDRunes = 40

	// diagramHeader names the diagram and states exactly what the block
	// proves: each drawn node and edge cites a real NEW-side diff line. It
	// does not claim the relation an edge draws — that stays the agent's.
	diagramHeader = "Behavior flow (each element anchored to a diff line — "
	// diagramHeaderTail closes the summary after the two counts.
	diagramHeaderTail = " anchored)"
	// omittedPrefix opens the one-line note a whole-diagram omission leaves in
	// the body. It is a blockquote, never a fence: nothing renders.
	omittedPrefix = "> Behavioral diagram omitted: "
	// diagramNotePrefix marks the cap-truncation note as a mermaid comment,
	// legal inside the fence and invisible in the rendered picture.
	diagramNotePrefix = "%% "
	// diagramNoteSuffix closes the cap note, so a truncated block never reads
	// as a complete picture. The caps keep whatever still fits, in input
	// order, and skip only what does not, so the counts are exact.
	diagramNoteSuffix = " omitted (caps)"
	diagramNoteJoin   = " and "
	diagramNodeMore   = " more nodes"
	diagramEdgeMore   = " more edges"
	// diagramOrphanPrefix and diagramOrphanSuffix word the edges the node cap
	// left without an endpoint, so they are counted instead of vanishing.
	diagramOrphanPrefix = "; "
	diagramOrphanSuffix = " edges to omitted nodes not drawn"
	// accTitlePrefix is the accessible-title directive Mermaid supports in all
	// three rendered types. The title is metadata: it never becomes a statement
	// of its own, so a hostile title cannot change the diagram.
	accTitlePrefix = "accTitle: "
	// diagramTail closes the mermaid fence and the <details> region. No node or
	// edge may spend it, so the byte budget reserves it up front.
	diagramTail = newline + "```" + newline + newline + "</details>"
	// labelPunctuation is the closed set of ASCII punctuation a rendered label
	// may keep. Everything else — quotes, backticks, brackets, braces, pipes,
	// semicolons, percent signs, angle brackets, ampersands, hashes, equals,
	// tildes, and backslashes — is dropped, so a label can never close a quote,
	// open a directive, start a comment, or spell an arrow.
	labelPunctuation = ".,:!?'()+_-/@"
	// typeSeparator joins the out-of-scope note's wanted list.
	typeSeparator = ", "
	// nodeIDPrefix opens every generated node identifier. The block never
	// renders an agent-chosen id, so no supplied byte reaches statement syntax.
	nodeIDPrefix = "n"
)

// diagramTypes is the supported vocabulary, in the order the note names it.
var diagramTypes = []string{DiagramSequence, DiagramState, DiagramFlow}

// Limits are the diagram caps the shape checker and the renderer share, so the
// warning an agent reads and the truncation it will get can never disagree.
// CheckNodes and CheckEdges bound the quote matching itself: elements past
// them are dropped unmatched.
type Limits struct {
	Nodes      int
	Edges      int
	CheckNodes int
	CheckEdges int
	LabelRunes int
	TitleRunes int
	IDRunes    int
	Bytes      int
}

// Caps is the one set of diagram limits.
var Caps = Limits{
	Nodes:      24,
	Edges:      40,
	CheckNodes: checkFactor * 24,
	CheckEdges: checkFactor * 40,
	LabelRunes: 120,
	TitleRunes: 120,
	IDRunes:    maxDiagramIDRunes,
	Bytes:      maxDiagramBytes,
}

// Types returns the supported diagram types in the order the messages name
// them.
func Types() []string { return slices.Clone(diagramTypes) }

// maxDiagramNoteBytes is the room the block reserves for its widest cap note,
// taken before the counts that fill it are known: three counts at countDigits
// digits each, the wording, the indented comment prefix, and the suffixes.
const maxDiagramNoteBytes = len(newline+mermaidIndent+diagramNotePrefix) + 3*countDigits +
	len(diagramNodeMore) + len(diagramNoteJoin) + len(diagramEdgeMore) + len(diagramNoteSuffix) +
	len(diagramOrphanPrefix) + len(diagramOrphanSuffix)

// Result is one settled behavioral diagram: every element whose quote matched
// a NEW-side line of the pinned diff (Block draws the cap-truncated head of
// them) and the counts the receipt and the status publish. Omitted is set
// when the whole diagram is left out; Type is empty when the supplied type was
// out of scope or the value could not be read, so the receipt never publishes
// a type outside the enum.
type Result struct {
	Title    string
	Omitted  string
	Type     string
	Nodes    []node
	Edges    []edge
	Supplied int
	Verified int
	Dropped  int
	// unchecked counts the elements past the checking limits: dropped
	// without a match, and named in the omission reason when they cost the
	// block its threshold.
	unchecked int
}

// node is one node that survived verification: the generated identifier the
// block renders and the sanitized label it shows.
type node struct {
	id, label string
}

// edge is one verified relation between two surviving nodes.
type edge struct {
	from, to, label string
}

// Settle verifies every supplied node and edge against the bundle's pinned
// diff and settles what the block may draw. A node whose quote does not verify
// is dropped, an edge is dropped when its own quote fails or when either
// endpoint is gone, and a diagram whose verified fraction falls under
// [MinVerifiedPercent] is omitted whole. Nothing here fails a review: the
// receipt counts and the note are the whole consequence.
func Settle(idx *evidence.DiffIndex, req *core.ReviewDiagram) Result {
	if req == nil {
		return Result{}
	}
	sup := suppliedCount(req)
	if !knownDiagramType(req.Type) {
		return Result{Supplied: sup, Dropped: sup, Omitted: typeOmission(req.Type)}
	}
	res := Result{
		Type:     req.Type,
		Title:    safeLabel(req.Title, Caps.TitleRunes),
		Supplied: sup,
	}
	kept := settleNodes(idx, req, &res)
	res.Edges = settleEdges(idx, req, kept)
	res.unchecked = max(len(req.Nodes)-Caps.CheckNodes, 0) + max(len(req.Edges)-Caps.CheckEdges, 0)
	if res.Type == DiagramSequence {
		nameSilentMessages(&res)
	}
	res.Verified = len(res.Nodes) + len(res.Edges)
	res.Dropped = res.Supplied - res.Verified
	res.Omitted = omission(&res)
	return res
}

// Unreadable is the settled result for a `diagram` value that is present but
// not an object: nothing in it can be counted or drawn, so the block is
// omitted with the note that names why, and the shape checker's
// diagram-invalid warning carries the detail.
func Unreadable() Result {
	return Result{Omitted: "the diagram value is not an object"}
}

// typeOmission names why a type outside the vocabulary draws nothing. The
// note is the one place an agent string reaches the body outside the fence,
// so the value is reduced to the label alphabet and then set in a code span:
// GitHub links no URL and pings no mention inside one, and the alphabet holds
// no backtick that could close it.
func typeOmission(kind string) string {
	want := strings.Join(diagramTypes, typeSeparator)
	if strings.TrimSpace(kind) == "" {
		return "no type was supplied (want " + want + ")"
	}
	shown := safeLabel(kind, Caps.IDRunes)
	if shown == "" {
		return "the supplied type is out of scope (want " + want + ")"
	}
	return "type `" + shown + "` is out of scope (want " + want + ")"
}

// suppliedCount is every element the agent supplied, drawn or not.
func suppliedCount(req *core.ReviewDiagram) int {
	return len(req.Nodes) + len(req.Edges)
}

// knownDiagramType reports whether t is one of the three supported types.
func knownDiagramType(t string) bool {
	return slices.Contains(diagramTypes, t)
}

// settleNodes keeps the nodes whose quotes verify, in input order, assigning
// one generated identifier per surviving agent id. A repeated id keeps its
// first node — one identifier, one box — and a node with no id is dropped,
// because no edge could name it and an unnamed box proves nothing the others
// do not.
// A label the sanitizer empties shows the generated identifier instead, so no
// declaration is ever left without the text its syntax requires.
func settleNodes(idx *evidence.DiffIndex, req *core.ReviewDiagram, res *Result) map[string]string {
	ids := make(map[string]string, min(len(req.Nodes), Caps.CheckNodes))
	for i := range req.Nodes[:min(len(req.Nodes), Caps.CheckNodes)] {
		supplied := &req.Nodes[i]
		if supplied.ID == "" || ids[supplied.ID] != "" || !verifyAnchor(idx, supplied.Evidence) {
			continue
		}
		id := nodeIDPrefix + strconv.Itoa(len(res.Nodes)+1)
		ids[supplied.ID] = id
		label := safeLabel(supplied.Label, Caps.LabelRunes)
		if label == "" {
			label = id
		}
		res.Nodes = append(res.Nodes, node{id: id, label: label})
	}
	return ids
}

// settleEdges keeps the edges whose own quote verifies and whose two endpoints
// both survived, in input order. A dangling edge is never drawn: the block
// points at nothing it did not verify.
func settleEdges(idx *evidence.DiffIndex, req *core.ReviewDiagram, ids map[string]string) []edge {
	var edges []edge
	for i := range req.Edges[:min(len(req.Edges), Caps.CheckEdges)] {
		supplied := &req.Edges[i]
		from, to := ids[supplied.From], ids[supplied.To]
		if !verifyAnchor(idx, supplied.Evidence) || from == "" || to == "" {
			continue
		}
		edges = append(edges, edge{from: from, to: to, label: safeLabel(supplied.Label, Caps.LabelRunes)})
	}
	return edges
}

// nameSilentMessages gives every unlabeled sequence message its target's
// label. A sequence message needs text after its colon — `A->>B:` with
// nothing after it does not parse, and a lone trailing space does not survive
// a pipeline that trims lines — so each one carries a non-empty, allowlisted
// label instead of an empty one.
func nameSilentMessages(res *Result) {
	labels := make(map[string]string, len(res.Nodes))
	for i := range res.Nodes {
		labels[res.Nodes[i].id] = res.Nodes[i].label
	}
	for i := range res.Edges {
		if res.Edges[i].label == "" {
			res.Edges[i].label = labels[res.Edges[i].to]
		}
	}
}

// verifyAnchor runs one element's quote through the A2 matcher exactly as a
// spec check's evidence runs: the same three fields, read on the NEW side. A
// missing index, path, or quote proves nothing.
func verifyAnchor(idx *evidence.DiffIndex, anchor core.SpecEvidence) bool {
	if idx == nil || anchor.Path == "" || anchor.Quote == "" {
		return false
	}
	return evidence.MatchQuote(idx, evidence.QuoteRequest{
		Path:  anchor.Path,
		Side:  evidence.SideRight,
		Quote: anchor.Quote,
		Line:  anchor.Line,
	}).Verified()
}

// omission states why the whole diagram is left out, or "" when it renders.
func omission(res *Result) string {
	if res.Supplied == 0 {
		return "no nodes or edges were supplied"
	}
	if res.Verified == 0 {
		return fmt.Sprintf("none of the %d supplied elements anchored (each did not verify or was malformed)",
			res.Supplied)
	}
	if !meetsVerifiedFraction(res.Verified, res.Supplied) && res.unchecked > 0 {
		return fmt.Sprintf("%d of %d elements did not verify, were malformed, or were past the checking limit "+
			"of %d nodes and %d edges (min %d%% anchored)",
			res.Dropped, res.Supplied, Caps.CheckNodes, Caps.CheckEdges, MinVerifiedPercent)
	}
	if !meetsVerifiedFraction(res.Verified, res.Supplied) {
		return fmt.Sprintf("%d of %d elements did not verify or were malformed (min %d%% anchored)",
			res.Dropped, res.Supplied, MinVerifiedPercent)
	}
	return ""
}

// meetsVerifiedFraction reports whether verified/supplied reaches
// [MinVerifiedPercent] by exact integer cross-multiplication.
func meetsVerifiedFraction(verified, supplied int) bool {
	if supplied == 0 {
		return false
	}
	return verified*percentScale >= MinVerifiedPercent*supplied
}

// Block renders the settled diagram as one collapsed block, or the one-line
// omission note when it was left out whole. It returns "" when the review
// supplied no diagram at all, so an absent diagram changes no body byte.
func (r *Result) Block() string {
	if r.Omitted != "" {
		return omittedPrefix + r.Omitted
	}
	if r.Type == "" {
		return ""
	}
	lines := []string{
		"<details>", "<summary>" + r.summary() + "</summary>", "",
		"```mermaid", declaration(r.Type),
	}
	if r.Title != "" {
		lines = append(lines, mermaidIndent+accTitlePrefix+r.Title)
	}
	budget := maxDiagramBytes - len(diagramTail) - maxDiagramNoteBytes
	used := len(strings.Join(lines, newline))
	nodes, used, nodesLeft := fitDiagramNodes(r.Type, r.Nodes, budget, used)
	edges, edgesLeft, orphaned := fitDiagramEdges(r.Type, nodes, r.Edges, budget, used)
	for i := range nodes {
		lines = append(lines, behavioralNodeLine(r.Type, &nodes[i]))
	}
	for i := range edges {
		lines = append(lines, behavioralEdgeLine(r.Type, &edges[i]))
	}
	if note := diagramNote(nodesLeft, edgesLeft, orphaned); note != "" {
		lines = append(lines, mermaidIndent+note)
	}
	return strings.Join(append(lines, "```", "", "</details>"), newline)
}

// declaration maps one settled type to the Mermaid declaration line. The three
// literals are the whole vocabulary: no agent byte reaches it.
func declaration(kind string) string {
	switch kind {
	case DiagramSequence:
		return "sequenceDiagram"
	case DiagramState:
		return "stateDiagram-v2"
	default:
		return "flowchart TD"
	}
}

// summary is the block's one summary line. Its counts are the receipt's, read
// off the same settled result, so the body and record.json cannot disagree.
func (r *Result) summary() string {
	return diagramHeader + strconv.Itoa(r.Verified) + "/" + strconv.Itoa(r.Supplied) + diagramHeaderTail
}

// Summary returns the receipt the bundle stores, or nil when the review
// supplied no diagram. The receipt never withholds approval.
func (r *Result) Summary() *core.EvidenceDiagram {
	if r.Type == "" && r.Omitted == "" {
		return nil
	}
	return &core.EvidenceDiagram{
		Type:     r.Type,
		Title:    r.Title,
		Supplied: r.Supplied,
		Verified: r.Verified,
		Dropped:  r.Dropped,
		Omitted:  r.Omitted,
	}
}

// fitDiagramNodes keeps every node, in input order, that the node cap and the
// byte budget still allow, skipping only a node that does not fit, and
// reports the bytes spent and how many nodes it left out.
func fitDiagramNodes(kind string, nodes []node, budget, used int) (kept []node, spent, left int) {
	kept = make([]node, 0, min(len(nodes), Caps.Nodes))
	for i := range nodes {
		cost := len(newline + behavioralNodeLine(kind, &nodes[i]))
		if len(kept) == Caps.Nodes || used+cost > budget {
			left++
			continue
		}
		used += cost
		kept = append(kept, nodes[i])
	}
	return kept, used, left
}

// fitDiagramEdges keeps every edge, in input order, that the edge cap and the
// byte budget still allow, skipping only an edge that does not fit. It
// returns the kept edges, how many drawable edges the caps left out, and how
// many edges the node cap orphaned by dropping an endpoint: both counts reach
// the note, so no supplied edge disappears unannounced.
func fitDiagramEdges(kind string, nodes []node, edges []edge, budget, used int) (kept []edge, left, orphaned int) {
	kept = make([]edge, 0, len(edges))
	for i := range edges {
		if !bothEndpointsKept(nodes, &edges[i]) {
			orphaned++
			continue
		}
		cost := len(newline + behavioralEdgeLine(kind, &edges[i]))
		if len(kept) == Caps.Edges || used+cost > budget {
			left++
			continue
		}
		used += cost
		kept = append(kept, edges[i])
	}
	return kept, left, orphaned
}

// bothEndpointsKept reports whether an edge joins two rendered nodes.
func bothEndpointsKept(nodes []node, e *edge) bool {
	from, to := false, false
	for i := range nodes {
		from = from || nodes[i].id == e.from
		to = to || nodes[i].id == e.to
	}
	return from && to
}

// diagramNote names what the caps left out, so a truncated block never reads
// as a complete picture: the nodes and edges past a cap, then the edges the
// node cap left without an endpoint.
func diagramNote(nodes, edges, orphaned int) string {
	var parts []string
	if nodes > 0 {
		parts = append(parts, strconv.Itoa(nodes)+diagramNodeMore)
	}
	if edges > 0 {
		parts = append(parts, strconv.Itoa(edges)+diagramEdgeMore)
	}
	note := ""
	if len(parts) > 0 {
		note = strings.Join(parts, diagramNoteJoin) + diagramNoteSuffix
	}
	if orphaned > 0 {
		note += diagramOrphanPrefix + strconv.Itoa(orphaned) + diagramOrphanSuffix
	}
	if note == "" {
		return ""
	}
	return diagramNotePrefix + strings.TrimPrefix(note, diagramOrphanPrefix)
}

// behavioralNodeLine renders one node declaration for the diagram type.
func behavioralNodeLine(kind string, n *node) string {
	switch kind {
	case DiagramSequence:
		return mermaidIndent + "participant " + n.id + " as " + n.label
	case DiagramState:
		return mermaidIndent + "state \"" + n.label + "\" as " + n.id
	default:
		return mermaidIndent + n.id + "[\"" + n.label + "\"]"
	}
}

// behavioralEdgeLine renders one transition for the diagram type. Every arrow
// is a fixed literal, and both endpoints are generated identifiers, so no
// agent byte chooses the syntax of the line.
func behavioralEdgeLine(kind string, e *edge) string {
	switch kind {
	case DiagramSequence:
		// Settle names every silent message, so the text is never empty.
		return mermaidIndent + e.from + "->>" + e.to + ": " + e.label
	case DiagramState:
		if e.label == "" {
			return mermaidIndent + e.from + " --> " + e.to
		}
		return mermaidIndent + e.from + " --> " + e.to + " : " + e.label
	default:
		if e.label == "" {
			return mermaidIndent + e.from + " --> " + e.to
		}
		return mermaidIndent + e.from + " -->|\"" + e.label + "\"| " + e.to
	}
}

// safeLabel renders one agent-supplied title or label as Mermaid label text.
// The closed allowlist is the whole sanitizer, so every rune of the result is
// a letter, digit, combining mark, space, or one of labelPunctuation — no
// other rune, not even a zero-width one, can appear. The mention pass of
// untrusted.InlineProse is deliberately not applied: on allowlisted text its
// only effect is a U+200B inside an `@handle`, and a mention inside a mermaid
// fence or a code span never notifies anyone. The rune cap closes with
// labelEllipsis, also allowlisted. Labels only ever appear after a fixed
// prefix inside one line, so no label can start a Mermaid statement.
func safeLabel(s string, maxRunes int) string {
	return capLabel(strings.TrimSpace(allowLabelRunes(s)), maxRunes)
}

// capLabel bounds a sanitized label at maxRunes runes, ending a shortened one
// with labelEllipsis — the topology's own marker, three allowlisted dots, so a
// capped label stays inside the label alphabet.
func capLabel(s string, maxRunes int) string {
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	keep := maxRunes - utf8.RuneCountInString(labelEllipsis)
	runes := []rune(s)
	return strings.TrimSpace(string(runes[:max(keep, 0)])) + labelEllipsis
}

// allowLabelRunes keeps only letters, digits, spaces, and the closed
// punctuation set, collapsing whitespace and dash runs so no `-->`, `-->>`,
// `%%`, `==>`, or `-->|` sequence can be spelled. A collapsed dash run is what
// keeps an arrow out: the allowlist already drops `>` and `%`. A line break or
// tab becomes one space, so a multi-line label stays one readable line.
func allowLabelRunes(s string) string {
	var sb strings.Builder
	sb.Grow(len(s))
	var last rune
	for _, r := range s {
		if unicode.IsSpace(r) {
			r = ' '
		}
		if !labelRune(r) || collapses(last, r) {
			continue
		}
		last = r
		sb.WriteRune(r)
	}
	return sb.String()
}

// collapses reports whether r repeats a space or a dash it directly follows:
// a run of either keeps one rune.
func collapses(last, r rune) bool {
	return r == last && (r == ' ' || r == '-')
}

// labelRune reports whether r may appear in a rendered label.
func labelRune(r rune) bool {
	if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r) {
		return true
	}
	return r == ' ' || strings.ContainsRune(labelPunctuation, r)
}

// Status is the settled diagram's published disposition: what post.json's
// body.diagram reports and what a human dry run prints.
type Status struct {
	State    string
	Type     string
	Title    string
	Omitted  string
	Detail   string
	Supplied int
	Verified int
	Dropped  int
}

// Post projects the status into the dry run's body.diagram: nil when no block
// was drawn, else the same fields evidence.diagram carries plus the state, so
// a dry run and the stored receipt can be read against each other field by
// field.
func (s *Status) Post() *core.PostDiagram {
	if s.State == StatusAbsent || s.State == "" {
		return nil
	}
	return &core.PostDiagram{
		Status: s.State, Type: s.Type, Title: s.Title,
		Supplied: s.Supplied, Verified: s.Verified, Dropped: s.Dropped, Omitted: s.Omitted,
	}
}

// Diagram status states.
const (
	StatusAbsent   = "absent"
	StatusRendered = "rendered"
	StatusOmitted  = "omitted"
	// AbsentDetail is the absent status's detail: no block, whether the
	// review supplied none or the operator left the knob off.
	AbsentDetail = "no behavioral diagram drawn"
)

// Status settles the disposition one surface publishes.
func (r *Result) Status() Status {
	switch {
	case r.Omitted != "":
		return Status{
			State: StatusOmitted, Type: r.Type, Title: r.Title, Omitted: r.Omitted, Detail: r.Omitted,
			Supplied: r.Supplied, Verified: r.Verified, Dropped: r.Dropped,
		}
	case r.Type == "":
		return Status{State: StatusAbsent, Detail: AbsentDetail}
	default:
		return Status{
			State:    StatusRendered,
			Type:     r.Type,
			Title:    r.Title,
			Detail:   strconv.Itoa(r.Verified) + "/" + strconv.Itoa(r.Supplied) + " elements anchored",
			Supplied: r.Supplied, Verified: r.Verified, Dropped: r.Dropped,
		}
	}
}
