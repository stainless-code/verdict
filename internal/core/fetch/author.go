package fetchcore

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

const (
	suffixAuthor = "/author.json"

	authorFieldTitle   = "title"
	authorFieldBody    = "body"
	authorFieldHeadRef = "head_ref"
	authorFieldCommits = "commits"

	authorHigh   = "high"
	authorMedium = "medium"

	agentClaude = "claude"
	agentCodex  = "codex"
	agentCursor = "cursor"

	authorTag       = "[codex]"
	authorGenerated = "generated with claude code"

	keyCommits         = "commits"
	keyHeadRefName     = "headRefName"
	keyMessage         = "message"
	keyMessageBody     = "messageBody"
	keyMessageHeadline = "messageHeadline"
)

// longest first, so "Claude Code" is not parsed as model "Code".
var authorNames = []struct{ name, agent string }{
	{"claude code", agentClaude},
	{"claude", agentClaude},
	{"codex", agentCodex},
	{"cursor", agentCursor},
}

var authorPrefixes = []struct{ prefix, agent string }{
	{"codex/", agentCodex},
	{"cursor/", agentCursor},
}

var authorBrackets = strings.NewReplacer("[", "", "]", "")

type authorText struct {
	field string
	text  string
}

type authorHit struct {
	agent      string
	model      string
	confidence string
	evidence   core.AuthorEvidence
}

func writeAuthor(ctx2 *fetchRunCtx) error {
	file := authorFromBundle(ctx2.dir)
	if file == nil {
		return discardAuthor(ctx2.dir)
	}
	raw := jq.PrettyOrdered(core.Wire(file))
	if werr := atomic.WriteFile(ctx2.dir+suffixAuthor, []byte(raw)); werr != nil {
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf(
			"could not write %s/author.json: %v", ctx2.dir, werr)}
	}
	fmt.Fprintf(ctx2.runner.Human, "» author: %s (%s confidence) from %d signal(s) -> %s/author.json\n",
		file.Agent, file.Confidence, len(file.Evidence), ctx2.runner.display(ctx2.dir))
	return nil
}

func discardAuthor(dir string) error {
	if err := os.Remove(dir + suffixAuthor); err != nil && !errors.Is(err, os.ErrNotExist) {
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf("could not remove %s/author.json: %v", dir, err)}
	}
	return nil
}

func authorFromBundle(dir string) *core.AuthorFile {
	raw, err := os.ReadFile(dir + suffixPRJSON)
	if err != nil {
		return nil
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		return nil
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		return nil
	}
	return detectAuthor(authorTexts(m))
}

func authorTexts(m map[string]any) []authorText {
	out := []authorText{
		{authorFieldTitle, jq.StrOf(m[keyTitle])},
		{authorFieldBody, jq.StrOf(m[keyBody])},
		{authorFieldHeadRef, authorHeadRef(m)},
	}
	for _, c := range jq.Elements(m[keyCommits]) {
		commit, ok := jq.AsMap(c)
		if !ok {
			continue
		}
		out = append(out, authorText{authorFieldCommits, strings.Join([]string{
			jq.StrOf(commit[keyMessageHeadline]), jq.StrOf(commit[keyMessageBody]), jq.StrOf(commit[keyMessage]),
		}, "\n")})
	}
	return out
}

func authorHeadRef(m map[string]any) string {
	if ref := jq.StrOf(m[keyHeadRefName]); ref != "" {
		return ref
	}
	return jq.StrOf(m[keyHeadRef])
}

func detectAuthor(texts []authorText) *core.AuthorFile {
	var hits []authorHit
	for _, t := range texts {
		hits = append(hits, authorHits(t)...)
	}
	if len(hits) == 0 {
		return nil
	}
	best := hits[0]
	for _, hit := range hits {
		if hit.confidence == authorHigh && best.confidence != authorHigh {
			best = hit
		}
	}
	file := &core.AuthorFile{
		SchemaVersion: schemaV1,
		Agent:         best.agent,
		Model:         authorModel(hits, best.agent),
		Confidence:    best.confidence,
		Evidence:      make([]core.AuthorEvidence, 0, len(hits)),
	}
	for _, hit := range hits {
		file.Evidence = append(file.Evidence, hit.evidence)
	}
	return file
}

