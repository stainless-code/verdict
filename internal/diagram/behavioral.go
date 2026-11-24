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

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/evidence"
	"github.com/stainless-code/verdict/internal/untrusted"
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

	// maxDiagramBytes is the hard ceiling one rendered block may occupy. The
	// topology block spends at most 12,000 of the same 65,536-byte body, so
	// 8,000 here keeps both blocks and the findings well inside one comment.
	maxDiagramBytes = 8000
	// maxDiagramIDRunes caps one agent-supplied node id. The id is a lookup
	// key, never rendered: the block draws generated identifiers, so only the
	// map's memory is at stake.
	maxDiagramIDRunes = 40

	// diagramHeader names the diagram and states what the block proves: the
	// verified fraction the caps and drops are counted against.
	diagramHeader = "Behavior flow (verified against the diff — "
	// diagramHeaderTail closes the summary after the two counts.
	diagramHeaderTail = " elements anchored)"
	// omittedPrefix opens the one-line note a whole-diagram omission leaves in
	// the body. It is a blockquote, never a fence: nothing renders.
	omittedPrefix = "> Behavioral diagram omitted: "
	// diagramNotePrefix marks the cap-truncation note as a mermaid comment,
	// legal inside the fence and invisible in the rendered picture.
	diagramNotePrefix = "%% "
	// diagramNoteSuffix names the order the caps kept, so a truncated block
	// never reads as a complete picture.
	diagramNoteSuffix = " omitted (input order)"
	diagramNoteJoin   = " and "
	diagramNodeMore   = " more nodes"
	diagramEdgeMore   = " more edges"
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
type Limits struct {
	Nodes      int
	Edges      int
	LabelRunes int
	TitleRunes int
	IDRunes    int
	Bytes      int
}

// Caps is the one set of diagram limits.
var Caps = Limits{
	Nodes:      24,
	Edges:      40,
	LabelRunes: 120,
	TitleRunes: 120,
	IDRunes:    maxDiagramIDRunes,
	Bytes:      maxDiagramBytes,
}

// Types returns the supported diagram types in the order the messages name
// them.
func Types() []string { return slices.Clone(diagramTypes) }

// maxDiagramNoteBytes is the room the block reserves for its widest cap note,
// taken before the counts that fill it are known: two counts at countDigits
// digits each, the wording, the indented comment prefix, and the suffix.
const maxDiagramNoteBytes = len(newline+mermaidIndent+diagramNotePrefix) + 2*countDigits +
	len(diagramNodeMore) + len(diagramNoteJoin) + len(diagramEdgeMore) + len(diagramNoteSuffix)

