package diagram

import (
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/evidence"
)

// settledDiff is the fixture every case verifies against. Lines 2 and 3 are
// added, so "func expired() bool {" and "return true" are anchorable and
// "package auth" (line 1) is context a right-side quote may still match.
const settledDiff = "" +
	"diff --git a/src/auth.go b/src/auth.go\n" +
	"--- a/src/auth.go\n" +
	"+++ b/src/auth.go\n" +
	"@@ -1,2 +1,4 @@\n" +
	" package auth\n" +
	"+func expired() bool {\n" +
	"+\treturn true\n" +
	" }\n"

// quote builds the anchored quote every node and edge in these fixtures
// carries unless a case says otherwise.
func quote(path string, line int, text string) core.SpecEvidence {
	return core.SpecEvidence{Path: path, Line: line, Quote: text}
}

// verifiedAnchor is the one anchor the fixture diff proves.
func verifiedAnchor(line int, text string) core.SpecEvidence {
	return quote("src/auth.go", line, text)
}

func settledIndex(t *testing.T) *evidence.DiffIndex {
	t.Helper()
	return evidence.IndexDiff([]byte(settledDiff))
}

// TestSettleRendersEachType pins the exact block each supported type draws, so
// a renderer change is a copy of this test's literals, never a silent move.
func TestSettleRendersEachType(t *testing.T) {
	t.Parallel()
	node := core.DiagramNode{ID: "caller", Label: "Caller", Evidence: verifiedAnchor(2, "func expired() bool {")}
	leaf := core.DiagramNode{ID: "svc", Label: "Service", Evidence: verifiedAnchor(3, "return true")}
	edge := core.DiagramEdge{From: "caller", To: "svc", Label: "request", Evidence: verifiedAnchor(3, "return true")}
	tests := []struct {
		name string
		req  *core.ReviewDiagram
		want string
	}{
		{
			name: "sequence",
			req: &core.ReviewDiagram{
				Type: DiagramSequence, Title: "Refresh flow",
				Nodes: []core.DiagramNode{node, leaf}, Edges: []core.DiagramEdge{edge},
			},
			want: strings.Join([]string{
				"<details>",
				"<summary>Behavior flow (each element anchored to a diff line — 3/3 anchored)</summary>",
				"",
				"```mermaid",
				"sequenceDiagram",
				"    accTitle: Refresh flow",
				"    participant n1 as Caller",
				"    participant n2 as Service",
				"    n1->>n2: request",
				"```",
				"",
				"</details>",
			}, "\n"),
		},
		{
			name: "state",
			req: &core.ReviewDiagram{
				Type: DiagramState, Nodes: []core.DiagramNode{node, leaf}, Edges: []core.DiagramEdge{edge},
			},
			want: strings.Join([]string{
				"<details>",
				"<summary>Behavior flow (each element anchored to a diff line — 3/3 anchored)</summary>",
				"",
				"```mermaid",
				"stateDiagram-v2",
				"    state \"Caller\" as n1",
				"    state \"Service\" as n2",
				"    n1 --> n2 : request",
				"```",
				"",
				"</details>",
			}, "\n"),
		},
		{
			name: "flow",
			req: &core.ReviewDiagram{
				Type: DiagramFlow, Nodes: []core.DiagramNode{node, leaf}, Edges: []core.DiagramEdge{edge},
			},
			want: strings.Join([]string{
				"<details>",
				"<summary>Behavior flow (each element anchored to a diff line — 3/3 anchored)</summary>",
				"",
				"```mermaid",
				"flowchart TD",
				"    n1[\"Caller\"]",
				"    n2[\"Service\"]",
				"    n1 -->|\"request\"| n2",
				"```",
				"",
				"</details>",
			}, "\n"),
		},
	}
	for i := range tests {
		t.Run(tests[i].name, func(t *testing.T) {
			t.Parallel()
			got := settleBlock(settledIndex(t), tests[i].req)
			if got != tests[i].want {
				t.Fatalf("Block() =\n%s\nwant\n%s", got, tests[i].want)
			}
		})
	}
}

