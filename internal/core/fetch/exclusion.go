package fetchcore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/exclude"
	"github.com/stainless-code/verdict/internal/hunks"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

const excludedSuffix = "/excluded.json"

// ExcludedPath is the domain's removed diff section.
type ExcludedPath = core.ExcludedPath

// ExcludedFile is the domain's exclusion receipt.
type ExcludedFile = core.ExcludedFile

func readConfinedFile(name string) ([]byte, error) {
	root, leaf, err := readcommon.OpenParentWith(name, os.OpenRoot)
	if err != nil {
		return nil, jq.Sealed("open bundle", err)
	}
	defer root.Close()
	f, err := readcommon.OpenRegularNoSymlinkIn(root, leaf)
	if err != nil {
		return nil, jq.Sealed("open bundle", err)
	}
	defer f.Close()
	raw, err := io.ReadAll(f)
	if err != nil {
		return nil, jq.Sealed("read bundle", err)
	}
	return raw, nil
}

func filterExcluded(ctx2 *fetchRunCtx) error {
	r, dir := ctx2.runner, ctx2.dir
	policy, err := loadExclusionPolicy(ctx2)
	if err != nil {
		return err
	}
	raw, err := readConfinedFile(dir + suffixFullDiff)
	if err != nil {
		return &core.RefusalError{Code: CodeFetchFailed, Message: err.Error()}
	}
	filtered, receipt := excludedDiff(string(raw), policy.defaults, policy.explicit, policy.attrs)
	receipt.Ignore = append([]string{}, ctx2.ignore...)
	receipt.NoDefaultExcludes = ctx2.noDefaultExcludes
	receipt.PatternsSource = exclusionSource(ctx2)
	if err := writeExcludedDiff(dir, raw, filtered, &receipt); err != nil {
		return err
	}
	if len(raw) > 0 && filtered == "" {
		r.Warn("all changed files excluded — review diff is empty (re-include with ! or --no-default-excludes)")
	}
	return nil
}

func excludedDiff(raw string, defaults, explicit []exclude.Rule, attrs []byte) (string, ExcludedFile) {
	receipt := ExcludedFile{SchemaVersion: "1", Excluded: []ExcludedPath{}}
	lines := strings.SplitAfter(raw, "\n")
	var kept strings.Builder
	cursor := 0
	files := hunks.Files(raw)
	for i := range files {
		f := &files[i]
		start, end := f.StartLine-1, min(f.EndLine, len(lines))
		kept.WriteString(strings.Join(lines[cursor:start], ""))
		section := strings.Join(lines[start:end], "")
		rule, drop := exclusionRule(defaults, explicit, attrs, f.Path, f.OldPath)
		if drop {
			receipt.Excluded = append(receipt.Excluded, ExcludedPath{
				Path: f.Path, OldPath: f.OldPath, RuleID: rule.ID, Bytes: len(section),
			})
			receipt.ExcludedBytes += len(section)
		} else {
			kept.WriteString(section)
		}
		cursor = end
	}
	kept.WriteString(strings.Join(lines[cursor:], ""))
	receipt.KeptBytes = kept.Len()
	return kept.String(), receipt
}

func excludedSet(dir string) map[string]bool {
	raw, err := readConfinedFile(dir + excludedSuffix)
	set := map[string]bool{}
	if err != nil {
		return set
	}
	var receipt ExcludedFile
	if json.Unmarshal(raw, &receipt) != nil {
		return set
	}
	for _, e := range receipt.Excluded {
		set[e.Path] = true
		set[e.OldPath] = true
	}
	return set
}