// Result is one settled behavioral diagram: every element whose quote the
// pinned diff proved (Block draws the cap-truncated head of them) and the
// counts the receipt and the status publish. Omitted is set when the whole
// diagram is left out; Type is empty when the supplied type was out of scope
// or the value could not be read, so the receipt never publishes a type
// outside the enum.
type Result struct {
	Title    string
	Omitted  string
	Type     string
	Nodes    []node
	Edges    []edge
	Supplied int
	Verified int
	Dropped  int
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
// agent's value is sanitized like a label before it reaches the note.
func typeOmission(kind string) string {
	want := strings.Join(diagramTypes, typeSeparator)
	if strings.TrimSpace(kind) == "" {
		return "no type was supplied (want " + want + ")"
	}
	return fmt.Sprintf("type %q is out of scope (want %s)", safeLabel(kind, Caps.IDRunes), want)
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
	ids := make(map[string]string, len(req.Nodes))
	for i := range req.Nodes {
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
	for i := range req.Edges {
		supplied := &req.Edges[i]
		from, to := ids[supplied.From], ids[supplied.To]
		if !verifyAnchor(idx, supplied.Evidence) || from == "" || to == "" {
			continue
		}
		edges = append(edges, edge{from: from, to: to, label: safeLabel(supplied.Label, Caps.LabelRunes)})
	}
	return edges
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
		return fmt.Sprintf("none of the %d supplied elements verified against the diff", res.Supplied)
	}
	if !meetsVerifiedFraction(res.Verified, res.Supplied) {
		return fmt.Sprintf("%d of %d elements failed quote verification (min %d%% anchored)",
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
	edges, _, edgesLeft := fitDiagramEdges(r.Type, nodes, r.Edges, budget, used)
	for i := range nodes {
		lines = append(lines, behavioralNodeLine(r.Type, &nodes[i]))
	}
	for i := range edges {
		lines = append(lines, behavioralEdgeLine(r.Type, &edges[i]))
	}
	if note := diagramNote(nodesLeft, edgesLeft); note != "" {
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

// fitDiagramNodes keeps the head of the node list the node cap and the byte
// budget allow, reporting the bytes spent and how many nodes are left out.
func fitDiagramNodes(kind string, nodes []node, budget, used int) ([]node, int, int) {
	kept := 0
	for kept < len(nodes) && kept < Caps.Nodes {
		cost := len(newline + behavioralNodeLine(kind, &nodes[kept]))
		if used+cost > budget {
			break
		}
		used += cost
		kept++
	}
	return nodes[:kept], used, len(nodes) - kept
}

// fitDiagramEdges keeps the head of the edge list the edge cap and the byte
// budget allow, and counts every drawable edge it had to leave out. An edge
// whose endpoint the node cap dropped is skipped without a count of its own —
// the node note already names that truncation.
func fitDiagramEdges(kind string, nodes []node, edges []edge, budget, used int) ([]edge, int, int) {
	kept := make([]edge, 0, len(edges))
	left := 0
	for i := range edges {
		if !bothEndpointsKept(nodes, &edges[i]) {
			continue
		}
		cost := len(newline + behavioralEdgeLine(kind, &edges[i]))
		if left > 0 || len(kept) == Caps.Edges || used+cost > budget {
			left++
			continue
		}
		used += cost
		kept = append(kept, edges[i])
	}
	return kept, used, left
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

// diagramNote names what the caps left out, so a truncated block never reads as
// a complete picture.
func diagramNote(nodes, edges int) string {
	var parts []string
	if nodes > 0 {
		parts = append(parts, strconv.Itoa(nodes)+diagramNodeMore)
	}
	if edges > 0 {
		parts = append(parts, strconv.Itoa(edges)+diagramEdgeMore)
	}
	if len(parts) == 0 {
		return ""
	}
	return diagramNotePrefix + strings.Join(parts, diagramNoteJoin) + diagramNoteSuffix
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
		if e.label == "" {
			return mermaidIndent + e.from + "->>" + e.to + ": "
		}
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

// safeLabel renders one agent-supplied title or label as Mermaid label text:
// the closed punctuation allowlist runs first so no markup character survives,
// [untrusted.InlineProse] then flattens controls and neutralizes comments,
// fences, links, and mentions as it does for every other agent string, and the
// rune cap bounds the result. Labels only ever appear after a fixed prefix
// inside one line, so no label can start a Mermaid statement.
func safeLabel(s string, maxRunes int) string {
	clean := allowLabelRunes(s)
	clean = untrusted.InlineProse(clean)
	return untrusted.CapRunes(strings.TrimSpace(clean), maxRunes)
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
	Detail   string
	Supplied int
	Verified int
	Dropped  int
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
			State: StatusOmitted, Type: r.Type, Detail: r.Omitted,
			Supplied: r.Supplied, Verified: r.Verified, Dropped: r.Dropped,
		}
	case r.Type == "":
		return Status{State: StatusAbsent, Detail: AbsentDetail}
	default:
		return Status{
			State:    StatusRendered,
			Type:     r.Type,
			Detail:   strconv.Itoa(r.Verified) + "/" + strconv.Itoa(r.Supplied) + " elements anchored",
			Supplied: r.Supplied, Verified: r.Verified, Dropped: r.Dropped,
		}
	}
}
