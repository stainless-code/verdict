package run

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	skillfs "github.com/stainless-code/verdict"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/readcommon"
)

const finderCount = 2

func finderProcedure(dir, output string, index int) ([]byte, *core.RefusalError) {
	method := "Read spec/context first. Identify requirements, boundary values and declared defaults; " +
		"compare each with implementation, then trace callers and partner paths. Cover every hunk."
	if index == 1 {
		method = "Read the diff from the last file toward the first. Follow entry points, state transitions, " +
			"error/cleanup paths and consumers, then challenge against spec/context. Cover every hunk."
	}
	text := fmt.Sprintf("# Finder %d\n\n%s\n\n", index+1, method) +
		"Review the whole diff in this process. Do not spawn sub-agents. Recipe lenses are declarative context; " +
		"this method owns dispatch. Report every supported suspicion, including low-confidence ones.\n\n"
	files := finderSections(dir)
	var fragments strings.Builder
	for _, name := range files {
		raw, err := skillfs.Read("prompts/handoff/" + name)
		if err != nil {
			return nil, &core.RefusalError{Code: codeMissingBundle, Message: "finder contract unavailable: " + name}
		}
		fragments.WriteString(string(raw) + "\n\n")
	}
	text += fragments.String()
	text = strings.ReplaceAll(text, "<bundle>/review.json", filepath.Join(output, reviewFile))
	text = strings.ReplaceAll(text, "<bundle>", dir)
	return []byte(text), nil
}

func finderSections(dir string) []string {
	files := []string{"role-method.md", "findings-contract.md", "capability-map.md"}
	for _, section := range []struct{ artifact, name string }{
		{filepath.Join("chunks", "manifest.json"), "chunked.md"},
		{"threads.json", "threads.md"},
		{"kb.txt", "kb.md"},
		{"lenses.txt", "lenses.md"},
		{"context.txt", "context.md"},
	} {
		if info, err := os.Stat(filepath.Join(dir, section.artifact)); err == nil && info.Size() > 0 {
			files = append(files, section.name)
		}
	}
	if finderHasSpec(dir) {
		files = append(files, "spec.md")
	}
	return files
}

func finderHasSpec(dir string) bool {
	for _, entry := range readcommon.ContextEntries(dir) {
		if entry.Role == "spec" {
			return true
		}
	}

	return false
}