// TestHostileLabelsAreNeutralized proves no agent byte can spell mermaid
// syntax: every injection shape the task names is supplied as a label, and
// the drawn label text must carry none of it while the block still renders.
func TestHostileLabelsAreNeutralized(t *testing.T) {
	t.Parallel()
	hostile := strings.Join([]string{
		"click n1 callback",
		"%% directive",
		"```mermaid",
		"<script>alert(1)</script>",
		"line one\nline two\r\nline three",
		"a --> b",
		"a ->> b",
		"quote \" and ' and `tick`",
		"brackets [x] {y} (z)",
		"pipe | colon : semi ;",
		"href \"javascript:alert(1)\"",
		"style n1 fill:#f00",
		"&lt;entity&gt; &amp; &#65;",
		"==> thick",
	}, " ")
	req := &core.ReviewDiagram{
		Type:  DiagramFlow,
		Title: hostile,
		Nodes: []core.DiagramNode{
			{ID: "a", Label: hostile, Evidence: verifiedAnchor(2, "func expired() bool {")},
		},
	}
	block := settleBlock(settledIndex(t), req)
	if block == "" {
		t.Fatal("Block() = \"\", want a rendered block")
	}
	label := renderedLabel(t, block)
	wantNone(t, "rendered label", label,
		"\"", "`", "%", "<", ">", "|", ";", "{", "}", "[", "]", "&", "#", "\\", "-->", "->>", "==>", "\n")
	// The label survives as readable words, so the escaping is not a blanket
	// deletion that would make the picture useless.
	wantAll(t, "rendered label", label, "click n1 callback", "line one line two line three")
	// The fixed syntax around the label is the only arrow or pipe in the block,
	// and no edge was supplied, so neither an edge pipe nor a nested fence may
	// appear.
	wantNone(t, "block", block, "-->|", "```mermaid\n    ```")
}

// renderedLabel returns the label text of the block's one flow node line.
func renderedLabel(t *testing.T, block string) string {
	t.Helper()
	const prefix = `    n1["`
	for line := range strings.SplitSeq(block, "\n") {
		if after, ok := strings.CutPrefix(line, prefix); ok {
			return strings.TrimSuffix(after, `"]`)
		}
	}
	t.Fatalf("block carries no node line:\n%s", block)
	return ""
}

// TestUnverifiedElementsAreDropped proves each unverified quote is dropped,
// that a dangling edge is dropped with the node it names, and that the counts
// report exactly what survived.
func TestUnverifiedElementsAreDropped(t *testing.T) {
	t.Parallel()
	req := &core.ReviewDiagram{
		Type: DiagramFlow,
		Nodes: []core.DiagramNode{
			{ID: "kept", Label: "Kept", Evidence: verifiedAnchor(2, "func expired() bool {")},
			{ID: "forged", Label: "Forged", Evidence: verifiedAnchor(3, "line the diff never carried")},
		},
		Edges: []core.DiagramEdge{
			{From: "kept", To: "forged", Label: "dangling", Evidence: verifiedAnchor(3, "return true")},
			{From: "kept", To: "kept", Label: "own", Evidence: verifiedAnchor(3, "return true")},
		},
	}
	res := Settle(settledIndex(t), req)
	got := res.Block()
	wantNone(t, "block", got, "Forged", "dangling")
	// One node and one self-edge survive out of two nodes and two edges: half
	// the elements verified, which is exactly the threshold that still renders.
	wantAll(t, "block", got, "2/4 anchored", `n1["Kept"]`, `n1 -->|"own"| n1`)
	wantCounts(t, &res, 4, 2, 2)
}

// TestBelowThresholdOmitsWholeDiagram proves a mostly-forged diagram leaves one
// note and no fence: half the elements have to verify before anything renders.
func TestBelowThresholdOmitsWholeDiagram(t *testing.T) {
	t.Parallel()
	req := &core.ReviewDiagram{
		Type: DiagramFlow,
		Nodes: []core.DiagramNode{
			{ID: "kept", Label: "Kept", Evidence: verifiedAnchor(2, "func expired() bool {")},
			{ID: "a", Label: "A", Evidence: verifiedAnchor(3, "forged a")},
			{ID: "b", Label: "B", Evidence: verifiedAnchor(3, "forged b")},
		},
		Edges: []core.DiagramEdge{
			{From: "kept", To: "a", Label: "x", Evidence: verifiedAnchor(3, "forged c")},
			{From: "a", To: "b", Label: "y", Evidence: verifiedAnchor(3, "forged d")},
		},
	}
	res := Settle(settledIndex(t), req)
	if want := "> Behavioral diagram omitted: 4 of 5 elements did not verify or were malformed (min 50% anchored)"; res.Block() != want {
		t.Fatalf("Block() = %q, want %q", res.Block(), want)
	}
	if strings.Contains(res.Block(), "```") {
		t.Errorf("omitted diagram still carries a fence: %q", res.Block())
	}
	if res.Summary() == nil || res.Summary().Omitted == "" {
		t.Error("Summary() carries no omission reason")
	}
}

// TestWholeDiagramOmittedWhenNothingVerifies proves the zero case names itself.
func TestWholeDiagramOmittedWhenNothingVerifies(t *testing.T) {
	t.Parallel()
	req := &core.ReviewDiagram{
		Type:  DiagramFlow,
		Nodes: []core.DiagramNode{{ID: "a", Label: "A", Evidence: verifiedAnchor(2, "nothing matches this")}},
	}
	res := Settle(settledIndex(t), req)
	if want := "> Behavioral diagram omitted: none of the 1 supplied elements anchored (each did not verify or was malformed)"; res.Block() != want {
		t.Fatalf("Block() = %q, want %q", res.Block(), want)
	}
}

