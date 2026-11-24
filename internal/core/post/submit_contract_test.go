package postcore

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/diagram"
	"github.com/stainless-code/verdict/internal/jq"
)

func postFailCommander(t *testing.T, res scriptResult) *scriptCommander {
	t.Helper()
	cmdr := postCommander(t)
	cmdr.out["gh\x00api\x00repos/acme/app/pulls/7/reviews\x00--method\x00POST\x00--input\x00-\x00--jq\x00"+postJQFilter] = res
	return cmdr
}

// TestRunPostFailedCarriesStderrHead proves the typed post-failed refusal
// quotes the forge's own stderr (flattened) instead of an empty reason, and
// caps it at the documented 300 bytes.
//
//nolint:gocognit,revive // legs assert the refusal, receipt, and projection fields inline
func TestRunPostFailedCarriesStderrHead(t *testing.T) {
	cases := []struct {
		name   string
		stderr string
		want   string
		absent string
	}{
		{
			name:   "two-lines-flatten-to-one",
			stderr: "HTTP 422: Validation Failed\nbody is too long\n",
			want: "post failed: HTTP 422: Validation Failed body is too long  — narrow the cause " +
				"(anchor drift? body size?) — do not drop comments blindly.",
		},
		{
			name:   "over-300-bytes-truncates",
			stderr: strings.Repeat("x", 300) + "TAIL-THAT-MUST-NOT-RIDE",
			want:   "post failed: " + strings.Repeat("x", 300) + " — narrow the cause",
			absent: "TAIL-THAT-MUST-NOT-RIDE",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			postBundle(t, postReview)
			cmdr := postFailCommander(t, scriptResult{stderr: tc.stderr, fail: true})
			var warnings, human []string
			req := postTestReq(cmdr, Flags{Approve: "false", ReviewFile: ".verdict/state/7/review.json"}, &warnings, &human)
			_, ref := Run(context.Background(), req)
			if ref == nil || ref.Code != codePostFailed {
				t.Fatalf("ref = %v, want post-failed", ref)
			}
			if !strings.Contains(ref.Message, tc.want) {
				t.Errorf("message = %q, want it to carry %q", ref.Message, tc.want)
			}
			if tc.absent != "" && strings.Contains(ref.Message, tc.absent) {
				t.Errorf("message carries the bytes past the 300-byte head: %q", ref.Message)
			}
			if exists(".verdict/state/7/post.json") {
				t.Error("a failed post wrote the receipt")
			}
			if matches := globMarkers(t); len(matches) != 0 {
				t.Errorf("a failed post armed the duplicate marker: %v", matches)
			}
		})
	}
}

func writeProvenanceFixture(t *testing.T) {
	t.Helper()
	if err := writeTestFile("package.json", `{"version":"3.1.4"}`); err != nil {
		t.Fatal(err)
	}
	if err := writeTestFile("SKILL.md", "# Verdict review skill\n"); err != nil {
		t.Fatal(err)
	}
	if err := writeTestFile(".verdict/state/7/cost.json",
		`[{"schema_version":"1","effort":"standard","models":[{"name":"gpt-5-codex"}],"est_usd":0.05}]`); err != nil {
		t.Fatal(err)
	}
}

// TestRunReceiptProvenanceTriple proves post.json carries the bundle's
// provenance triple (version, prompt-set hash, cost model) instead of empty
// fields, and the real-post envelope carries the same triple.
//
//nolint:revive // legs assert the refusal, receipt, and projection fields inline
func TestRunReceiptProvenanceTriple(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	postBundle(t, postReview)
	writeProvenanceFixture(t)
	cmdr := postCommander(t)
	var warnings, human []string
	req := postTestReq(cmdr, Flags{Approve: "false", ReviewFile: ".verdict/state/7/review.json"}, &warnings, &human)
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	receipt := decodePostedPayload(t, readTestFile(t, ".verdict/state/7/post.json"))
	if got := receipt["tool_version"]; got != "3.1.4" {
		t.Errorf("post.json tool_version = %v, want 3.1.4", got)
	}
	hash, isString := receipt["prompts_hash"].(string)
	if !isString || len(hash) != 64 || strings.Trim(hash, "0123456789abcdef") != "" {
		t.Errorf("post.json prompts_hash = %q, want the 64-hex prompt-set digest", hash)
	}
	if got := receipt["model"]; got != "gpt-5-codex" {
		t.Errorf("post.json model = %v, want the cost record's model", got)
	}
	if got := resp.Data.Provenance.ToolVersion; got != "3.1.4" {
		t.Errorf("envelope provenance tool_version = %q, want 3.1.4", got)
	}
	if got := resp.Data.Provenance.PromptsHash; got != hash {
		t.Errorf("envelope provenance prompts_hash = %q, want the receipt's %q", got, hash)
	}
	if model := resp.Data.Provenance.Model; model == nil || *model != "gpt-5-codex" {
		t.Errorf("envelope provenance model = %v, want gpt-5-codex", model)
	}
}

