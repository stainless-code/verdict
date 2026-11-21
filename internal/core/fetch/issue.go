package fetchcore

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/stainless-code/verdict/internal/evidence"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// issueDoc is one resolved issue document before the manifest records it.
// source is the canonical URL the bundle cites; label is the reference the
// warnings and the human stream name.
type issueDoc struct {
	source string
	label  string
	title  string
	body   string
}

// document composes the spec source: the title as an H1, a blank line, then
// the body verbatim — the shape a --spec reader already reads. kept and
// total are set only when the per-issue cap cut the document.
func (d issueDoc) document() (body []byte, kept, total int) {
	text := "# " + d.title + "\n\n" + d.body
	if len(text) <= issueMaxBytes {
		return []byte(text), 0, 0
	}
	kept = issueMaxBytes
	for kept > 0 && !utf8.RuneStart(text[kept]) {
		kept--
	}
	marker := fmt.Sprintf("\n[truncated: %d of %d bytes]\n", kept, len(text))
	return []byte(text[:kept] + marker), kept, len(text)
}

// contextFile renders one issue document as a context source. An issue
// document is Markdown whatever its URL ends with, so the raw copy keeps a
// .md name.
func (d issueDoc) contextFile(s *session, role string) contextFile {
	body, kept, total := d.document()
	if total > 0 {
		s.Warn(fmt.Sprintf(msgIssueTruncated, reasonTruncatedBytes,
			fmt.Sprintf("%s kept %d of %d bytes", d.source, kept, total)))
	}
	return contextFile{source: d.source, role: role, ext: issueExt, body: body}
}

// collectExplicitIssues resolves every --issue operand into a spec source. An
// unreadable issue warns and never refuses: the operator named a source the
// review would like, not a gate the fetch must pass. A canceled context is the
// one exception — it stops the collection at once with its own error, so the
// run refuses `canceled` before any bundle write instead of warning its way
// through the operands it can no longer read.
func collectExplicitIssues(ctx context.Context, ctx2 *fetchRunCtx, f *Flags) ([]contextFile, error) {
	refs, err := parseIssueRefs(f.Issue, f.Provider, ctx2.repo)
	if err != nil {
		return nil, err
	}
	var files []contextFile
	seen := make(map[string]bool, len(refs))
	for _, ref := range refs {
		file, ok, err := explicitIssueFile(ctx, ctx2, ref, seen)
		if err != nil {
			return nil, err
		}
		if ok {
			files = append(files, file)
		}
	}
	return files, nil
}

// explicitIssueFile resolves one operand into its spec source. ok is false when
// the read failed (its warning is already on the stream) or the run already
// carries that issue; the error is the canceled context that ends the
// collection.
func explicitIssueFile(
	ctx context.Context, ctx2 *fetchRunCtx, ref issueRef, seen map[string]bool,
) (contextFile, bool, error) {
	read, err := readUncanceled(ctx, ctx2, ref)
	if err != nil {
		return contextFile{}, false, err
	}
	if !read.ok() {
		ctx2.runner.Warn(read.warn)
		return contextFile{}, false, nil
	}
	key := strings.ToLower(read.doc.source)
	if seen[key] {
		return contextFile{}, false, nil
	}
	seen[key] = true
	return read.doc.contextFile(ctx2.runner, contextRoleSpec), true, nil
}

// readUncanceled reads one --issue operand and gives the context the chance to
// stop the collection: a canceled context is the error it returns, not a
// warning, because the review it was gathered for is over.
func readUncanceled(ctx context.Context, ctx2 *fetchRunCtx, ref issueRef) (issueRead, error) {
	if err := ctx.Err(); err != nil {
		return issueRead{}, err
	}
	read := readIssue(ctx, ctx2, ref)
	if err := ctx.Err(); err != nil {
		return issueRead{}, err
	}
	return read, nil
}

