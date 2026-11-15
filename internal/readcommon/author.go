package readcommon

import (
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// AuthorSuffix names a bundle's advisory author fingerprint off its dir.
const AuthorSuffix = "/author.json"

// Author reads and decodes author.json under MaxAuthorBytes; invalid or absent
// files return false.
func Author(dir string) (*core.AuthorFile, bool) {
	raw, err := jq.CappedFile(dir+AuthorSuffix, jq.MaxAuthorBytes)
	if err != nil {
		return nil, false
	}
	return ParseAuthor(raw)
}

var authorAgents = map[string]bool{"claude": true, "codex": true, "cursor": true}

var authorConfidences = map[string]bool{"high": true, "medium": true}

// ParseAuthor decodes one author.json body, false for anything that names no
// agent.
func ParseAuthor(raw []byte) (*core.AuthorFile, bool) {
	doc, err := jq.Decode(raw)
	if err != nil {
		return nil, false
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		return nil, false
	}
	agent := jq.StrOf(m["agent"])
	confidence := jq.StrOf(m["confidence"])
	if !authorAgents[agent] || !authorConfidences[confidence] {
		return nil, false
	}
	var model *string
	if name := jq.StrOf(m["model"]); name != "" {
		model = &name
	}
	return &core.AuthorFile{
		SchemaVersion: jq.StrOf(m["schema_version"]),
		Agent:         agent,
		Model:         model,
		Confidence:    confidence,
		Evidence:      authorEvidence(m["evidence"]),
	}, true
}

func authorEvidence(raw any) []core.AuthorEvidence {
	var out []core.AuthorEvidence
	for _, v := range jq.Elements(raw) {
		row, ok := jq.AsMap(v)
		if !ok {
			continue
		}
		evidence := core.AuthorEvidence{Field: jq.StrOf(row["field"]), Signal: jq.StrOf(row["signal"])}
		if evidence.Field == "" || evidence.Signal == "" {
			continue
		}
		out = append(out, evidence)
	}
	return out
}