// TestOutOfScopeTypeIsOmitted proves an unsupported type renders no fence and
// records no type in the receipt, so the published enum stays closed.
func TestOutOfScopeTypeIsOmitted(t *testing.T) {
	t.Parallel()
	req := &core.ReviewDiagram{
		Type:  "gantt",
		Nodes: []core.DiagramNode{{ID: "a", Label: "A", Evidence: verifiedAnchor(2, "func expired() bool {")}},
	}
	res := Settle(settledIndex(t), req)
	if want := "> Behavioral diagram omitted: type `gantt` is out of scope (want sequence, state, flow)"; res.Block() != want {
		t.Fatalf("Block() = %q, want %q", res.Block(), want)
	}
	if res.Type != "" {
		t.Errorf("Type = %q, want empty so the receipt never publishes an out-of-scope enum", res.Type)
	}
	if got := res.Summary(); got == nil || got.Type != "" || got.Omitted == "" {
		t.Errorf("Summary() = %+v, want an omitted receipt with no type", got)
	}
}

// TestCapsTruncateAndStayInsideTheByteCeiling proves the node and edge caps and
// the byte ceiling hold, that the truncation is named inside the fence, and
// that no rendered block outgrows [Caps].Bytes.
func TestCapsTruncateAndStayInsideTheByteCeiling(t *testing.T) {
	t.Parallel()
	req := &core.ReviewDiagram{Type: DiagramSequence}
	for i := range Caps.Nodes + 6 {
		req.Nodes = append(req.Nodes, core.DiagramNode{
			ID:       "n" + strings.Repeat("x", i%3) + string(rune('a'+i)),
			Label:    strings.Repeat("label ", 40),
			Evidence: verifiedAnchor(2, "func expired() bool {"),
		})
	}
	for i := range Caps.Edges + 10 {
		req.Edges = append(req.Edges, core.DiagramEdge{
			From:     req.Nodes[i%3].ID,
			To:       req.Nodes[(i+1)%3].ID,
			Label:    strings.Repeat("edge ", 40),
			Evidence: verifiedAnchor(3, "return true"),
		})
	}
	res := Settle(settledIndex(t), req)
	block := res.Block()
	if len(block) > Caps.Bytes {
		t.Fatalf("Block() = %d bytes, over the %d-byte ceiling", len(block), Caps.Bytes)
	}
	wantAll(t, "truncated block", block, diagramNotePrefix, diagramNoteSuffix)
	wantAtMost(t, "drawn nodes", strings.Count(block, "    participant "), Caps.Nodes)
	wantAtMost(t, "drawn edges", strings.Count(block, "->>"), Caps.Edges)
	// The caps truncate drawing, never verification: all 30 nodes and 50
	// edges stay counted as verified.
	wantCounts(t, &res, 80, 80, 0)
}

// TestNoDiagramSuppliedChangesNothing proves the zero value renders no byte and
// publishes no receipt: an absent diagram must not move any body.
func TestNoDiagramSuppliedChangesNothing(t *testing.T) {
	t.Parallel()
	res := Settle(settledIndex(t), nil)
	if res.Block() != "" {
		t.Errorf("Block() = %q, want empty", res.Block())
	}
	if res.Summary() != nil {
		t.Errorf("Summary() = %+v, want nil", res.Summary())
	}
	if got := res.Status(); got.State != StatusAbsent {
		t.Errorf("Status().State = %q, want %q", got.State, StatusAbsent)
	}
}

// TestRepeatedNodeIDKeepsTheFirst proves one agent id names one box: a second
// node with the same id is dropped instead of producing a duplicate actor.
func TestRepeatedNodeIDKeepsTheFirst(t *testing.T) {
	t.Parallel()
	req := &core.ReviewDiagram{
		Type: DiagramFlow,
		Nodes: []core.DiagramNode{
			{ID: "a", Label: "First", Evidence: verifiedAnchor(2, "func expired() bool {")},
			{ID: "a", Label: "Second", Evidence: verifiedAnchor(3, "return true")},
		},
	}
	res := Settle(settledIndex(t), req)
	got := res.Block()
	if strings.Contains(got, "Second") {
		t.Errorf("repeated id drew the later node:\n%s", got)
	}
	if !strings.Contains(got, "n1[\"First\"]") {
		t.Errorf("first node with the repeated id is missing:\n%s", got)
	}
	if res.Supplied != 2 || res.Verified != 1 {
		t.Errorf("counts = %d/%d, want supplied 2, verified 1", res.Supplied, res.Verified)
	}
}

