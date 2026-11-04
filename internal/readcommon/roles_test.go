package readcommon_test

import (
	"testing"

	"github.com/stainless-code/verdict/internal/readcommon"
)

// TestPathRole pins the shared roles_for_paths heuristic: one table for the
// fetch roles build and the report overview, in branch order.
func TestPathRole(t *testing.T) {
	t.Parallel()
	tests := []struct{ path, want string }{
		{"src/app.go", "source"},
		{"pkg/test/helper.go", "test"},
		{"spec/models/user_spec.rb", "test"},
		{"test_helpers.py", "test"},
		{"src/app_test.go", "test"},
		{"package-lock.json", "generated"},
		{"vendor/github.com/x/y.go", "generated"},
		{"web/app.min.js", "generated"},
		{"docs/manual.md", "docs"},
		{"README.md", "docs"},
		{"go.mod", "config"},
		{".prettierrc", "config"},
		{"ci/workflows.yml", "config"},
		{"", "source"},
	}
	for _, tt := range tests {
		if got := readcommon.PathRole(tt.path); got != tt.want {
			t.Errorf("PathRole(%q) = %q; want %q", tt.path, got, tt.want)
		}
	}
}