// TestRunSuccessNextLine proves the real-post envelope's next line follows
// the open-finding count in both directions.
//
//nolint:revive // legs assert the refusal, receipt, and projection fields inline
func TestRunSuccessNextLine(t *testing.T) {
	cases := []struct {
		name   string
		review string
		want   string
	}{
		{name: "one-open-address", review: postReview, want: "address open threads"},
		{
			name:   "none-open-nothing-to-address",
			review: `{"schema_version":"1","body":"> ✅ Clean.","approved":false,"comments":[]}`,
			want:   "none open — nothing to address",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			postBundle(t, tc.review)
			cmdr := postCommander(t)
			var warnings, human []string
			req := postTestReq(cmdr, Flags{Approve: "false", ReviewFile: ".verdict/state/7/review.json"}, &warnings, &human)
			resp, ref := Run(context.Background(), req)
			if ref != nil {
				t.Fatalf("Run refusal: %v", ref)
			}
			if resp.Next != tc.want {
				t.Errorf("next = %q, want %q", resp.Next, tc.want)
			}
			if !strings.Contains(strings.Join(human, "\n"), "next: "+tc.want) {
				t.Errorf("human next line = %q, want %q", human, "next: "+tc.want)
			}
		})
	}
}

// TestPostRetryWarningNumbering proves the transient-error warning counts the
// attempt that is about to be retried (1 then 2 of 2).
func TestPostRetryWarningNumbering(t *testing.T) {
	t.Parallel()
	limited := retryResult{stderr: "HTTP 429: secondary rate limit exceeded, retry-after: 0", fail: true}
	cmdr := &retryCommander{t: t, results: []retryResult{
		limited, limited,
		{stdout: `{"id":101,"state":"commented"}`},
	}}
	var warnings []string
	st := postTestState(t.TempDir(), "", Flags{}, &warnings)
	posted, ref := st.postOneWithRetry(context.Background(), cmdr, "{}")
	if ref != nil {
		t.Fatalf("retry fell through to a refusal: %v", ref)
	}
	if posted != `{"id":101,"state":"commented"}` {
		t.Errorf("posted = %q, want the third attempt's body", posted)
	}
	want := []string{
		"transient GitHub error — retry 1/2, comments untouched",
		"transient GitHub error — retry 2/2, comments untouched",
	}
	if strings.Join(st.warnings, "\n") != strings.Join(want, "\n") {
		t.Errorf("warnings = %q, want %q", st.warnings, want)
	}
}

// TestCountRecheckOpenOnlyExplicitFalse proves the live-thread count follows
// jq's `select(.isResolved == false)`: resolved nodes and nodes without the
// key never count, and a non-object node errors the read.
func TestCountRecheckOpenOnlyExplicitFalse(t *testing.T) {
	t.Parallel()
	nodes := []any{
		map[string]any{"isResolved": false},
		map[string]any{"isResolved": false},
		map[string]any{"isResolved": true},
		map[string]any{"id": 7},
	}
	n, err := countRecheckOpen(nodes)
	if err != nil {
		t.Fatalf("countRecheckOpen: %v", err)
	}
	if n != 2 {
		t.Errorf("open = %d, want 2 (only the explicit falses)", n)
	}
	if _, err := countRecheckOpen([]any{map[string]any{"isResolved": false}, "not an object"}); err == nil {
		t.Error("a non-object node counted instead of erroring the read")
	}
}

// TestPostCountsTally proves the preflight/envelope tallies count each status
// exactly once over the merged entries.
func TestPostCountsTally(t *testing.T) {
	t.Parallel()
	entries := []any{
		jq.NewObj("status", "open"),
		jq.NewObj("status", "open"),
		jq.NewObj("status", "deferred"),
		jq.NewObj("status", "addressed"),
		jq.NewObj("status", "addressed"),
		jq.NewObj("status", "addressed"),
	}
	open, deferred, addressed := postCounts(entries)
	if open != 2 || deferred != 1 || addressed != 3 {
		t.Errorf("counts = %d open, %d deferred, %d addressed; want 2, 1, 3", open, deferred, addressed)
	}
	if n := countByStatus([]any{jq.NewObj("status", "open"), jq.NewObj("kind", "section")}, "open"); n != 1 {
		t.Errorf("countByStatus = %d, want the status-bearing entry only", n)
	}
}