// TestStatusSettlesEachDisposition pins what post.json publishes.
func TestStatusSettlesEachDisposition(t *testing.T) {
	t.Parallel()
	rendered := Settle(settledIndex(t), &core.ReviewDiagram{
		Type:  DiagramFlow,
		Nodes: []core.DiagramNode{{ID: "a", Label: "A", Evidence: verifiedAnchor(2, "func expired() bool {")}},
	})
	if got := rendered.Status(); got.State != StatusRendered || got.Type != DiagramFlow ||
		got.Detail != "1/1 elements anchored" || got.Supplied != 1 || got.Verified != 1 {
		t.Errorf("rendered Status() = %+v", got)
	}
	omitted := Settle(settledIndex(t), &core.ReviewDiagram{
		Type:  DiagramFlow,
		Nodes: []core.DiagramNode{{ID: "a", Label: "A", Evidence: verifiedAnchor(2, "no such line")}},
	})
	if got := omitted.Status(); got.State != StatusOmitted || got.Dropped != 1 || got.Detail == "" {
		t.Errorf("omitted Status() = %+v", got)
	}
}

// TestStatusPostMatchesTheReceipt proves the dry-run projection carries
// exactly the receipt's fields plus the state, for a drawn and an omitted
// block: the omission reason and the title included.
func TestStatusPostMatchesTheReceipt(t *testing.T) {
	t.Parallel()
	for _, req := range []*core.ReviewDiagram{
		{Type: DiagramFlow, Title: "Refresh", Nodes: []core.DiagramNode{{ID: "a", Label: "A", Evidence: verifiedAnchor(2, "func expired() bool {")}}},
		{Type: DiagramFlow, Title: "Refresh", Nodes: []core.DiagramNode{{ID: "a", Label: "A", Evidence: verifiedAnchor(2, "no such line")}}},
	} {
		res := Settle(settledIndex(t), req)
		st := res.Status()
		post, receipt := st.Post(), res.Summary()
		if post == nil || receipt == nil {
			t.Fatalf("Post() = %+v, Summary() = %+v, want both", post, receipt)
		}
		want := core.PostDiagram{
			Status: st.State, Type: receipt.Type, Title: receipt.Title, Supplied: receipt.Supplied,
			Verified: receipt.Verified, Dropped: receipt.Dropped, Omitted: receipt.Omitted,
		}
		if *post != want || post.Title != "Refresh" || (post.Status == StatusOmitted) == (post.Omitted == "") {
			t.Errorf("Post() = %+v, want the receipt plus state %+v", *post, want)
		}
	}
}

// TestNoteReservationCoversTheWidestNote proves the bytes the block reserves
// before it knows its counts cover the widest note any counts can produce:
// all three counts at the widest int, nodes, edges, and orphaned edges.
func TestNoteReservationCoversTheWidestNote(t *testing.T) {
	t.Parallel()
	widest := len(newline + mermaidIndent + diagramNote(math.MaxInt, math.MaxInt, math.MaxInt))
	if widest > maxDiagramNoteBytes {
		t.Errorf("widest note = %d bytes, reservation = %d", widest, maxDiagramNoteBytes)
	}
}

// TestSummaryNeverExceedsSupplied proves the receipt's arithmetic is honest:
// verified plus dropped is always what the review supplied.
func TestSummaryNeverExceedsSupplied(t *testing.T) {
	t.Parallel()
	req := &core.ReviewDiagram{
		Type: DiagramSequence,
		Nodes: []core.DiagramNode{
			{ID: "a", Label: "A", Evidence: verifiedAnchor(2, "func expired() bool {")},
			{ID: "b", Label: "B", Evidence: verifiedAnchor(3, "forged")},
		},
		Edges: []core.DiagramEdge{{From: "a", To: "b", Label: "x", Evidence: verifiedAnchor(3, "return true")}},
	}
	got := settleSummary(settledIndex(t), req)
	if got == nil {
		t.Fatal("Summary() = nil, want a receipt")
	}
	if got.Verified+got.Dropped != got.Supplied {
		t.Errorf("receipt = %+v, want verified+dropped == supplied", got)
	}
	// The forged node drops, and the edge naming it dangles with it: one node of
	// three elements survives.
	if got.Supplied != 3 || got.Verified != 1 || got.Dropped != 2 {
		t.Errorf("receipt = %+v, want 3 supplied, 1 verified, 2 dropped", got)
	}
}

// safeText is the exact label alphabet the docs promise: letters, digits,
// combining marks, spaces, and labelPunctuation — an allowlist, not a deny
// list, so a zero-width rune, an ellipsis, or any other stray character fails
// the grammar. None of these closes a quote, opens a directive or comment,
// spells an arrow or a pipe, starts an entity, or breaks the line.
const safeText = `[\p{L}\p{N}\p{M} .,:!?'()+_/@-]*`

// capNote is the cap-truncation comment in every type's grammar.
const capNote = `^    %% (([0-9]+ more (nodes|edges)( and [0-9]+ more edges)? omitted \(input order\))` +
	`(; [0-9]+ edges to omitted nodes not drawn)?|[0-9]+ edges to omitted nodes not drawn)$`

