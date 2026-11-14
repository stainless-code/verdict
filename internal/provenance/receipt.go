package provenance

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
)

// ReceiptFile is the per-bundle provenance receipt file name.
const ReceiptFile = "provenance.json"

const maxReceiptBytes = 4 * 1024 * 1024

// KBCandidateInput describes one KB rule candidate evaluated during fetch.
type KBCandidateInput struct {
	ID     string
	Status string // "included" | "omitted"
	Reason string // "" | "top_n"
}

// KBRecallInput summarizes KB recall results without rule text.
type KBRecallInput struct {
	Candidates    []KBCandidateInput
	Cap           *int
	Reason        string
	Included      int
	Omitted       int
	CharsIncluded int
	CharsTotal    int
	Truncated     bool
	NotesWritten  bool
}

// ControlInput describes one control snapshot entry.
type ControlInput struct {
	Path   string
	Status string
	Source string
	SHA256 string
	Bytes  int64
}

// ConfigOriginInput describes one winning config field origin.
type ConfigOriginInput struct {
	Key    string
	Layer  string
	Source string
}

// ExclusionsInput describes excluded.json without raw file content.
type ExclusionsInput struct {
	Present           bool
	RawDiffSHA256     string
	DiffSHA256        string
	PatternsSource    string
	Excluded          int
	ExcludedBytes     int64
	KeptBytes         int64
	Defaults          bool
	Ignore            bool
	NoDefaultExcludes bool
}

// ReceiptBuildInput carries all fetch-time inputs needed to build provenance.json.
type ReceiptBuildInput struct {
	Root         string
	Dir          string
	HeadSHA      string
	BaseSHA      string
	PanelSource  string
	Panel        []string
	ConfigSHA256 string
	ConfigLayers []string
	ConfigValues []ConfigOriginInput
	Controls     []ControlInput
	Overrides    []ControlInput
	Learnings    *ControlInput
	Exclusions   ExclusionsInput
	KB           *KBRecallInput
	BundleTrunc  *core.Truncation
}

// BuildReceipt constructs the deterministic ProvenanceReceipt for a fetched bundle.
func BuildReceipt(in *ReceiptBuildInput) *core.ProvenanceReceipt {
	promptsHash := PromptsHash(in.Root)
	prompts := promptInputEntries(in.Root)
	bundles := bundleInputEntries(in.Dir)
	kbEntries, kbTruncRows := kbInputsAndTruncated(in.Dir, in.KB)
	learnEntry := learningsInputEntry(in.Learnings)
	overrideEntries := controlEntries("override", in.Overrides)
	ctrlEntries := controlEntries("control", in.Controls)
	cfgEntries := configInputEntries(in)
	exclEntries := exclusionsInputEntries(in.Dir, &in.Exclusions)

	inputs := make([]core.ProvenanceInput, 0,
		len(prompts)+1+len(bundles)+len(kbEntries)+1+len(overrideEntries)+len(ctrlEntries)+len(cfgEntries)+len(exclEntries))
	inputs = append(inputs, prompts...)
	inputs = append(inputs, panelInputEntry(in.PanelSource, in.Panel))
	inputs = append(inputs, bundles...)
	inputs = append(inputs, kbEntries...)
	inputs = append(inputs, learnEntry)
	inputs = append(inputs, overrideEntries...)
	inputs = append(inputs, ctrlEntries...)
	inputs = append(inputs, cfgEntries...)
	inputs = append(inputs, exclEntries...)

	truncated := append([]core.ProvenanceTruncatedRow{}, kbTruncRows...)
	if in.BundleTrunc != nil && in.BundleTrunc.Truncated {
		truncated = append(truncated, core.NewProvenanceTruncatedRow("bundle", "", "truncated", in.BundleTrunc.TruncationReason, in.BundleTrunc))
	}

	return &core.ProvenanceReceipt{
		SchemaVersion: "1",
		RunID:         computeRunID(in, promptsHash, learnEntry),
		PromptsHash:   promptsHash,
		Panel:         slices.Clone(in.Panel),
		Inputs:        inputs,
		Truncated:     truncated,
	}
}