// TestTypedPostItemsProjection proves the typed envelope summary carries each
// entry's sig, status, anchored flag, and locator, and skips non-objects.
//
//nolint:revive // legs assert the refusal, receipt, and projection fields inline
func TestTypedPostItemsProjection(t *testing.T) {
	t.Parallel()
	entries := []any{
		jq.NewObj("sig", strings.Repeat("a", 40), "status", "open", "anchored", true,
			"path", "notes.txt", "line", 2, "side", "RIGHT"),
		jq.NewObj("sig", strings.Repeat("b", 40), "status", "addressed", "anchored", false,
			"path", "notes.txt", "line", 5, "side", "LEFT"),
		jq.NewObj("kind", "section", "heading", "### X"),
		"not an object",
	}
	items := typedPostItems(entries)
	if len(items) != 3 {
		t.Fatalf("items = %d, want the three object entries: %+v", len(items), items)
	}
	if items[0].Sig != strings.Repeat("a", 40) || items[0].Status != "open" || items[0].Anchored != true {
		t.Errorf("items[0] = %+v, want the first entry's scalars", items[0])
	}
	if items[1].Anchored != false || items[1].Status != "addressed" {
		t.Errorf("items[1] = %+v, want the second entry's scalars", items[1])
	}
	if items[0].Locator != "notes.txt:2" || items[1].Locator != "notes.txt:5" {
		t.Errorf("locators = %q, %q; want the path:line anchors", items[0].Locator, items[1].Locator)
	}
	if items[2].Sig != "" || items[2].Status != "" {
		t.Errorf("items[2] = %+v, want the section entry's empty scalars", items[2])
	}
}

// TestPostDiagramValue proves the dry-run diagram disposition: absent reads
// null, and present carries the state, the type, and the settled counts.
func TestPostDiagramValue(t *testing.T) {
	t.Parallel()
	if got := postDiagramValue(&diagram.Status{State: diagram.StatusAbsent}); got != nil {
		t.Errorf("absent diagram = %v, want nil", got)
	}
	got, ok := postDiagramValue(&diagram.Status{
		State: diagram.StatusRendered, Type: "sequence", Detail: "4/5 elements anchored",
		Supplied: 5, Verified: 4, Dropped: 1,
	}).(*jq.Obj)
	if !ok {
		t.Fatal("present diagram is not an ordered object")
	}
	for key, want := range map[string]any{
		"status": "rendered", "type": "sequence", "supplied": 5, "verified": 4,
		"dropped": 1, "detail": "4/5 elements anchored",
	} {
		if v, _ := got.Get(key); v != want {
			t.Errorf("diagram.%s = %v, want %v", key, v, want)
		}
	}
	omitted, ok := postDiagramValue(&diagram.Status{
		State: diagram.StatusOmitted, Detail: "none of the 2 supplied elements verified against the diff",
	}).(*jq.Obj)
	if !ok {
		t.Fatal("omitted diagram is not an ordered object")
	}
	if v, _ := omitted.Get("type"); v != nil {
		t.Errorf("diagram.type = %v, want null when the type was out of scope", v)
	}
}

// TestDecodeProvenanceTyped proves the typed provenance reader honors the
// triple: a missing cost record reads a nil model, a present one reads its
// model, and an unreadable bundle reads nil.
//
//nolint:revive // legs assert the refusal, receipt, and projection fields inline
func TestDecodeProvenanceTyped(t *testing.T) {
	dir := t.TempDir()
	if err := writeTestFile(dir+"/package.json", `{"version":"3.1.4"}`); err != nil {
		t.Fatal(err)
	}
	got := decodeProvenanceTyped(dir, dir)
	if got == nil || got.ToolVersion != "3.1.4" {
		t.Fatalf("provenance = %+v, want the version triple", got)
	}
	if got.Model != nil {
		t.Errorf("model = %v, want nil without a cost record", *got.Model)
	}
	if len(got.PromptsHash) != 64 {
		t.Errorf("prompts_hash = %q, want the empty-set digest", got.PromptsHash)
	}
	if err := writeTestFile(dir+"/cost.json", `[{"models":[{"name":"gpt-5-codex"}]}]`); err != nil {
		t.Fatal(err)
	}
	if model := decodeProvenanceTyped(dir, dir).Model; model == nil || *model != "gpt-5-codex" {
		t.Errorf("model = %v, want gpt-5-codex", model)
	}
	if got := decodeProvenanceTyped(dir, dir+"/missing"); got != nil && got.Model != nil {
		t.Errorf("model = %v, want nil for a bundle with no cost record", *got.Model)
	}
}