// lineGrammar is every line shape each type's fence may hold, after the
// declaration. Identifiers are generated (`n<digits>`), so none of them is an
// agent byte.
var lineGrammar = map[string][]*regexp.Regexp{
	DiagramSequence: {
		regexp.MustCompile(`^    accTitle: ` + safeText + `$`),
		regexp.MustCompile(`^    participant n[0-9]+ as ` + safeText + `$`),
		regexp.MustCompile(`^    n[0-9]+->>n[0-9]+: ` + safeText + `$`),
		regexp.MustCompile(capNote),
	},
	DiagramState: {
		regexp.MustCompile(`^    accTitle: ` + safeText + `$`),
		regexp.MustCompile(`^    state "` + safeText + `" as n[0-9]+$`),
		regexp.MustCompile(`^    n[0-9]+ --> n[0-9]+( : ` + safeText + `)?$`),
		regexp.MustCompile(capNote),
	},
	DiagramFlow: {
		regexp.MustCompile(`^    accTitle: ` + safeText + `$`),
		regexp.MustCompile(`^    n[0-9]+\["` + safeText + `"\]$`),
		regexp.MustCompile(`^    n[0-9]+ -->(\|"` + safeText + `"\|)? n[0-9]+$`),
		regexp.MustCompile(capNote),
	},
}

// assertBlockGrammar proves a rendered block is exactly the collapsed region,
// the one fence, the fixed declaration, and lines of the type's grammar —
// nothing an agent supplied can add a statement, a second fence, or markup
// outside the fence.
func assertBlockGrammar(t *testing.T, kind, block string) {
	t.Helper()
	for _, line := range blockBody(t, kind, block) {
		if !slices.ContainsFunc(lineGrammar[kind], func(re *regexp.Regexp) bool { return re.MatchString(line) }) {
			t.Errorf("%s line outside the grammar: %q", kind, line)
		}
	}
}

// blockBody checks the fixed preamble and close around a rendered block and
// returns the statement lines between them.
func blockBody(t *testing.T, kind, block string) []string {
	t.Helper()
	lines := strings.Split(block, "\n")
	head := []string{"<details>", "", "```mermaid", declaration(kind)}
	tail := []string{"```", "", "</details>"}
	if len(lines) < len(head)+1+len(tail) {
		t.Fatalf("block is too short to be a rendered diagram:\n%s", block)
	}
	summary := strings.HasPrefix(lines[1], "<summary>Behavior flow (each element anchored to a diff line — ")
	if lines[0] != head[0] || !summary || !slices.Equal(lines[2:5], head[1:]) {
		t.Fatalf("block head is not the fixed preamble:\n%s", block)
	}
	if !slices.Equal(lines[len(lines)-len(tail):], tail) {
		t.Fatalf("block tail is not the fixed close:\n%s", block)
	}
	return lines[5 : len(lines)-len(tail)]
}

// hostileInputs is every injection shape the label sanitizer has to defeat,
// each one a whole label on its own.
var hostileInputs = []string{
	"click n1 call alert()",
	"click n1 href \"javascript:alert(1)\"",
	"%%{init: {'theme':'dark'}}%%",
	"%% comment",
	"```\n```mermaid\ngraph TD",
	"</details><script>alert(1)</script>",
	"<img src=x onerror=alert(1)>",
	"a --> b",
	"a ->> b",
	"a -.-> b ==> c --- d ~~~ e",
	"x\"]; click n1 evil; n9[\"y",
	"quote \" and ' and `tick`",
	"brackets [x] {y} (z)",
	"pipe | colon : semi ;",
	"style n1 fill:#f00",
	"&lt;entity&gt; &amp; &#65; #quot; #59;",
	"line one\nline two\r\nline three four",
	"end",
	"\\n escaped \\\"",
	"<br/>break",
}

// TestHostileInputKeepsTheGrammar drives every hostile shape through every
// rendered position — title, node label, edge label, and node id — for each
// type, and proves every line still parses as the renderer's own grammar.
func TestHostileInputKeepsTheGrammar(t *testing.T) {
	t.Parallel()
	for _, kind := range Types() {
		for i, hostile := range hostileInputs {
			t.Run(kind+"/"+strconv.Itoa(i), func(t *testing.T) {
				t.Parallel()
				checkHostileGrammar(t, kind, hostile)
			})
		}
	}
}

// checkHostileGrammar puts one hostile string in every agent-supplied slot of
// one diagram type and checks the block still parses as its grammar.
func checkHostileGrammar(t *testing.T, kind, hostile string) {
	t.Helper()
	req := &core.ReviewDiagram{
		Type:  kind,
		Title: hostile,
		Nodes: []core.DiagramNode{
			{ID: hostile, Label: hostile, Evidence: verifiedAnchor(2, "func expired() bool {")},
			{ID: "b", Label: hostile, Evidence: verifiedAnchor(3, "return true")},
		},
		Edges: []core.DiagramEdge{
			{From: hostile, To: "b", Label: hostile, Evidence: verifiedAnchor(3, "return true")},
		},
	}
	res := Settle(settledIndex(t), req)
	// A hostile label must never cost an element: all three verify.
	wantCounts(t, &res, 3, 3, 0)
	assertBlockGrammar(t, kind, res.Block())
}