// WriteReceipt persists provenance.json atomically in dir.
func WriteReceipt(dir string, receipt *core.ProvenanceReceipt) error {
	if receipt == nil {
		return nil
	}
	raw, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", ReceiptFile, err)
	}
	path := filepath.Join(dir, ReceiptFile)
	if err := atomic.WriteFile(path, append(raw, '\n')); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// ReadReceipt loads and validates provenance.json from dir.
// When the file does not exist, exists is false and err is nil.
// When the file exists but is malformed/oversized, exists is true and err is non-nil.
func ReadReceipt(dir string) (receipt *core.ProvenanceReceipt, exists bool, err error) {
	path := filepath.Join(dir, ReceiptFile)
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, true, fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, maxReceiptBytes+1))
	if err != nil {
		return nil, true, fmt.Errorf("read %s: %w", path, err)
	}
	if len(raw) > maxReceiptBytes {
		return nil, true, fmt.Errorf("%s exceeds %d bytes", path, maxReceiptBytes)
	}
	var out core.ProvenanceReceipt
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return nil, true, fmt.Errorf("parse %s: %w", path, err)
	}
	if out.SchemaVersion == "" || out.Inputs == nil {
		return nil, true, fmt.Errorf("%s missing schema_version or inputs", path)
	}
	if out.Truncated == nil {
		out.Truncated = []core.ProvenanceTruncatedRow{}
	}
	return &out, true, nil
}

// EmptyReceipt returns a degraded empty receipt for malformed/missing bundles.
func EmptyReceipt() *core.ProvenanceReceipt {
	return &core.ProvenanceReceipt{
		SchemaVersion: "1",
		Inputs:        []core.ProvenanceInput{},
		Truncated:     []core.ProvenanceTruncatedRow{},
	}
}

func promptInputEntries(root string) []core.ProvenanceInput {
	out := make([]core.ProvenanceInput, 0, len(promptFiles))
	for _, f := range promptFiles {
		raw, err := readPromptFile(root, f)
		if err != nil {
			n := int64(0)
			out = append(out, core.ProvenanceInput{
				Kind: "prompt", Name: f, Status: "absent", Bytes: &n,
			})
			continue
		}
		out = append(out, core.ProvenanceInput{
			Kind: "prompt", Name: f, Status: "included", SHA256: shaHex(raw), Bytes: new(int64(len(raw))),
		})
	}
	return out
}

func panelInputEntry(source string, members []string) core.ProvenanceInput {
	status := "included"
	if len(members) == 0 {
		status = "absent"
	}
	return core.ProvenanceInput{
		Kind:    "panel",
		Name:    "panel",
		Status:  status,
		Source:  source,
		Members: slices.Clone(members),
	}
}

func bundleInputEntries(dir string) []core.ProvenanceInput {
	if dir == "" {
		return nil
	}
	var rels []string
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d == nil {
			return nil
		}
		rel, rerr := filepath.Rel(dir, path)
		if rerr != nil || rel == "." {
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		if d.IsDir() {
			if relSlash == "controls" || strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if relSlash == ReceiptFile || strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		rels = append(rels, relSlash)
		return nil
	})
	slices.Sort(rels)
	out := make([]core.ProvenanceInput, 0, len(rels))
	for _, rel := range rels {
		raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			continue
		}
		out = append(out, core.ProvenanceInput{
			Kind:   "bundle",
			Name:   rel,
			Status: "included",
			SHA256: shaHex(raw),
			Bytes:  new(int64(len(raw))),
		})
	}
	return out
}

