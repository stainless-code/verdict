package reportcore

import (
	"slices"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// sarifArtifact builds the --sarif block: the SARIF 2.1.0 finding export
// with the sig as the partial fingerprint. PrettyOrdered carries the
// trailing newline like the shell's jq redirect.
func sarifArtifact(st *state) *core.FileArtifact {
	if st.flags.Sarif == "" {
		return nil
	}
	return &core.FileArtifact{Path: st.flags.Sarif, Content: []byte(jq.PrettyOrdered(sarifDoc(st)))}
}

// sarifDoc builds the SARIF document over the ranked findings.
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
	return jq.NewObj(
		"version", "2.1.0",
		"$schema", "https://json.schemastore.org/sarif-2.1.0.json",
		"runs", []any{jq.NewObj(
			"tool", jq.NewObj("driver", jq.NewObj(
				"name", "verdict",
				"version", "1",
				"informationUri", "https://github.com",
				"rules", rules,
			)),
			"results", results,
		)},
	)
}

// sarifKinds ports the unique-kind rule list: the coalesced kinds
// (null reads unknown, mirroring the null-safe kind concat) sorted in jq
// order.
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

// sarifKind ports ("verdict/" + (.kind // "unknown"))'s kind half.
func sarifKind(entry any) string {
	if kind, ok := fieldOf(entry, "kind").(string); ok {
		return kind
	}
	return "unknown"
}

// sarifResult ports one SARIF result: the tier level, heading-led message,
// path/line location, and sig fingerprint.
func sarifResult(entry any) *jq.Obj {
	message := sarifMessage(entry)
	region := jq.NewObj()
	if line := fieldOf(entry, "line"); line != nil && line != false {
		region.Set("startLine", line)
	}
	path := fieldOf(entry, "path")
	if path == nil {
		path = "PR"
	}
	return jq.NewObj(
		"ruleId", "verdict/"+sarifKind(entry),
		"level", sarifLevel(fieldOf(entry, "severity")),
		"message", jq.NewObj("text", message),
		"locations", []any{jq.NewObj("physicalLocation", jq.NewObj(
			"artifactLocation", jq.NewObj("uri", path),
			"region", region,
		))},
		"partialFingerprints", jq.NewObj("verdict/sig", fieldOf(entry, "sig")),
	)
}

// sarifMessage ports the heading-led message: "[heading] body", with the
// body reading "" when the entry carries none.
func sarifMessage(entry any) string {
	message := ""
	if heading := fieldOf(entry, "heading"); heading != nil && heading != false {
		if s, ok := heading.(string); ok {
			message = "[" + s + "] "
		}
	}
	body, ok := fieldOf(entry, "body").(string)
	if !ok {
		body = ""
	}
	return message + body
}

// sarifLevel ports the severity-to-level map: untagged severities read note.
func sarifLevel(sev any) string {
	if s, ok := sev.(string); ok {
		switch s {
		case tierCritical:
			return "error"
		case tierImportant:
			return "warning"
		}
	}
	return "note"
}
