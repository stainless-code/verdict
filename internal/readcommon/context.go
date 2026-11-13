package readcommon

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/jq"
)

const contextManifestCap = 2097152

// ContextSources lists the source names in an explicitly fetched bundle.
func ContextSources(dir string) []string {
	f, err := os.Open(filepath.Join(dir, "context.json"))
	if err != nil {
		return nil
	}
	defer f.Close()
	var rows []struct {
		Source string `json:"source"`
	}
	if err := json.NewDecoder(io.LimitReader(f, contextManifestCap)).Decode(&rows); err != nil {
		return nil
	}
	var out []string
	for _, row := range rows {
		out = append(out, row.Source)
	}
	return out
}

// SourcesLine renders finding citations on one line.
func SourcesLine(value any) string {
	entry, ok := value.(*jq.Obj)
	if !ok || entry == nil {
		return ""
	}
	raw, _ := entry.Get("sources")
	items := contextArray(raw)
	var sources []string
	for _, item := range items {
		if s, ok := item.(string); ok {
			sources = append(sources, strings.Join(strings.Fields(s), " "))
		}
	}
	if len(sources) == 0 {
		return ""
	}
	return "Sources used: " + strings.Join(sources, ", ")
}

// WarnContextSources keeps unrecognized citations advisory.
func WarnContextSources(dir string, review *jq.Obj, warn func(string)) {
	known := ContextSources(dir)
	raw, _ := review.Get("comments")
	comments := contextArray(raw)
	for _, comment := range comments {
		obj, ok := comment.(*jq.Obj)
		if !ok {
			continue
		}
		raw, _ := obj.Get("sources")
		items := contextArray(raw)
		warnUnknownSources(known, items, warn)
	}
}

func contextArray(v any) []any {
	items, ok := v.([]any)
	if !ok {
		return nil
	}
	return items
}

func warnUnknownSources(known []string, items []any, warn func(string)) {
	for _, item := range items {
		if s, ok := item.(string); ok && !slices.Contains(known, s) {
			warn("unknown context source: " + strings.Join(strings.Fields(s), " "))
		}
	}
}

// ContextLine lists handed-over evidence for human reports.
func ContextLine(dir string) []string {
	sources := ContextSources(dir)
	if len(sources) == 0 {
		return nil
	}
	return []string{"Context used: " + strings.Join(sources, ", ")}
}

// CitationLines returns the optional citation line.
func CitationLines(entry any) []string {
	if line := SourcesLine(entry); line != "" {
		return []string{line}
	}
	return nil
}