func kbInputsAndTruncated(dir string, kbIn *KBRecallInput) ([]core.ProvenanceInput, []core.ProvenanceTruncatedRow) {
	if kbIn == nil {
		return []core.ProvenanceInput{{
			Kind:     "kb",
			Name:     "kb.txt",
			Status:   "absent",
			Included: new(0),
			Omitted:  new(0),
		}}, nil
	}
	status := "absent"
	if kbIn.Included > 0 {
		status = "included"
	}
	if kbIn.Truncated {
		status = "truncated"
	}
	summary := core.ProvenanceInput{
		Kind:     "kb",
		Name:     "kb.txt",
		Status:   status,
		Reason:   kbIn.Reason,
		Included: new(kbIn.Included),
		Omitted:  new(kbIn.Omitted),
	}
	if kbIn.CharsTotal > 0 || kbIn.Cap != nil {
		summary.CharsIncluded = new(kbIn.CharsIncluded)
		summary.CharsTotal = new(kbIn.CharsTotal)
	}
	if kbIn.Cap != nil {
		summary.Cap = new(*kbIn.Cap)
	}
	if raw, err := os.ReadFile(filepath.Join(dir, "kb.txt")); err == nil {
		summary.SHA256 = shaHex(raw)
		summary.Bytes = new(int64(len(raw)))
	}

	entries := make([]core.ProvenanceInput, 0, 1+len(kbIn.Candidates))
	entries = append(entries, summary)
	var truncRows []core.ProvenanceTruncatedRow
	totalCands := len(kbIn.Candidates)
	if kbIn.Truncated && kbIn.Cap != nil {
		cut := core.ResultCut("response_cap", kbIn.CharsIncluded, new(kbIn.CharsTotal), nil)
		row := core.NewProvenanceTruncatedRow("kb", "", "truncated", "cap", cut)
		row.CharsIncluded = new(kbIn.CharsIncluded)
		row.CharsTotal = new(kbIn.CharsTotal)
		row.Cap = new(*kbIn.Cap)
		truncRows = append(truncRows, row)
	}
	for _, c := range kbIn.Candidates {
		entries = append(entries, core.ProvenanceInput{
			Kind:   "kb_candidate",
			Name:   c.ID,
			ID:     c.ID,
			Status: c.Status,
			Reason: c.Reason,
		})
		if c.Status == "omitted" && c.Reason == "top_n" {
			cut := core.ResultCut("response_cap", kbIn.Included, new(totalCands), nil)
			row := core.NewProvenanceTruncatedRow("kb_candidate", c.ID, "omitted", "top_n", cut)
			row.Included = new(kbIn.Included)
			row.Omitted = new(kbIn.Omitted)
			truncRows = append(truncRows, row)
		}
	}
	return entries, truncRows
}

func learningsInputEntry(in *ControlInput) core.ProvenanceInput {
	// Learnings is never truncated by any cap; report "included" with byte count.
	n := int64(0)
	entry := core.ProvenanceInput{
		Kind:   "learnings",
		Name:   ".verdict/learnings.md",
		Status: "included",
		Bytes:  &n,
	}
	if in != nil {
		entry.Source = in.Source
		entry.SHA256 = in.SHA256
		entry.Bytes = new(in.Bytes)
		if in.Status == "unreadable" {
			entry.Status = "unreadable"
		}
	}
	return entry
}

func controlEntries(kind string, items []ControlInput) []core.ProvenanceInput {
	sorted := slices.Clone(items)
	slices.SortFunc(sorted, func(a, b ControlInput) int {
		return strings.Compare(a.Path, b.Path)
	})
	out := make([]core.ProvenanceInput, 0, len(sorted))
	for _, it := range sorted {
		status := it.Status
		if kind == "override" {
			if status == "used" || status == "base" || status == "head" {
				status = "included"
			}
		}
		entry := core.ProvenanceInput{
			Kind:   kind,
			Name:   it.Path,
			Status: status,
			Source: it.Source,
			SHA256: it.SHA256,
		}
		if it.Bytes > 0 || status == "included" || status == "used" || status == "base" || status == "head" {
			entry.Bytes = new(it.Bytes)
		}
		out = append(out, entry)
	}
	return out
}