// TestRunDryRunFooterDisposition proves the dry-run envelope reports the
// footer as null when the knobs disable it and as the rendered block
// otherwise, so a reader can tell "off" from "rendered nothing".
//
//nolint:gocognit,revive // legs assert the refusal, receipt, and projection fields inline
func TestRunDryRunFooterDisposition(t *testing.T) {
	for _, tc := range []struct {
		name     string
		footer   string
		wantNull bool
	}{
		{name: "footer-on-renders", footer: core.FlagOn},
		{name: "footer-off-reads-null", footer: core.FlagOff, wantNull: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			postBundle(t, postReview)
			writeProvenanceFixture(t)
			cmdr := postCommander(t)
			var warnings, human []string
			req := postTestReq(cmdr, Flags{
				Approve: "false", ReviewFile: ".verdict/state/7/review.json", DryRun: true,
			}, &warnings, &human)
			req.Effective.PublishFooter = tc.footer
			resp, ref := Run(context.Background(), req)
			if ref != nil {
				t.Fatalf("Run refusal: %v", ref)
			}
			footer := resp.Data.Body.Footer
			if tc.wantNull {
				if footer != nil {
					t.Errorf("footer = %v, want null with --no-footer", footer)
				}
				return
			}
			text, ok := footer.(string)
			if !ok || !strings.Contains(text, core.FooterDivider) {
				t.Errorf("footer = %v, want the rendered block", footer)
			}
			if got := resp.Data.Provenance.ToolVersion; got != "3.1.4" {
				t.Errorf("dry-run provenance tool_version = %q, want 3.1.4", got)
			}
		})
	}
}

// TestSplitHelpersLiteralOracle proves the split helpers' rendered bytes: the
// follow-up header, the continuation note, and the dry-run split note.
func TestSplitHelpersLiteralOracle(t *testing.T) {
	t.Parallel()
	if got, want := splitFollowHead(2, 3),
		"> ℹ️ Note: review body continued (part 2/3 — split at the 65,536-char hosting limit; "+
			"see findings.json for the full text).\n\n"; got != want {
		t.Errorf("splitFollowHead(2,3) = %q, want %q", got, want)
	}
	if got, want := splitFirstNote(3),
		"\n\n> ⚠️ Note: this review continues in 2 follow-up comment(s) below "+
			"(split at the 65,536-char hosting limit)."; got != want {
		t.Errorf("splitFirstNote(3) = %q, want %q", got, want)
	}
	one := &state{payloads: []string{"{}"}}
	if got := splitNote(one); got != "" {
		t.Errorf("splitNote(one part) = %q, want empty", got)
	}
	two := &state{payloads: []string{"{}", "{}"}}
	if got, want := splitNote(two), " (split into 2 review comments at the hosting limit)"; got != want {
		t.Errorf("splitNote(two parts) = %q, want %q", got, want)
	}
}

// TestSplitBodyKeepsExactFitChunk proves a part that a chunk fills exactly to
// the budget stays packed: the fit test is strict, so the greedy packer never
// splits a part it could keep, and every part stays within budget.
func TestSplitBodyKeepsExactFitChunk(t *testing.T) {
	t.Parallel()
	parts := splitBody("aaaa\n\nbbb\n\ncccc", 9)
	want := []string{"aaaa\n\nbbb", "cccc"}
	if strings.Join(parts, "|") != strings.Join(want, "|") {
		t.Errorf("splitBody(exact fit) = %q, want %q", parts, want)
	}
	for _, part := range parts {
		if len(part) > 9 {
			t.Errorf("part %q is over the 9-byte budget", part)
		}
	}
	if parts := splitBody("aaaa\n\nbbbb", 9); len(parts) != 2 {
		t.Errorf("splitBody(one byte short) = %q, want two parts", parts)
	}
}