// TestHostileTypeNoteStaysOneLine proves the out-of-scope note, the one place
// an agent's type string reaches the body, stays one inert blockquote line.
func TestHostileTypeNoteStaysOneLine(t *testing.T) {
	t.Parallel()
	for _, hostile := range append(slices.Clone(hostileInputs), "https://evil.example/x", "@alice", "www.evil.example") {
		block := settleBlock(settledIndex(t), &core.ReviewDiagram{Type: hostile})
		if strings.Count(block, "\n") != 0 || !strings.HasPrefix(block, omittedPrefix) {
			t.Errorf("type %q note = %q, want one omission line", hostile, block)
		}
		checkInertTypeNote(t, hostile, strings.TrimPrefix(block, omittedPrefix))
	}
}

// checkInertTypeNote proves an out-of-scope type reaches the body only inside
// one code span of allowlisted runes, where GitHub links no URL and pings no
// mention.
func checkInertTypeNote(t *testing.T, hostile, reason string) {
	t.Helper()
	const want = " is out of scope (want sequence, state, flow)"
	if reason == "the supplied type"+want {
		return // the alphabet emptied it: nothing of the agent's reaches the body
	}
	shown, ok := strings.CutPrefix(reason, "type `")
	shown, closed := strings.CutSuffix(shown, "`"+want)
	if !ok || !closed || shown == "" {
		t.Fatalf("type %q reason = %q, want the value in one code span", hostile, reason)
	}
	if !regexp.MustCompile(`^` + safeText + `$`).MatchString(shown) {
		t.Errorf("type %q shows runes outside the label alphabet: %q", hostile, shown)
	}
}

// TestURLTypeRendersInert pins the exact note for a URL-shaped type: the URL
// sits in a code span, so the public comment carries no link.
func TestURLTypeRendersInert(t *testing.T) {
	t.Parallel()
	got := settleBlock(settledIndex(t), &core.ReviewDiagram{Type: "https://evil.example/x"})
	const want = "> Behavioral diagram omitted: type `https://evil.example/x` is out of scope (want sequence, state, flow)"
	if got != want {
		t.Errorf("Block() = %q, want %q", got, want)
	}
}

// TestLabelsStayInTheAlphabet proves the documented contract rune by rune:
// whatever goes in, every rune safeLabel returns is a letter, digit,
// combining mark, space, or labelPunctuation — a mention gains no U+200B and
// a capped label ends in allowlisted dots, never U+2026.
func TestLabelsStayInTheAlphabet(t *testing.T) {
	t.Parallel()
	inputs := append(slices.Clone(hostileInputs),
		"@alice please look", "ping @org/team", strings.Repeat("word ", 60),
		strings.Repeat("\U00020000", 200), "zero\u200bwidth\u2060joiner\ufeff", "tab\tsep")
	for _, in := range inputs {
		for _, maxRunes := range []int{Caps.LabelRunes, Caps.IDRunes, 5} {
			checkLabelAlphabet(t, in, maxRunes)
		}
	}
	if got := safeLabel("@alice", Caps.LabelRunes); got != "@alice" {
		t.Errorf("safeLabel(@alice) = %q, want it unchanged: a fenced mention is inert", got)
	}
	if got := safeLabel(strings.Repeat("a", 130), Caps.LabelRunes); got != strings.Repeat("a", 117)+"..." {
		t.Errorf("capped label = %q, want 117 runes and three dots", got)
	}
}

// TestOrphanedEdgesAreCounted proves an edge whose endpoint the node cap
// dropped is announced in the note instead of vanishing: 30 verified nodes
// keep 24, and the three edges into node k27 are named as not drawn.
func TestOrphanedEdgesAreCounted(t *testing.T) {
	t.Parallel()
	req := &core.ReviewDiagram{Type: DiagramFlow}
	for i := range Caps.Nodes + 6 {
		req.Nodes = append(req.Nodes, core.DiagramNode{
			ID: "k" + strconv.Itoa(i), Label: "N" + strconv.Itoa(i), Evidence: verifiedAnchor(2, "func expired() bool {"),
		})
	}
	for _, to := range []string{"k1", "k27", "k2", "k27", "k28"} {
		req.Edges = append(req.Edges, core.DiagramEdge{From: "k0", To: to, Evidence: verifiedAnchor(3, "return true")})
	}
	res := Settle(settledIndex(t), req)
	block := res.Block()
	assertBlockGrammar(t, DiagramFlow, block)
	wantAll(t, "block", block,
		"    n1 --> n2\n    n1 --> n3\n    %% 6 more nodes omitted (input order); 3 edges to omitted nodes not drawn\n```")
	wantCounts(t, &res, 35, 35, 0)
}

