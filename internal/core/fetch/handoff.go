package fetchcore

import (
	"os"

	skillfs "github.com/stainless-code/verdict"
	threadscore "github.com/stainless-code/verdict/internal/core/threads"
)

// bundleFacts reads only the bundle's presence facts, in one place. Every
// field is a boolean: the handoff appends a section only when its fact is
// true, so the same bundle always assembles the same procedure.md.
func bundleFacts(dir string) skillfs.HandoffFacts {
	return skillfs.HandoffFacts{
		Chunked:      hasChunkManifest(dir),
		PriorThreads: threadscore.CountFile(dir+suffixThreadsJSON) > 0,
		KB:           nonEmptyFile(dir + suffixKBTxt),
		Lenses:       nonEmptyFile(dir + suffixLensesTxt),
		Context:      nonEmptyFile(dir + "/context.txt"),
	}
}

// hasChunkManifest reports whether the chunk map the review core names is
// present. An unreadable manifest is still a fact: the bundle carries chunks,
// and the section tells the agent to read the map and resolve the failure.
func hasChunkManifest(dir string) bool {
	info, err := os.Stat(dir + suffixChunkManifest)
	return err == nil && info.Mode().IsRegular()
}

// nonEmptyFile reports a regular file with content. An empty kb.txt or
// lenses.txt is a fetch that wrote no rules, so the section would tell the
// agent to read nothing.
func nonEmptyFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Size() > 0
}