// TestRuneChunksCutOnBoundaries proves the over-long-paragraph fallback cuts
// on rune boundaries and loses no bytes.
func TestRuneChunksCutOnBoundaries(t *testing.T) {
	t.Parallel()
	parts := runeChunks(strings.Repeat("⚠", 4), 7)
	if len(parts) != 2 {
		t.Fatalf("runeChunks = %q, want two 6-byte parts", parts)
	}
	for _, part := range parts {
		if part != "⚠⚠" {
			t.Errorf("chunk = %q, want the two-rune boundary cut", part)
		}
	}
	if got := strings.Join(runeChunks(strings.Repeat("⚠", 3), 4), ""); got != strings.Repeat("⚠", 3) {
		t.Errorf("narrow budget lost bytes: %q", got)
	}
}

// TestPreviewDryRunLines proves the human dry-run preview names the body size,
// the comment keys, the footer block, and the diagram line — and that an
// absent diagram is not announced.
func TestPreviewDryRunLines(t *testing.T) {
	t.Parallel()
	var human []string
	var warnings []string
	st := postSplitState(&warnings)
	st.req.Progress = testProgress{warnings: &warnings, human: &human}
	st.body = "> ✅ Looks good."
	st.comments = []any{jq.NewObj(fieldPath, "notes.txt", fieldLine, 2, fieldBody, "x")}
	st.diagram = diagram.Status{State: "rewritten", Detail: "two blocks collapsed"}
	st.previewDryRun("---\n\n<!-- verdict-footer -->\n> Reviewed with Verdict")
	want := []string{
		"dry-run: payload NOT posted. body chars: 17, payload comment keys: path,line,body",
		"footer preview:\n---\n\n<!-- verdict-footer -->\n> Reviewed with Verdict",
		"diagram: rewritten — two blocks collapsed",
	}
	if strings.Join(human, "\n") != strings.Join(want, "\n") {
		t.Errorf("human = %q, want %q", human, want)
	}
	human = nil
	st.diagram = diagram.Status{State: "absent"}
	st.payloads = []string{"{}", "{}"}
	st.previewDryRun("")
	only := strings.Join(human, "\n")
	if !strings.Contains(only, "(split into 2 review comments at the hosting limit)") {
		t.Errorf("preview misses the split note: %q", only)
	}
	if strings.Contains(only, "footer preview") || strings.Contains(only, "diagram:") {
		t.Errorf("preview announced an absent footer/diagram: %q", only)
	}
}

// TestBuildPostCommentsSigShortfall proves a kept comment past the sig list
// posts unmarked instead of tripping the index guard, and that the embedded
// count only counts real suggestions.
func TestBuildPostCommentsSigShortfall(t *testing.T) {
	t.Parallel()
	kept := []any{
		jq.NewObj(fieldPath, "a.txt", fieldLine, 1, fieldBody, "first", fieldSuggestion, "fix();"),
		jq.NewObj(fieldPath, "b.txt", fieldLine, 2, fieldBody, "second"),
		jq.NewObj(fieldPath, "c.txt", fieldLine, 3, fieldBody, "third"),
	}
	sigs := []string{strings.Repeat("a", 40)}
	got := buildPostComments(kept, sigs)
	if len(got) != 3 {
		t.Fatalf("comments = %d, want 3", len(got))
	}
	first := commentBody(t, got[0])
	if !strings.HasSuffix(first, "<!-- verdict-finding sig="+strings.Repeat("a", 40)+" -->") {
		t.Errorf("first body = %q, want the finding marker", first)
	}
	for i, want := range []string{"second", "third"} {
		body := commentBody(t, got[i+1])
		if body != want {
			t.Errorf("comment %d body = %q, want %q unmarked", i+2, body, want)
		}
	}
	if n := countEmbedded(kept); n != 1 {
		t.Errorf("embedded = %d, want the one suggestion", n)
	}
}

// globMarkers lists the armed duplicate markers in the test bundle.
func globMarkers(t *testing.T) []string {
	t.Helper()
	matches, err := filepath.Glob(".verdict/state/7/.posted_*")
	if err != nil {
		t.Fatalf("glob markers: %v", err)
	}
	return matches
}

// commentBody returns the posted body of one built comment.
func commentBody(t *testing.T, comment any) string {
	t.Helper()
	obj, ok := comment.(*jq.Obj)
	if !ok {
		t.Fatalf("comment %v is not an ordered object", comment)
	}
	return jq.R(mustGet(obj))
}