// TestEmptiedLabelShowsTheGeneratedID proves a label the sanitizer reduces to
// nothing still renders a well-formed declaration: an empty alias would break
// the sequence grammar.
func TestEmptiedLabelShowsTheGeneratedID(t *testing.T) {
	t.Parallel()
	req := &core.ReviewDiagram{
		Type: DiagramSequence,
		Nodes: []core.DiagramNode{
			{ID: "a", Label: "<<>>|;`%", Evidence: verifiedAnchor(2, "func expired() bool {")},
		},
	}
	block := settleBlock(settledIndex(t), req)
	if !strings.Contains(block, "\n    participant n1 as n1\n") {
		t.Errorf("emptied label did not fall back to the generated id:\n%s", block)
	}
}

// TestNodeWithoutIDIsDropped proves an id-less node is never drawn and never
// lets an id-less edge attach to it.
func TestNodeWithoutIDIsDropped(t *testing.T) {
	t.Parallel()
	req := &core.ReviewDiagram{
		Type: DiagramFlow,
		Nodes: []core.DiagramNode{
			{ID: "", Label: "Anonymous", Evidence: verifiedAnchor(2, "func expired() bool {")},
			{ID: "a", Label: "Named", Evidence: verifiedAnchor(3, "return true")},
		},
		Edges: []core.DiagramEdge{{From: "", To: "a", Label: "ghost", Evidence: verifiedAnchor(3, "return true")}},
	}
	res := Settle(settledIndex(t), req)
	if got := res.Block(); strings.Contains(got, "Anonymous") || strings.Contains(got, "ghost") {
		t.Errorf("id-less elements were drawn:\n%s", got)
	}
	if res.Supplied != 3 || res.Verified != 1 || res.Dropped != 2 {
		t.Errorf("counts = %d/%d/%d, want supplied 3, verified 1, dropped 2", res.Supplied, res.Verified, res.Dropped)
	}
}

// TestThresholdBoundary pins the constant from both sides: exactly half the
// elements verified renders, one fewer is omitted with the note.
func TestThresholdBoundary(t *testing.T) {
	t.Parallel()
	ok := verifiedAnchor(2, "func expired() bool {")
	bad := verifiedAnchor(3, "forged")
	at := &core.ReviewDiagram{Type: DiagramFlow, Nodes: []core.DiagramNode{
		{ID: "a", Label: "A", Evidence: ok},
		{ID: "b", Label: "B", Evidence: ok},
		{ID: "c", Label: "C", Evidence: bad},
		{ID: "d", Label: "D", Evidence: bad},
	}}
	if res := Settle(settledIndex(t), at); res.Omitted != "" || res.Verified != 2 || res.Supplied != 4 {
		t.Errorf("2/4 = %+v, want rendered at exactly %d%%", res, MinVerifiedPercent)
	}
	below := &core.ReviewDiagram{Type: DiagramFlow, Nodes: append(slices.Clone(at.Nodes),
		core.DiagramNode{ID: "e", Label: "E", Evidence: bad})}
	const want = "> Behavioral diagram omitted: 3 of 5 elements did not verify or were malformed (min 50% anchored)"
	if got := settleBlock(settledIndex(t), below); got != want {
		t.Errorf("2/5 Block() = %q, want %q", got, want)
	}
}

// TestCapsDrawExactlyTheHead pins the node and edge caps with labels small
// enough that the byte ceiling never binds: the first Caps.Nodes nodes and
// Caps.Edges edges render, and the note counts the rest exactly.
func TestCapsDrawExactlyTheHead(t *testing.T) {
	t.Parallel()
	req := &core.ReviewDiagram{Type: DiagramFlow}
	for i := range Caps.Nodes + 6 {
		req.Nodes = append(req.Nodes, core.DiagramNode{
			ID: "k" + strconv.Itoa(i), Label: "N" + strconv.Itoa(i), Evidence: verifiedAnchor(2, "func expired() bool {"),
		})
	}
	for i := range Caps.Edges + 10 {
		req.Edges = append(req.Edges, core.DiagramEdge{
			From: "k0", To: "k" + strconv.Itoa(1+i%3), Evidence: verifiedAnchor(3, "return true"),
		})
	}
	block := settleBlock(settledIndex(t), req)
	assertBlockGrammar(t, DiagramFlow, block)
	if got := strings.Count(block, `"]`+"\n"); got != Caps.Nodes {
		t.Errorf("drew %d nodes, want exactly the %d-node cap", got, Caps.Nodes)
	}
	if got := strings.Count(block, " --> "); got != Caps.Edges {
		t.Errorf("drew %d edges, want exactly the %d-edge cap", got, Caps.Edges)
	}
	if !strings.Contains(block, "\n    %% 6 more nodes and 10 more edges omitted (input order)\n") {
		t.Errorf("cap note does not count the truncation exactly:\n%s", block)
	}
}

// TestOversizedGraphNeverCrossesTheCeiling floods every field with four-byte
// runes at and past each cap, for every type, and proves the block never
// panics, never outgrows Caps.Bytes, and still parses as its grammar.
func TestOversizedGraphNeverCrossesTheCeiling(t *testing.T) {
	t.Parallel()
	for _, kind := range Types() {
		checkOversized(t, kind)
	}
}