func withoutExcluded(dir, paths string) string {
	set := excludedSet(dir)
	var kept []string
	for line := range strings.SplitSeq(paths, "\n") {
		if !set[line] {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}

func exclusionTOC(dir string) string {
	raw, err := readConfinedFile(dir + excludedSuffix)
	if err != nil {
		return ""
	}
	var receipt ExcludedFile
	if json.Unmarshal(raw, &receipt) != nil || len(receipt.Excluded) == 0 {
		return ""
	}
	var out strings.Builder
	fmt.Fprintf(&out, "\n## excluded\n%d files, %d bytes — receipt: excluded.json:\n",
		len(receipt.Excluded), receipt.ExcludedBytes)
	for _, e := range receipt.Excluded {
		fmt.Fprintf(&out, "- %s (%s, %d bytes)\n", e.Path, e.RuleID, e.Bytes)
	}
	return out.String()
}

func writeExcludedReceipt(dir string, receipt *ExcludedFile) error {
	receipt.RawDiffSHA256 = sha256Hex(dir + suffixRawDiff)
	receipt.DiffSHA256 = sha256Hex(dir + suffixFullDiff)
	data, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return jq.Sealed("encode excluded receipt", err)
	}
	if err := atomic.WriteFile(dir+excludedSuffix, append(data, '\n')); err != nil {
		return err
	}
	return nil
}

func exclusionRule(defaults, explicit []exclude.Rule, attrs []byte, file, old string) (exclude.Rule, bool) {
	rule, drop := exclude.Excluded(defaults, file, old)
	if exclude.Attributes(attrs, file) || exclude.Attributes(attrs, old) {
		rule, drop = exclude.Rule{ID: "default:attribute"}, true
	}
	if match, yes := exclude.Excluded(explicit, file, old); match.ID != "" {
		return match, yes
	}
	return rule, drop
}

func phaseTwoFiltered(
	ctx context.Context, fc *fetchRunCtx, req *core.FetchRequest, flags *core.FetchFlags, dir string, budget *budgetState,
) error {
	if err := phaseTwo(ctx, fc, req, flags, dir, budget); err != nil {
		return err
	}
	return filterExcluded(fc)
}

func exclusionControl(r *session, name string) ([]byte, error) {
	if r.controls == nil || !r.controls.Has(name) {
		return nil, nil
	}
	raw, err := readConfinedFile(r.controls.Path(name))
	if err != nil {
		return nil, &core.RefusalError{Code: "bad-ignore", Message: "cannot read exclusion control: " + err.Error()}
	}
	return raw, nil
}

func exclusionManifest(dir string, manifest *jq.Obj) {
	raw, err := readConfinedFile(dir + excludedSuffix)
	if err != nil {
		return
	}
	var receipt ExcludedFile
	if json.Unmarshal(raw, &receipt) != nil {
		return
	}
	manifest.Set("excluded_count", len(receipt.Excluded))
	manifest.Set("excluded_bytes", receipt.ExcludedBytes)
	manifest.Set("raw_diff_sha256", receipt.RawDiffSHA256)
	manifest.Set("diff_sha256", receipt.DiffSHA256)
}

func writeExcludedDiff(dir string, raw []byte, filtered string, receipt *ExcludedFile) error {
	if err := atomic.WriteFile(dir+suffixRawDiff, raw); err != nil {
		return err
	}
	if err := atomic.WriteFile(dir+suffixFullDiff, []byte(filtered)); err != nil {
		return err
	}
	if err := writeExcludedReceipt(dir, receipt); err != nil {
		return err
	}
	return nil
}

func ignoreRefusal(err error) *core.RefusalError {
	if errors.Is(err, exclude.ErrTooLarge) {
		return &core.RefusalError{Code: "ignore-too-large", Message: err.Error()}
	}
	return &core.RefusalError{Code: "bad-ignore", Message: err.Error()}
}

type diffExclusions struct {
	defaults, explicit []exclude.Rule
	attrs              []byte
}

func loadExclusionPolicy(fc *fetchRunCtx) (*diffExclusions, error) {
	raw, err := exclusionControl(fc.runner, ".verdict/ignore")
	if err != nil {
		return nil, err
	}
	explicit, err := exclude.ParseIgnore(raw, "ignore")
	if err != nil {
		return nil, ignoreRefusal(err)
	}
	cli, err := exclude.ParseIgnore([]byte(strings.Join(fc.ignore, newline)), "cli")
	if err != nil {
		return nil, ignoreRefusal(err)
	}
	attrs, err := exclusionControl(fc.runner, ".gitattributes")
	if err != nil {
		return nil, err
	}
	out := &diffExclusions{defaults: exclude.Defaults(), explicit: append(explicit, cli...), attrs: attrs}
	if fc.noDefaultExcludes {
		out.defaults = nil
		out.attrs = nil
	}
	return out, nil
}

func exclusionSource(fc *fetchRunCtx) string {
	if len(fc.ignore) > 0 {
		return "cli"
	}
	if fc.runner.controls != nil && fc.runner.controls.File.Policy == "head" {
		return ".verdict/ignore@head"
	}
	return ".verdict/ignore@base"
}
