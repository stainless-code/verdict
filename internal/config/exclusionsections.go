package config

import (
	"strings"

	"github.com/stainless-code/verdict/internal/hunks"
)

// ExcludedEndpoint reports whether a privacy exclusion glob covers either
// rename endpoint of one diff section: the b-side path, or the a-side path when
// the section renamed it. Matching one endpoint withholds the whole section, so
// a rename out of an excluded path never discloses the other endpoint's bytes.
func ExcludedEndpoint(path, oldPath string, exclusions []string) bool {
	for _, glob := range exclusions {
		if Match(glob, path) {
			return true
		}
		if oldPath != "" && oldPath != path && Match(glob, oldPath) {
			return true
		}
	}
	return false
}

// WithoutExcludedSections returns diffText without the file sections the
// personal privacy exclusions cover, so a model-facing export can publish a
// diff that still holds every kept section byte for byte. The bytes before the
// first header are kept; with no exclusions the input comes back unchanged.
func WithoutExcludedSections(diffText string, exclusions []string) string {
	if len(exclusions) == 0 || diffText == "" {
		return diffText
	}
	preamble, sections := hunks.Sections(diffText)
	kept := make([]string, 0, len(sections))
	for i := range sections {
		oldPath, newPath := hunks.HeaderPaths(diffHeaderLine(sections[i]))
		if ExcludedEndpoint(newPath, oldPath, exclusions) {
			continue
		}
		kept = append(kept, sections[i])
	}
	return preamble + strings.Join(kept, "")
}

// diffHeaderLine returns one section's first line, its `diff --git` header.
func diffHeaderLine(section string) string {
	header, _, _ := strings.Cut(section, "\n")
	return header
}
