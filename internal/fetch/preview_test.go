package fetch

import (
	"bytes"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

// TestWritePreview pins the human preview block byte for byte: the header
// counts, one row per reviewed file, one row per withheld section carrying its
// rule, pattern, and reason, the resolved budget with the classifier's reasons,
// and the closing promise that nothing was written. The expected text is a
// literal because an operator reads it, so a reworded line is a contract change
// and has to show up here.
func TestWritePreview(t *testing.T) {
	tests := []struct {
		name    string
		preview *core.PreviewData
		want    string
	}{
		{
			name: "one reviewed file and one withheld secret",
			preview: &core.PreviewData{
				SchemaVersion: "1",
				Included:      []core.PreviewFile{{Path: "a.txt", Role: "docs", Bytes: 69}},
				Excluded: []core.ExcludedPath{{
					Path: ".env", OldPath: ".env", RuleID: "default:secret",
					Pattern: ".env", Reason: "secret-builtin", Bytes: 69,
				}},
				IncludedBytes: 69,
				ExcludedBytes: 69,
				Files:         1,
				Budget:        core.PreviewBudget{Tier: "light", Reasons: []string{"docs-or-test-only"}, Finders: 1, CrossCheck: "off"},
			},
			want: "preview: 1 included, 1 excluded (69 bytes reviewed, 69 excluded)\n" +
				"  include a.txt (docs, 69 bytes)\n" +
				"  exclude .env (default:secret, .env, secret-builtin, 69 bytes)\n" +
				"budget: light — 1 finder(s), cross-check off (docs-or-test-only)\n" +
				"no bundle written: preview stops before the bundle directory\n",
		},
		{
			name: "nothing kept, two reasons, no included row",
			preview: &core.PreviewData{
				SchemaVersion: "1",
				Included:      []core.PreviewFile{},
				Excluded: []core.ExcludedPath{
					{Path: "vendor/lib.go", RuleID: "default:generated", Pattern: "vendor/**", Reason: "default", Bytes: 120},
					{Path: "auth/login.go", RuleID: "cli:1", Pattern: "auth/**", Reason: "cli", Bytes: 90},
				},
				IncludedBytes: 0,
				ExcludedBytes: 210,
				Budget:        core.PreviewBudget{Tier: "standard", Reasons: []string{"walkthrough-threshold: 40/4096", "sensitive-path: **/auth/**"}, Finders: 2, CrossCheck: "on"},
			},
			want: "preview: 0 included, 2 excluded (0 bytes reviewed, 210 excluded)\n" +
				"  exclude vendor/lib.go (default:generated, vendor/**, default, 120 bytes)\n" +
				"  exclude auth/login.go (cli:1, auth/**, cli, 90 bytes)\n" +
				"budget: standard — 2 finder(s), cross-check on (walkthrough-threshold: 40/4096, sensitive-path: **/auth/**)\n" +
				"no bundle written: preview stops before the bundle directory\n",
		},
		{
			name: "no reasons to print",
			preview: &core.PreviewData{
				SchemaVersion: "1",
				Included:      []core.PreviewFile{{Path: "src/app.go", Role: "source", Bytes: 40}},
				Excluded:      []core.ExcludedPath{},
				IncludedBytes: 40,
				Files:         1,
				Budget:        core.PreviewBudget{Tier: "light", Reasons: []string{}, Finders: 1, CrossCheck: "off"},
			},
			want: "preview: 1 included, 0 excluded (40 bytes reviewed, 0 excluded)\n" +
				"  include src/app.go (source, 40 bytes)\n" +
				"budget: light — 1 finder(s), cross-check off\n" +
				"no bundle written: preview stops before the bundle directory\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			writePreview(&out, tt.preview)
			if got := out.String(); got != tt.want {
				t.Fatalf("writePreview =\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}