// collectLinkedIssues appends the PR's linked issues, on GitHub only. The
// keyword gate keeps a PR that links nothing to today's bundle and today's
// host calls, and nothing here refuses: a linked issue a PR body advertised
// must never break the fetch.
func collectLinkedIssues(ctx context.Context, ctx2 *fetchRunCtx) {
	gate := keywordLinkedRefs(ctx2)
	if len(gate) == 0 {
		return
	}
	c := &linkedCollector{ctx2: ctx2}
	c.collect(ctx, gate)
	if len(c.files) > 0 {
		ctx2.contextFiles = dedupeContextFiles(append(ctx2.contextFiles, c.files...))
	}
}

// keywordLinkedRefs reads the reviewed PR's closing keywords: the cheap local
// gate that decides whether the GraphQL read is worth a host call. A keyword
// whose slug is not a repository shape is not an issue link at all — a PR body
// is attacker-influenced, so `Fixes ../..#12` costs nothing.
func keywordLinkedRefs(ctx2 *fetchRunCtx) []readcommon.LinkedIssue {
	bundle := readcommon.OpenBundle(ctx2.dir)
	refs := readcommon.ClosingIssueRefs(bundle.StrField(keyTitle), bundle.StrField(keyBody))
	out := refs[:0]
	for _, ref := range refs {
		if ref.Slug == "" || validSlug(ref.Slug) {
			out = append(out, ref)
		}
	}
	return out
}

// warnUnsupportedLinkedIssues warns when a non-GitHub PR links an issue this
// build cannot follow: the review still needs that context, so the omission is
// said out loud instead of skipped.
func warnUnsupportedLinkedIssues(ctx2 *fetchRunCtx, provider string) {
	if len(keywordLinkedRefs(ctx2)) == 0 {
		return
	}
	ctx2.runner.Warn(fmt.Sprintf(msgIssueUnsupported, reasonUnsupported, provider))
}

// issueRead is one issue read's outcome: the document, or the typed warning
// line that explains why there is none.
type issueRead struct {
	doc  issueDoc
	warn string
}

func (r *issueRead) ok() bool { return r.warn == "" }

// readIssue resolves one reference, naming the read on the human stream
// before it costs a host call.
func readIssue(ctx context.Context, ctx2 *fetchRunCtx, ref issueRef) issueRead {
	fmt.Fprintf(ctx2.runner.Human, "» issue %s\n", ref.label())
	return readGitHubIssue(ctx, ctx2.runner, ref)
}

// capBreach names the combined context cap a set would cross: the file count
// first, then the byte total.
func capBreach(files []contextFile) string {
	if len(files) > contextMaxFiles {
		return reasonOverFiles
	}
	total := 0
	for _, file := range files {
		total += len(file.body)
	}
	if total > contextMaxBytes {
		return reasonOverBytes
	}
	return ""
}

// checkIssueCaps refuses the combined set an explicit issue pushed past a
// context cap: the operator asked for that source by name, so it keeps the
// context-invalid posture the flag inputs have.
func checkIssueCaps(files []contextFile) error {
	switch breach := capBreach(files); breach {
	case reasonOverFiles:
		return contextRefusal("context exceeds %d files", contextMaxFiles)
	case reasonOverBytes:
		return contextRefusal("context exceeds %d bytes", contextMaxBytes)
	}
	return nil
}

// specItemCount counts the deterministic checklist items across the set's
// spec-role sources.
func specItemCount(files []contextFile) int {
	var blocks []evidence.SpecBlock
	for i, file := range files {
		if file.role == contextRoleSpec {
			blocks = append(blocks, evidence.SpecBlock{Index: i, Text: string(file.body)})
		}
	}
	return len(evidence.EnumerateSpecItems(blocks))
}

// linkedCollector writes auto-discovered issues into the run's context set
// within the caps: at the file or byte cap it stops adding, and past the spec
// item cap it demotes the source to reference material.
type linkedCollector struct {
	ctx2  *fetchRunCtx
	files []contextFile
}

