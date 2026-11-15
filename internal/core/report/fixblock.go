package reportcore

import (
	"github.com/stainless-code/verdict/internal/brief"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

const keyFixSig = "sig"

func buildFix(rec *jq.Obj, dir string) *core.ShowFix {
	full := objectStringOr(rec, keyFixSig)
	return &core.ShowFix{
		Sig:        full,
		Status:     objectStringOr(rec, "status"),
		Severity:   recordSeverity(rec),
		Files:      recordFiles(rec),
		Locator:    recordLocator(rec),
		Suggestion: recordStringPtr(rec, "suggestion"),
		Brief:      brief.FromRecord(rec),
		VerifyHint: readcommon.VerifyHint(dir, full),
		Caution:    core.FindingCaution,
	}
}

func objectStringOr(m *jq.Obj, key string) string {
	value, ok := m.Get(key)
	if !ok {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return text
}

func recordStringPtr(m *jq.Obj, key string) *string {
	value, ok := m.Get(key)
	if !ok {
		return nil
	}
	text, ok := value.(string)
	if !ok {
		return nil
	}
	return &text
}

func recordSeverity(m *jq.Obj) *string {
	severity := objectStringOr(m, "severity")
	switch severity {
	case "critical", "important", "informational":
		return &severity
	default:
		return nil
	}
}

func recordFiles(rec *jq.Obj) []string {
	files := []string{}
	seen := map[string]struct{}{}
	if path, ok := rec.Get("path"); ok {
		addFilePath(&files, seen, path)
	}
	appendRelatedFiles(rec, &files, seen)
	return files
}

func appendRelatedFiles(rec *jq.Obj, files *[]string, seen map[string]struct{}) {
	for _, key := range []string{"files", "related_files"} {
		value, ok := rec.Get(key)
		if !ok {
			continue
		}
		items, ok := value.([]any)
		if !ok {
			continue
		}
		for _, item := range items {
			addFilePath(files, seen, item)
		}
	}
}

func addFilePath(files *[]string, seen map[string]struct{}, value any) {
	path, ok := value.(string)
	if !ok || path == "" {
		return
	}
	if _, dup := seen[path]; dup {
		return
	}
	seen[path] = struct{}{}
	*files = append(*files, path)
}

func recordLocator(rec *jq.Obj) string {
	path, _ := rec.Get("path")
	if text, ok := path.(string); ok && text != "" {
		locator := text
		if line, ok := rec.Get("line"); ok && !jq.Falsey(line) {
			locator += ":" + jq.Interp(line)
		}
		return locator
	}
	return objectStringOr(rec, "heading")
}