func configInputEntries(in *ReceiptBuildInput) []core.ProvenanceInput {
	layerSummary := strings.Join(in.ConfigLayers, ",")
	if layerSummary == "" {
		layerSummary = "built-in"
	}
	status := "included"
	if in.ConfigSHA256 == "" {
		status = "built-in"
	}
	parts := make([]string, 0, len(in.ConfigValues))
	sortedVals := slices.Clone(in.ConfigValues)
	slices.SortFunc(sortedVals, func(a, b ConfigOriginInput) int {
		return strings.Compare(a.Key, b.Key)
	})
	for _, v := range sortedVals {
		if v.Layer != "" {
			parts = append(parts, v.Key+"="+v.Layer)
		}
	}
	return []core.ProvenanceInput{{
		Kind:   "config",
		Name:   ".verdict/config.json",
		Status: status,
		SHA256: in.ConfigSHA256,
		Layer:  layerSummary,
		Detail: strings.Join(parts, ", "),
	}}
}

func exclusionsInputEntries(dir string, ex *ExclusionsInput) []core.ProvenanceInput {
	if !ex.Present {
		return []core.ProvenanceInput{{
			Kind:     "exclusions",
			Name:     "excluded.json",
			Status:   "absent",
			Excluded: new(0),
		}}
	}
	entry := core.ProvenanceInput{
		Kind:          "exclusions",
		Name:          "excluded.json",
		Status:        "included",
		Source:        ex.PatternsSource,
		Excluded:      new(ex.Excluded),
		ExcludedBytes: new(ex.ExcludedBytes),
		KeptBytes:     new(ex.KeptBytes),
	}
	if raw, err := os.ReadFile(filepath.Join(dir, "excluded.json")); err == nil {
		entry.SHA256 = shaHex(raw)
		entry.Bytes = new(int64(len(raw)))
	}
	return []core.ProvenanceInput{entry}
}