func authorModel(hits []authorHit, agent string) *string {
	for _, hit := range hits {
		if hit.agent == agent && hit.model != "" {
			model := hit.model
			return &model
		}
	}
	return nil
}

func authorHits(t authorText) []authorHit {
	var out []authorHit
	if hit, ok := authorTrailerHit(t); ok {
		out = append(out, hit)
	}
	if line, ok := authorGeneratedHit(t); ok {
		out = append(out, authorHit{
			agent: agentClaude, confidence: authorHigh,
			evidence: core.AuthorEvidence{Field: t.field, Signal: line},
		})
	}
	if t.field == authorFieldTitle && strings.HasPrefix(strings.TrimSpace(t.text), authorTag) {
		out = append(out, authorHit{
			agent: agentCodex, confidence: authorMedium,
			evidence: core.AuthorEvidence{Field: t.field, Signal: authorTag},
		})
	}
	return append(out, authorPrefixHits(t)...)
}

func authorPrefixHits(t authorText) []authorHit {
	if t.field != authorFieldHeadRef {
		return nil
	}
	var out []authorHit
	for _, known := range authorPrefixes {
		if strings.HasPrefix(t.text, known.prefix) {
			out = append(out, authorHit{
				agent: known.agent, confidence: authorMedium,
				evidence: core.AuthorEvidence{Field: t.field, Signal: known.prefix},
			})
		}
	}
	return out
}

func authorTrailerHit(t authorText) (authorHit, bool) {
	for line := range strings.Lines(t.text) {
		name, ok := authorTrailerName(line)
		if !ok {
			continue
		}
		if hit, matched := authorNamedHit(t.field, name, line); matched {
			return hit, true
		}
	}
	return authorHit{}, false
}

func nameContinues(lower, name string) bool {
	rest := lower[len(name):]
	if rest == "" {
		return false
	}
	next, _ := utf8.DecodeRuneInString(rest)
	return unicode.IsLetter(next)
}

func authorNamedHit(field, name, line string) (authorHit, bool) {
	lower := strings.ToLower(name)
	for _, known := range authorNames {
		if !strings.HasPrefix(lower, known.name) {
			continue
		}
		if nameContinues(lower, known.name) {
			continue
		}
		return authorHit{
			agent:      known.agent,
			model:      strings.TrimSpace(name[len(known.name):]),
			confidence: authorHigh,
			evidence:   core.AuthorEvidence{Field: field, Signal: strings.TrimSpace(line)},
		}, true
	}
	return authorHit{}, false
}

func authorTrailerName(line string) (string, bool) {
	key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
	if !ok || !strings.EqualFold(strings.TrimSpace(key), "co-authored-by") {
		return "", false
	}
	name, _, _ := strings.Cut(value, "<")
	name = strings.TrimSpace(name)
	return name, name != ""
}

func authorGeneratedHit(t authorText) (string, bool) {
	for line := range strings.Lines(t.text) {
		if strings.Contains(strings.ToLower(authorBrackets.Replace(line)), authorGenerated) {
			return strings.TrimSpace(line), true
		}
	}
	return "", false
}

func authorRoutingHint(dir string) string {
	file, ok := readcommon.Author(dir)
	if !ok || file.Confidence == "" {
		return ""
	}
	return fmt.Sprintf("; author looks like %s (%s); a reviewer from another model family is suggested",
		file.Agent, file.Confidence)
}

func addAuthorArtifact(obj *jq.Obj, dir string) {
	addArtifactIfPresent(obj, dir, "author.json", suffixAuthor)
}
