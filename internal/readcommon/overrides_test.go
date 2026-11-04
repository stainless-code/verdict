package readcommon

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

// writeFragment writes one overrides fragment into dir.
func writeFragment(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// refusalCode returns the typed refusal code, or "" when err is not one.
func refusalCode(err error) string {
	var ref *core.RefusalError
	if !errors.As(err, &ref) {
		return ""
	}
	return ref.Code
}

// TestValidateOverridesAtReadsTheGivenDirectory proves the snapshot-rooted
// validator: a bad fragment in the given overrides dir refuses, the same
// fragment shape in the worktree default is never consulted, and a clean
// snapshot passes.
func TestValidateOverridesAtReadsTheGivenDirectory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFragment(t, dir, "footer-order.md", "tier\nbogus\n")

	err := ValidateOverridesAt(dir)
	if got := refusalCode(err); got != "bad-override" {
		t.Fatalf("ValidateOverridesAt code = %q (%v), want bad-override", got, err)
	}
	if !strings.Contains(err.Error(), "footer-order.md") || !strings.Contains(err.Error(), "bogus") {
		t.Errorf("refusal = %q, want the logical fragment path and the bad field", err)
	}

	clean := t.TempDir()
	writeFragment(t, clean, "footer-order.md", "tier\ncost\nfiles\nlink\n")
	writeFragment(t, clean, "walkthrough-threshold.md", "files: 8\nbytes: 24576\n")
	writeFragment(t, clean, "preamble.md", "Reviewed with verdict {version} — {head_sha_short}.\n")
	writeFragment(t, clean, "attribution.md", "Attribution: {attribution}\n")
	if err := ValidateOverridesAt(clean); err != nil {
		t.Errorf("clean snapshot refused: %v", err)
	}
}

// overrideShapeCase is one fragment-posture fixture.
type overrideShapeCase struct {
	name    string
	seed    func(t *testing.T, dir string)
	want    string
	wantMsg string
}

// checkOverrideShape seeds one case and checks its refusal posture.
func checkOverrideShape(t *testing.T, tc overrideShapeCase) {
	t.Helper()
	dir := t.TempDir()
	tc.seed(t, dir)
	err := ValidateOverridesAt(dir)
	if got := refusalCode(err); got != tc.want {
		t.Fatalf("code = %q (%v), want %q", got, err, tc.want)
	}
	if tc.wantMsg != "" && !strings.Contains(err.Error(), tc.wantMsg) {
		t.Errorf("refusal = %q, want it to name %q", err, tc.wantMsg)
	}
}

// TestValidateOverridesAtFragmentShapes proves the two fragment-posture
// rules every copy shares: a directory in a fragment's
// place reads as no fragment at all, and a present fragment still runs the
// locked-block and placeholder inventories.
func TestValidateOverridesAtFragmentShapes(t *testing.T) {
	t.Parallel()
	tests := []overrideShapeCase{
		{
			name: "directory-is-absent",
			seed: func(t *testing.T, dir string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Join(dir, "preamble.md"), 0o755); err != nil {
					t.Fatal(err)
				}
			},
		},
		{name: "absent-is-clean", seed: func(*testing.T, string) {}},
		{
			name: "locked-marker",
			seed: func(t *testing.T, dir string) {
				t.Helper()
				writeFragment(t, dir, "preamble.md", "> ✅ verdict\n")
			},
			want:    "bad-override",
			wantMsg: "marker-first-line",
		},
		{
			name: "unknown-placeholder",
			seed: func(t *testing.T, dir string) {
				t.Helper()
				writeFragment(t, dir, "attribution.md", "Attribution: {nope}\n")
			},
			want:    "bad-settings",
			wantMsg: "unknown placeholder",
		},
		{
			// The placeholder inventory is not the attribution file's own
			// rule: the preamble carries the v1 placeholders too.
			name: "preamble-placeholder",
			seed: func(t *testing.T, dir string) {
				t.Helper()
				writeFragment(t, dir, "preamble.md",
					"Reviewed with verdict {version} — {head_sha_short} {nope}.\n")
			},
			want:    "bad-settings",
			wantMsg: "unknown placeholder",
		},
		{
			name: "multi-line-attribution",
			seed: func(t *testing.T, dir string) {
				t.Helper()
				writeFragment(t, dir, "attribution.md", "one\ntwo\n")
			},
			want:    "bad-override",
			wantMsg: "attribution-single-line",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkOverrideShape(t, tc)
		})
	}
}