func computeRunID(in *ReceiptBuildInput, promptsHash string, learn core.ProvenanceInput) string {
	h := sha256.New()
	writeKV(h, "prompts_hash", promptsHash)
	writeKV(h, "head_sha", in.HeadSHA)
	writeKV(h, "base_sha", in.BaseSHA)
	if raw, err := os.ReadFile(filepath.Join(in.Dir, "full.diff")); err == nil {
		writeKV(h, "full_diff_sha256", shaHex(raw))
	}
	writeKV(h, "panel_source", in.PanelSource)
	writeKV(h, "panel", strings.Join(in.Panel, ","))
	writeKV(h, "config_sha256", in.ConfigSHA256)
	sortedVals := slices.Clone(in.ConfigValues)
	slices.SortFunc(sortedVals, func(a, b ConfigOriginInput) int { return strings.Compare(a.Key, b.Key) })
	for _, v := range sortedVals {
		writeKV(h, "cfg:"+v.Key, v.Layer+":"+v.Source)
	}
	sortedCtrls := slices.Clone(in.Controls)
	slices.SortFunc(sortedCtrls, func(a, b ControlInput) int { return strings.Compare(a.Path, b.Path) })
	for _, c := range sortedCtrls {
		writeKV(h, "ctrl:"+c.Path, c.Status+":"+c.SHA256)
	}
	sortedOvers := slices.Clone(in.Overrides)
	slices.SortFunc(sortedOvers, func(a, b ControlInput) int { return strings.Compare(a.Path, b.Path) })
	for _, o := range sortedOvers {
		writeKV(h, "over:"+o.Path, o.Status+":"+o.SHA256)
	}
	writeKV(h, "learnings", learn.Status+":"+learn.SHA256)
	if in.Exclusions.Present {
		writeKV(h, "excl", fmt.Sprintf("%s:%s:%d:%s",
			in.Exclusions.RawDiffSHA256, in.Exclusions.DiffSHA256, in.Exclusions.Excluded, in.Exclusions.PatternsSource))
	}
	if in.KB != nil {
		writeKV(h, "kb_summary", fmt.Sprintf("%d:%d:%v:%d:%d",
			in.KB.Included, in.KB.Omitted, in.KB.Truncated, in.KB.CharsIncluded, in.KB.CharsTotal))
		if in.KB.Cap != nil {
			writeKV(h, "kb_cap", strconv.Itoa(*in.KB.Cap))
		}
		for _, c := range in.KB.Candidates {
			writeKV(h, "kb_cand:"+c.ID, c.Status+":"+c.Reason)
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

func writeKV(w io.Writer, k, v string) {
	_, _ = fmt.Fprintf(w, "%s=%s\n", k, v)
}

func shaHex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// RenderInputsFooterLine formats the single compact `> Inputs:` footer line
// from provenance.json. Old bundles without provenance.json return "" so the
// rendered footer stays byte-identical.
func RenderInputsFooterLine(dir string) string {
	if dir == "" {
		return ""
	}
	r, ok, err := ReadReceipt(dir)
	if !ok || err != nil || r == nil {
		return ""
	}
	var promptsCount, kbInc, kbOm, exclCount, overCount, ctrlCount int
	var kbTrunc bool
	var panelStr, learnStatus, cfgLayer string
	for i := range r.Inputs {
		in := &r.Inputs[i]
		switch in.Kind {
		case "prompt":
			if in.Status == "included" {
				promptsCount++
			}
		case "panel":
			if len(in.Members) > 0 {
				panelStr = strings.Join(in.Members, ",")
			}
		case "kb":
			if in.Included != nil {
				kbInc = *in.Included
			}
			if in.Omitted != nil {
				kbOm = *in.Omitted
			}
			kbTrunc = in.Status == "truncated"
		case "learnings":
			learnStatus = in.Status
		case "override":
			if in.Status == "included" {
				overCount++
			}
		case "control":
			if in.Status != "absent" {
				ctrlCount++
			}
		case "config":
			cfgLayer = in.Layer
		case "exclusions":
			if in.Excluded != nil {
				exclCount = *in.Excluded
			}
		}
	}
	if panelStr == "" {
		panelStr = "none"
	}
	if learnStatus == "" {
		learnStatus = "included"
	}
	if cfgLayer == "" {
		cfgLayer = "built-in"
	}
	kbPart := fmt.Sprintf("kb %d", kbInc)
	if kbOm > 0 || kbTrunc {
		kbPart = fmt.Sprintf("kb %d/%d", kbInc, kbInc+kbOm)
		if kbTrunc {
			kbPart += " (truncated)"
		} else if kbOm > 0 {
			kbPart += fmt.Sprintf(" (%d omitted)", kbOm)
		}
	}
	runShort := r.RunID
	if len(runShort) > 12 {
		runShort = runShort[:12]
	}
	promptHashShort := r.PromptsHash
	if len(promptHashShort) > 7 {
		promptHashShort = promptHashShort[:7]
	}
	return fmt.Sprintf("> Inputs: run %s · prompts %d (%s) · panel %s · %s · learnings %s · overrides %d · controls %d · config %s · excluded %d",
		runShort, promptsCount, promptHashShort, panelStr, kbPart, learnStatus, overCount, ctrlCount, cfgLayer, exclCount)
}

// MergeTruncated appends any bundle truncation rows not already captured in r.Truncated.
func MergeTruncated(r *core.ProvenanceReceipt, extra *core.Truncation) []core.ProvenanceTruncatedRow {
	var base []core.ProvenanceTruncatedRow
	if r != nil {
		base = append(base, r.Truncated...)
	}
	if extra == nil || !extra.Truncated {
		if base == nil {
			return []core.ProvenanceTruncatedRow{}
		}
		return base
	}
	for _, row := range base {
		if row.Input == "bundle" && row.Reason == extra.TruncationReason {
			return base
		}
	}
	base = append(base, core.NewProvenanceTruncatedRow("bundle", "", "truncated", extra.TruncationReason, extra))
	return base
}

// FormatTable renders the full local provenance & truncation tables for
// `report --provenance` and `post --provenance`.
func FormatTable(r *core.ProvenanceReceipt, truncated []core.ProvenanceTruncatedRow) []string {
	if r == nil {
		r = EmptyReceipt()
	}
	lines := []string{fmt.Sprintf("provenance: run_id=%s prompts_hash=%s", r.RunID, r.PromptsHash)}
	lines = append(lines, "KIND\tNAME\tSTATUS\tBYTES\tSHA256\tDETAIL")
	for i := range r.Inputs {
		in := &r.Inputs[i]
		lines = append(lines, formatInputRow(in))
	}
	if len(truncated) == 0 {
		lines = append(lines, "truncated: none")
		return lines
	}
	lines = append(lines, fmt.Sprintf("truncated: %d", len(truncated)))
	lines = append(lines, "INPUT\tID\tSTATUS\tREASON\tDETAIL")
	for i := range truncated {
		lines = append(lines, formatTruncatedRow(&truncated[i]))
	}
	return lines
}

func formatInputRow(in *core.ProvenanceInput) string {
	bytesCol := "-"
	if in.Bytes != nil {
		bytesCol = strconv.FormatInt(*in.Bytes, 10)
	}
	shaCol := "-"
	if in.SHA256 != "" {
		shaCol = in.SHA256
		if len(shaCol) > 12 {
			shaCol = shaCol[:12]
		}
	}
	var details []string
	if in.Reason != "" {
		details = append(details, "reason="+in.Reason)
	}
	if in.Included != nil {
		details = append(details, "included="+strconv.Itoa(*in.Included))
	}
	if in.Omitted != nil {
		details = append(details, "omitted="+strconv.Itoa(*in.Omitted))
	}
	if in.CharsIncluded != nil {
		details = append(details, "chars_included="+strconv.Itoa(*in.CharsIncluded))
	}
	if in.CharsTotal != nil {
		details = append(details, "chars_total="+strconv.Itoa(*in.CharsTotal))
	}
	if in.Cap != nil {
		details = append(details, "cap="+strconv.Itoa(*in.Cap))
	}
	if in.Excluded != nil {
		details = append(details, "excluded="+strconv.Itoa(*in.Excluded))
	}
	if in.Layer != "" {
		details = append(details, "layer="+in.Layer)
	}
	if len(in.Members) > 0 {
		details = append(details, "members="+strings.Join(in.Members, ","))
	}
	if in.Detail != "" {
		details = append(details, in.Detail)
	}
	detailCol := strings.Join(details, " ")
	if detailCol == "" {
		detailCol = "-"
	}
	return fmt.Sprintf("%s\t%s\t%s\t%s\t%s\t%s", in.Kind, in.Name, in.Status, bytesCol, shaCol, detailCol)
}

func formatTruncatedRow(row *core.ProvenanceTruncatedRow) string {
	idCol := row.ID
	if idCol == "" {
		idCol = "-"
	}
	var details []string
	if row.Included != nil {
		details = append(details, "included="+strconv.Itoa(*row.Included))
	}
	if row.Omitted != nil {
		details = append(details, "omitted="+strconv.Itoa(*row.Omitted))
	}
	if row.CharsIncluded != nil {
		details = append(details, "chars_included="+strconv.Itoa(*row.CharsIncluded))
	}
	if row.CharsTotal != nil {
		details = append(details, "chars_total="+strconv.Itoa(*row.CharsTotal))
	}
	if row.Cap != nil {
		details = append(details, "cap="+strconv.Itoa(*row.Cap))
	}
	details = append(details, fmt.Sprintf("truncation_reason=%s returned=%d", row.TruncationReason, row.Returned))
	if row.Total != nil {
		details = append(details, "total="+strconv.Itoa(*row.Total))
	}
	return fmt.Sprintf("%s\t%s\t%s\t%s\t%s", row.Input, idCol, row.Status, row.Reason, strings.Join(details, " "))
}