// collect resolves the linked candidates, names them, and adds each one it can
// read.
func (c *linkedCollector) collect(ctx context.Context, gate []readcommon.LinkedIssue) {
	candidates := c.fresh(c.candidates(ctx, gate))
	if len(candidates) == 0 {
		return
	}
	if len(candidates) > issueMaxLinked {
		c.ctx2.runner.Warn(fmt.Sprintf(msgIssueOverIssues, reasonOverIssues, issueMaxLinked, len(candidates)))
		candidates = candidates[:issueMaxLinked]
	}
	labels := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		labels = append(labels, candidate.ref.label())
	}
	fmt.Fprintf(c.ctx2.runner.Human, "» linked issues: %d (%s)\n", len(candidates), strings.Join(labels, ", "))
	for i := range candidates {
		read := candidates[i].read(ctx, c.ctx2)
		if !read.ok() {
			c.ctx2.runner.Warn(read.warn)
			continue
		}
		c.add(read.doc)
	}
}

// candidates unions the issues GraphQL reports with the keyword references,
// deduped by repository and number and sorted by number, so the host-call
// order and the bundle order never depend on map iteration.
func (c *linkedCollector) candidates(ctx context.Context, gate []readcommon.LinkedIssue) []linkedCandidate {
	nodes, more, text, err := closingIssues(ctx, c.ctx2)
	if err != nil {
		c.ctx2.runner.Warn(fmt.Sprintf(msgIssueUnavailable, issueReason(text, err),
			fmt.Sprintf(msgIssueLinkedReadError, c.ctx2.repo, c.ctx2.pr)))
	}
	if more {
		c.ctx2.runner.Warn(fmt.Sprintf(msgIssueTruncated, reasonTruncatedList,
			fmt.Sprintf("%s#%s kept the first %d linked issues", c.ctx2.repo, c.ctx2.pr, issueMaxGraphQL)))
	}
	return linkedCandidates(gate, nodes, c.ctx2.repo)
}

// fresh drops the candidates the run already cites, so an explicit
// `--issue acme/app#12` and a PR body saying `Fixes #12` cost one read and one
// warning, not two: the source URL is known before any host call, whether the
// candidate is a GraphQL node or a keyword-only REST read. The comparison
// folds the URL, because a repository slug is case-insensitive and the host may
// spell one side `Acme/App`.
func (c *linkedCollector) fresh(candidates []linkedCandidate) []linkedCandidate {
	seen := make(map[string]bool, len(c.ctx2.contextFiles)+len(candidates))
	for _, file := range c.ctx2.contextFiles {
		seen[strings.ToLower(file.source)] = true
	}
	out := candidates[:0]
	for _, candidate := range candidates {
		if seen[strings.ToLower(candidate.source())] {
			continue
		}
		out = append(out, candidate)
	}
	return out
}

// add writes one candidate into the set, demoting it to context when its items
// would cross the spec cap and stopping at the file and byte caps: a PR body
// is attacker-influenced and must not be able to break a fetch.
func (c *linkedCollector) add(doc issueDoc) {
	candidate := doc.contextFile(c.ctx2.runner, contextRoleSpec)
	set := make([]contextFile, 0, len(c.ctx2.contextFiles)+len(c.files)+1)
	set = append(set, c.ctx2.contextFiles...)
	set = append(set, c.files...)
	set = append(set, candidate)
	prospective := dedupeContextFiles(set)
	if breach := capBreach(prospective); breach != "" {
		c.ctx2.runner.Warn(fmt.Sprintf(msgIssueOverCap, breach,
			fmt.Sprintf("%s not added (the bundle would exceed %s)", doc.source, capSentence(breach))))
		return
	}
	if items := specItemCount(prospective); items > evidence.MaxSpecItems {
		candidate.role = contextRoleContext
		c.ctx2.runner.Warn(fmt.Sprintf(msgIssueOverCap, reasonOverItems,
			fmt.Sprintf("%s demoted to context (%d items over the %d-item spec cap)",
				doc.source, items, evidence.MaxSpecItems)))
	}
	c.files = append(c.files, candidate)
}

// capSentence renders the combined cap a breach names.
func capSentence(breach string) string {
	if breach == reasonOverFiles {
		return fmt.Sprintf("%d context files", contextMaxFiles)
	}
	return fmt.Sprintf("%d context bytes", contextMaxBytes)
}
