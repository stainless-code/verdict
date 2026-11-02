package sig_test

import (
	"testing"

	"github.com/stainless-code/verdict/internal/sig"
)

// TestOf checks the sig_version-1 identifier for the golden corpus.
func TestOf(t *testing.T) {
	tests := []struct {
		name string
		give string
		want string
	}{
		{"multiline squid", "a\nb\nc", "6b3f339c67bb03d57a7195cf43ed86b12bfdd347"},
		{"squash and trim", "  spaced   out  ", "f08f79944119df1d56fd261bbb05d588046e44ee"},
		{"empty", "", "da39a3ee5e6b4b0d3255bfef95601890afd80709"},
		{"single", "x", "11f6ad8ec52a2984abaafd7c3b516503785c2072"},
		{"finding sig", "notes.txt\n\nUnhandled await error path here.", "1fd35ceaaa1fc0a98e1486617b35249c646ee577"},
		// Non-breaking space is not C-locale [:space:]: it survives the
		// squash on both sides (verified against tr -s '[:space:]' above).
		{"nbsp survives", "a\u00a0b", "a3256d02eb06787d15078a7ed2a32962ab7721e2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sig.Of(tt.give); got != tt.want {
				t.Errorf("Of(%q) = %s; want %s", tt.give, got, tt.want)
			}
		})
	}
}
