package reportcore

import (
	"slices"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
	"github.com/stainless-code/verdict/internal/sarif"
)

// sarifArtifact builds the --sarif block: the SARIF 2.1.0 finding export
// with the sig as the partial fingerprint. PrettyOrdered carries the
// trailing newline the file contract expects.
func sarifArtifact(st *state) *core.FileArtifact {
	if st.flags.Sarif == "" {
		return nil
	}
	return &core.FileArtifact{Path: st.flags.Sarif, Content: []byte(jq.PrettyOrdered(sarifDoc(st)))}
}

func sarifDoc(st *state) *jq.Obj {
	kinds := sarifKinds(st.ranked)
	rules := make([]any, 0, len(kinds))
	for _, kind := range kinds {
		rules = append(rules, jq.NewObj(
			"id", "verdict/"+kind,
			"name", kind,
			"shortDescription", jq.NewObj("text", "verdict "+kind+" finding"),
		))
	}
	results := make([]any, 0, len(st.ranked))
	for _, item := range st.ranked {
		results = append(results, sarifResult(item.Entry))
	}
	run := jq.NewObj(
		"tool", jq.NewObj("driver", jq.NewObj("name", "verdict", "version", "1", "informationUri", "https://github.com", "rules", rules)),
		"results", results,
	)
	sarifWithheld(st, run)
	return jq.NewObj("version", "2.1.0", "$schema", "https://json.schemastore.org/sarif-2.1.0.json", "runs", []any{run})
}

func sarifKinds(ranked []readcommon.Ranked) []string {
	var kinds []string
	seen := map[string]bool{}
	for _, item := range ranked {
		kind := sarifKind(item.Entry)
		if !seen[kind] {
			seen[kind] = true
			kinds = append(kinds, kind)
		}
	}
	slices.Sort(kinds)
	return kinds
}

func sarifKind(entry any) string {
	if kind, ok := readcommon.FieldOf(entry, "kind").(string); ok {
		return kind
	}
	return "unknown"
}

func sarifResult(entry any) *jq.Obj {
	message := sarifMessage(entry)
	region := jq.NewObj()
	if line := readcommon.FieldOf(entry, "line"); line != nil && line != false {
		region.Set("startLine", line)
	}
	path := readcommon.FieldOf(entry, "path")
	if path == nil {
		path = "PR"
	}
	return jq.NewObj(
		"ruleId", "verdict/"+sarifKind(entry),
		"level", sarifLevel(readcommon.FieldOf(entry, "severity")),
		"message", jq.NewObj("text", message),
		"properties", jq.NewObj("category", sarifCategory(entry)),
		"locations", []any{jq.NewObj("physicalLocation", jq.NewObj(
			"artifactLocation", jq.NewObj("uri", path),
			"region", region,
		))},
		"partialFingerprints", jq.NewObj("verdict/sig", readcommon.FieldOf(entry, "sig")),
	)
}

func sarifMessage(entry any) string {
	message := ""
	if heading := readcommon.FieldOf(entry, "heading"); heading != nil && heading != false {
		if s, ok := heading.(string); ok {
			message = "[" + s + "] "
		}
	}
	body, ok := readcommon.FieldOf(entry, "body").(string)
	if !ok {
		body = ""
	}
	return message + body
}

func sarifLevel(sev any) string {
	s, ok := sev.(string)
	if !ok {
		return sarif.LevelForTier("")
	}
	return sarif.LevelForTier(s)
}

// sarifCategory reads the finding's published category for the SARIF
// properties block: the label when present, null when untagged.
func sarifCategory(entry any) any {
	m, ok := entry.(*jq.Obj)
	if !ok || m == nil {
		return nil
	}
	v, _ := m.Get("category")
	if s, ok := v.(string); ok && core.ValidCategory(s) {
		return s
	}
	return nil
}