// checkOversized floods one diagram type with 500 nodes and 2,000 edges whose
// every field is far past its rune cap. U+20000 is a four-byte letter, so the
// sanitizer keeps every rune and each capped label still costs 480 bytes: the
// node and edge caps alone would overrun the ceiling, and only the byte
// budget keeps the block inside it. The emoji is a symbol the allowlist drops.
func checkOversized(t *testing.T, kind string) {
	t.Helper()
	wide := strings.Repeat("\U00020000\U0001F600", 400)
	req := &core.ReviewDiagram{Type: kind, Title: wide}
	for i := range 500 {
		req.Nodes = append(req.Nodes, core.DiagramNode{
			ID: strconv.Itoa(i) + wide, Label: wide, Evidence: verifiedAnchor(2, "func expired() bool {"),
		})
	}
	for i := range 2000 {
		req.Edges = append(req.Edges, core.DiagramEdge{
			From: strconv.Itoa(i%500) + wide, To: strconv.Itoa((i*7)%500) + wide,
			Label: wide, Evidence: verifiedAnchor(3, "return true"),
		})
	}
	res := Settle(settledIndex(t), req)
	block := res.Block()
	wantAtMost(t, kind+" block bytes", len(block), Caps.Bytes)
	assertBlockGrammar(t, kind, block)
	// The caps truncate drawing, never verification.
	wantCounts(t, &res, 2500, 2500, 0)
	wantAll(t, kind+" block", block, "more nodes")
}

// TestUnreadableAndUntypedDiagramsOmitWithANote proves the two shapes with
// nothing to draw still leave one honest line instead of silence.
func TestUnreadableAndUntypedDiagramsOmitWithANote(t *testing.T) {
	t.Parallel()
	unreadable := Unreadable()
	if got, want := unreadable.Block(), "> Behavioral diagram omitted: the diagram value is not an object"; got != want {
		t.Errorf("Unreadable().Block() = %q, want %q", got, want)
	}
	res := Settle(settledIndex(t), &core.ReviewDiagram{
		Nodes: []core.DiagramNode{{ID: "a", Label: "A", Evidence: verifiedAnchor(2, "func expired() bool {")}},
	})
	if want := "> Behavioral diagram omitted: no type was supplied (want sequence, state, flow)"; res.Block() != want {
		t.Errorf("untyped Block() = %q, want %q", res.Block(), want)
	}
	if res.Supplied != 1 || res.Dropped != 1 || res.Verified != 0 {
		t.Errorf("untyped counts = %+v, want the element counted and dropped", res.Summary())
	}
}

// settleBlock settles one request and renders its block.
func settleBlock(idx *evidence.DiffIndex, req *core.ReviewDiagram) string {
	res := Settle(idx, req)
	return res.Block()
}

// settleSummary settles one request and returns its receipt.
func settleSummary(idx *evidence.DiffIndex, req *core.ReviewDiagram) *core.EvidenceDiagram {
	res := Settle(idx, req)
	return res.Summary()
}

// wantNone fails the test for every banned fragment text carries.
func wantNone(t *testing.T, what, text string, banned ...string) {
	t.Helper()
	for _, b := range banned {
		if strings.Contains(text, b) {
			t.Errorf("%s carries %q:\n%s", what, b, text)
		}
	}
}

// wantAll fails the test for every wanted fragment text lacks.
func wantAll(t *testing.T, what, text string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(text, w) {
			t.Errorf("%s is missing %q:\n%s", what, w, text)
		}
	}
}

// wantAtMost fails the test when got passes limit.
func wantAtMost(t *testing.T, what string, got, limit int) {
	t.Helper()
	if got > limit {
		t.Errorf("%s = %d, over the limit %d", what, got, limit)
	}
}

// wantCounts pins the three receipt counts of one settled result.
func wantCounts(t *testing.T, res *Result, supplied, verified, dropped int) {
	t.Helper()
	if res.Supplied != supplied || res.Verified != verified || res.Dropped != dropped {
		t.Errorf("counts = supplied %d, verified %d, dropped %d; want %d, %d, %d",
			res.Supplied, res.Verified, res.Dropped, supplied, verified, dropped)
	}
}

// checkLabelAlphabet proves one safeLabel result holds only alphabet runes
// and stays inside its rune cap.
func checkLabelAlphabet(t *testing.T, in string, maxRunes int) {
	t.Helper()
	got := safeLabel(in, maxRunes)
	if i := strings.IndexFunc(got, func(r rune) bool { return !labelRune(r) }); i >= 0 {
		r, _ := utf8.DecodeRuneInString(got[i:])
		t.Errorf("safeLabel(%q, %d) = %q carries %U outside the alphabet", in, maxRunes, got, r)
	}
	wantAtMost(t, "safeLabel runes", utf8.RuneCountInString(got), maxRunes)
}
