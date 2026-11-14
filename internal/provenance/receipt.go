package provenance

import (
	"bytes"
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
	"unicode/utf8"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
)

// ReceiptFile is the per-bundle provenance receipt file name.
const ReceiptFile = "provenance.json"

const (
	maxReceiptBytes = 4 * 1024 * 1024
	statusIncluded  = "included"
	statusAbsent    = "absent"
	statusTruncated = "truncated"
	statusOmitted   = "omitted"
	statusUsed      = "used"
	kindPrompt      = "prompt"
	kindPanel       = "panel"
	kindKB          = "kb"
	kindBundle      = "bundle"
	kindExclusions  = "exclusions"
	layerBuiltIn    = "built-in"
	runShortLen     = 12
	hashShortLen    = 7
	sepComma        = ","
	sepColon        = ":"
	dash            = "-"
)

type receiptError struct {
	err error
	msg string
}

func (e *receiptError) Error() string {
	if e.err == nil {
		return e.msg
	}
	return e.msg + ": " + e.err.Error()
}

func (e *receiptError) Unwrap() error { return e.err }

func wrapReceiptErr(msg string, err error) error {
	return &receiptError{msg: msg, err: err}
}

// KBCandidateInput describes one KB rule candidate evaluated during fetch.
type KBCandidateInput struct {
	ID     string
	Status string // "included" | "omitted" | "truncated"
	Reason string // "" | "top_n" | "cap"
}

// KBRecallInput summarizes KB recall results without rule text.
type KBRecallInput struct {
	Reason        string
	Cap           *int
	Candidates    []KBCandidateInput
	Included      int
	Omitted       int
	CharsIncluded int
	CharsTotal    int
	Truncated     bool
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
	RawDiffSHA256  string
	DiffSHA256     string
	PatternsSource string
	Excluded       int
	ExcludedBytes  int64
	KeptBytes      int64
	Present        bool
}

// ReceiptBuildInput holds fetch-time inputs for building provenance.json.
type ReceiptBuildInput struct {
	Learnings    *ControlInput
	KB           *KBRecallInput
	BundleTrunc  *core.Truncation
	Root         string
	Dir          string
	HeadSHA      string
	BaseSHA      string
	PanelSource  string
	ConfigSHA256 string
	Panel        []string
	ConfigLayers []string
	ConfigValues []ConfigOriginInput
	Controls     []ControlInput
	Overrides    []ControlInput
	Exclusions   ExclusionsInput
}

// BuildReceipt builds the deterministic ProvenanceReceipt for a bundle.
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
		row := core.NewProvenanceTruncatedRow(
			kindBundle, "", statusTruncated, in.BundleTrunc.TruncationReason, in.BundleTrunc,
		)
		truncated = append(truncated, row)
	}

	return &core.ProvenanceReceipt{
		SchemaVersion: "1",
		RunID:         computeRunID(in, promptsHash, &learnEntry),
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
		return wrapReceiptErr("encode "+ReceiptFile, err)
	}
	path := filepath.Join(dir, ReceiptFile)
	if err := atomic.WriteFile(path, append(raw, '\n')); err != nil {
		return wrapReceiptErr("write "+path, err)
	}
	return nil
}

// ReadReceipt loads and validates provenance.json from dir. Returns
// exists=false when missing, and exists=true with non-nil err when corrupt.
func ReadReceipt(dir string) (receipt *core.ProvenanceReceipt, exists bool, err error) {
	path := filepath.Join(dir, ReceiptFile)
	raw, exists, err := readCappedReceiptBytes(path)
	if !exists || err != nil {
		return nil, exists, err
	}
	out, err := decodeReceipt(path, raw)
	if err != nil {
		return nil, true, err
	}
	return out, true, nil
}

func readCappedReceiptBytes(path string) ([]byte, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, true, wrapReceiptErr("open "+path, err)
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, maxReceiptBytes+1))
	if err != nil {
		return nil, true, wrapReceiptErr("read "+path, err)
	}
	if len(raw) > maxReceiptBytes {
		return nil, true, wrapReceiptErr(fmt.Sprintf("%s exceeds %d bytes", path, maxReceiptBytes), nil)
	}
	return raw, true, nil
}

func decodeReceipt(path string, raw []byte) (*core.ProvenanceReceipt, error) {
	var out core.ProvenanceReceipt
	dec := json.NewDecoder(bytes.NewReader(raw))
	if receiptIsOurs(raw) {
		dec.DisallowUnknownFields()
	}
	if err := dec.Decode(&out); err != nil {
		return nil, wrapReceiptErr("parse "+path, err)
	}
	if out.SchemaVersion == "" || out.Inputs == nil {
		return nil, wrapReceiptErr(path+" missing schema_version or inputs", nil)
	}
	if out.Truncated == nil {
		out.Truncated = []core.ProvenanceTruncatedRow{}
	}
	return &out, nil
}

// receiptIsOurs reports whether the receipt declares this binary's schema
// version. Only that version decodes strictly: the published schema lets the
// receipt carry additive fields, and a reader must not turn a newer writer's
// receipt into a permanent warning.
func receiptIsOurs(raw []byte) bool {
	var header struct {
		SchemaVersion string `json:"schema_version"`
	}
	return json.Unmarshal(raw, &header) == nil && header.SchemaVersion == "1"
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
				Kind: kindPrompt, Name: f, Status: statusAbsent, Bytes: &n,
			})
			continue
		}
		out = append(out, core.ProvenanceInput{
			Kind: kindPrompt, Name: f, Status: statusIncluded, SHA256: shaHex(raw), Bytes: new(int64(len(raw))),
		})
	}
	return out
}

func panelInputEntry(source string, members []string) core.ProvenanceInput {
	status := statusIncluded
	if len(members) == 0 {
		status = statusAbsent
	}
	return core.ProvenanceInput{
		Kind:    kindPanel,
		Name:    kindPanel,
		Status:  status,
		Source:  source,
		Members: slices.Clone(members),
	}
}

func bundleInputEntries(dir string) []core.ProvenanceInput {
	if dir == "" {
		return nil
	}
	rels := listBundleRelPaths(dir)
	out := make([]core.ProvenanceInput, 0, len(rels))
	for _, rel := range rels {
		raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			continue
		}
		out = append(out, core.ProvenanceInput{
			Kind:   kindBundle,
			Name:   rel,
			Status: statusIncluded,
			SHA256: shaHex(raw),
			Bytes:  new(int64(len(raw))),
		})
	}
	return out
}

func listBundleRelPaths(dir string) []string {
	var rels []string
	walkErr := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d == nil {
			return err
		}
		return collectBundleEntry(dir, path, d, &rels)
	})
	if walkErr != nil {
		return nil
	}
	slices.Sort(rels)
	return rels
}

func collectBundleEntry(dir, path string, d fs.DirEntry, rels *[]string) error {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return wrapReceiptErr("relpath", err)
	}
	if rel == "." {
		return nil
	}
	relSlash := filepath.ToSlash(rel)
	if d.IsDir() {
		return skipBundleDirIfNeeded(relSlash, d.Name())
	}
	if relSlash != ReceiptFile && !strings.HasPrefix(d.Name(), ".") {
		*rels = append(*rels, relSlash)
	}
	return nil
}

func skipBundleDirIfNeeded(relSlash, name string) error {
	if relSlash == "controls" || strings.HasPrefix(name, ".") {
		return filepath.SkipDir
	}
	return nil
}

func kbInputsAndTruncated(dir string, kbIn *KBRecallInput) ([]core.ProvenanceInput, []core.ProvenanceTruncatedRow) {
	if kbIn == nil {
		return []core.ProvenanceInput{{
			Kind:     kindKB,
			Name:     "kb.txt",
			Status:   statusAbsent,
			Included: new(0),
			Omitted:  new(0),
		}}, nil
	}
	summary := kbSummaryEntry(dir, kbIn)
	entries := make([]core.ProvenanceInput, 0, 1+len(kbIn.Candidates))
	entries = append(entries, summary)
	var truncRows []core.ProvenanceTruncatedRow
	if kbIn.Truncated && kbIn.Cap != nil {
		truncRows = append(truncRows, kbCapTruncRow(kbIn))
	}
	totalCands := len(kbIn.Candidates)
	for _, c := range kbIn.Candidates {
		entries = append(entries, core.ProvenanceInput{
			Kind:   "kb_candidate",
			Name:   c.ID,
			ID:     c.ID,
			Status: c.Status,
			Reason: c.Reason,
		})
		if c.Status == statusOmitted && c.Reason == "top_n" {
			truncRows = append(truncRows, kbTopNTruncRow(c.ID, kbIn, totalCands))
		}
	}
	return entries, truncRows
}

func kbSummaryEntry(dir string, kbIn *KBRecallInput) core.ProvenanceInput {
	status := statusAbsent
	if kbIn.Included > 0 {
		status = statusIncluded
	}
	if kbIn.Truncated {
		status = statusTruncated
	}
	summary := core.ProvenanceInput{
		Kind:     kindKB,
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
	return summary
}

func kbCapTruncRow(kbIn *KBRecallInput) core.ProvenanceTruncatedRow {
	cut := core.ResultCut("response_cap", kbIn.CharsIncluded, new(kbIn.CharsTotal), nil)
	row := core.NewProvenanceTruncatedRow(kindKB, "", statusTruncated, "cap", cut)
	row.CharsIncluded = new(kbIn.CharsIncluded)
	row.CharsTotal = new(kbIn.CharsTotal)
	row.Cap = new(*kbIn.Cap)
	return row
}

func kbTopNTruncRow(id string, kbIn *KBRecallInput, totalCands int) core.ProvenanceTruncatedRow {
	cut := core.ResultCut("response_cap", kbIn.Included, new(totalCands), nil)
	row := core.NewProvenanceTruncatedRow("kb_candidate", id, statusOmitted, "top_n", cut)
	row.Included = new(kbIn.Included)
	row.Omitted = new(kbIn.Omitted)
	return row
}

func learningsInputEntry(in *ControlInput) core.ProvenanceInput {
	entry := core.ProvenanceInput{Kind: "learnings", Name: ".verdict/learnings.md", Status: statusAbsent}
	if in == nil || in.Status != statusUsed {
		return entry
	}
	entry.Status = statusIncluded
	entry.Source = in.Source
	entry.SHA256 = in.SHA256
	entry.Bytes = new(in.Bytes)
	return entry
}

func controlEntries(kind string, items []ControlInput) []core.ProvenanceInput {
	out := make([]core.ProvenanceInput, 0, len(items))
	for _, it := range sortedControls(items) {
		status := normalizeControlStatus(kind, it.Status)
		entry := core.ProvenanceInput{
			Kind:   kind,
			Name:   it.Path,
			Status: status,
			Source: it.Source,
			SHA256: it.SHA256,
		}
		if it.Bytes > 0 || isLiveControlStatus(status) {
			entry.Bytes = new(it.Bytes)
		}
		out = append(out, entry)
	}
	return out
}

func normalizeControlStatus(kind, status string) string {
	if kind == "override" && isLiveControlStatus(status) {
		return statusIncluded
	}
	return status
}

func isLiveControlStatus(status string) bool {
	return status == statusIncluded || status == statusUsed || status == "base" || status == "head"
}

func configInputEntries(in *ReceiptBuildInput) []core.ProvenanceInput {
	layerSummary := strings.Join(in.ConfigLayers, sepComma)
	if layerSummary == "" {
		layerSummary = layerBuiltIn
	}
	status := statusIncluded
	if in.ConfigSHA256 == "" {
		status = layerBuiltIn
	}
	parts := make([]string, 0, len(in.ConfigValues))
	for _, v := range sortedConfigValues(in.ConfigValues) {
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
			Kind:     kindExclusions,
			Name:     "excluded.json",
			Status:   statusAbsent,
			Excluded: new(0),
		}}
	}
	entry := core.ProvenanceInput{
		Kind:          kindExclusions,
		Name:          "excluded.json",
		Status:        statusIncluded,
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

func computeRunID(in *ReceiptBuildInput, promptsHash string, learn *core.ProvenanceInput) string {
	h := sha256.New()
	writeKV(h, "prompts_hash", promptsHash)
	writeKV(h, "head_sha", in.HeadSHA)
	writeKV(h, "base_sha", in.BaseSHA)
	if raw, err := os.ReadFile(filepath.Join(in.Dir, "full.diff")); err == nil {
		writeKV(h, "full_diff_sha256", shaHex(raw))
	}
	writeKV(h, "panel_source", in.PanelSource)
	writeKV(h, "panel", strings.Join(in.Panel, sepComma))
	writeKV(h, "config_sha256", in.ConfigSHA256)
	writeConfigOriginsToHash(h, in.ConfigValues)
	writeControlsToHash(h, "ctrl:", in.Controls)
	writeControlsToHash(h, "over:", in.Overrides)
	writeKV(h, "learnings", learn.Status+sepColon+learn.SHA256)
	writeExclAndKBToHash(h, in)
	return hex.EncodeToString(h.Sum(nil))
}

// sortedConfigValues returns the winning config origins in key order.
func sortedConfigValues(vals []ConfigOriginInput) []ConfigOriginInput {
	sorted := slices.Clone(vals)
	slices.SortFunc(sorted, func(a, b ConfigOriginInput) int { return strings.Compare(a.Key, b.Key) })
	return sorted
}

// sortedControls returns snapshot entries in path order.
func sortedControls(items []ControlInput) []ControlInput {
	sorted := slices.Clone(items)
	slices.SortFunc(sorted, func(a, b ControlInput) int { return strings.Compare(a.Path, b.Path) })
	return sorted
}

func writeConfigOriginsToHash(w io.Writer, vals []ConfigOriginInput) {
	for _, v := range sortedConfigValues(vals) {
		writeKV(w, "cfg:"+v.Key, v.Layer+sepColon+v.Source)
	}
}

func writeControlsToHash(w io.Writer, prefix string, items []ControlInput) {
	for _, c := range sortedControls(items) {
		writeKV(w, prefix+c.Path, c.Status+sepColon+c.SHA256)
	}
}

func writeExclAndKBToHash(w io.Writer, in *ReceiptBuildInput) {
	if in.Exclusions.Present {
		writeKV(w, "excl", fmt.Sprintf("%s:%s:%d:%s",
			in.Exclusions.RawDiffSHA256, in.Exclusions.DiffSHA256, in.Exclusions.Excluded, in.Exclusions.PatternsSource))
	}
	if in.KB == nil {
		return
	}
	writeKV(w, "kb_summary", fmt.Sprintf("%d:%d:%v:%d:%d",
		in.KB.Included, in.KB.Omitted, in.KB.Truncated, in.KB.CharsIncluded, in.KB.CharsTotal))
	if in.KB.Cap != nil {
		writeKV(w, "kb_cap", strconv.Itoa(*in.KB.Cap))
	}
	for _, c := range in.KB.Candidates {
		writeKV(w, "kb_cand:"+c.ID, c.Status+sepColon+c.Reason)
	}
}

func writeKV(w io.Writer, k, v string) {
	_, _ = fmt.Fprintf(w, "%s=%s\n", k, v)
}

func shaHex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

type footerInputStats struct {
	panelStr    string
	learnStatus string
	cfgLayer    string
	promptsCnt  int
	kbInc       int
	kbOm        int
	exclCount   int
	overCount   int
	ctrlCount   int
	kbTrunc     bool
}

func collectFooterStats(inputs []core.ProvenanceInput) footerInputStats {
	s := footerInputStats{panelStr: "none", learnStatus: statusAbsent, cfgLayer: layerBuiltIn}
	for i := range inputs {
		in := &inputs[i]
		accumulateFooterInput(&s, in)
	}
	return s
}

func accumulateFooterInput(s *footerInputStats, in *core.ProvenanceInput) {
	switch in.Kind {
	case kindPrompt:
		s.promptsCnt += countTrue(in.Status == statusIncluded)
	case kindPanel:
		if len(in.Members) > 0 {
			s.panelStr = strings.Join(in.Members, sepComma)
		}
	case kindKB:
		s.kbInc, s.kbOm = ptrInt(in.Included), ptrInt(in.Omitted)
		s.kbTrunc = in.Status == statusTruncated
	case kindExclusions:
		s.exclCount = ptrInt(in.Excluded)
	default:
		accumulateFooterPolicyInput(s, in)
	}
}

func accumulateFooterPolicyInput(s *footerInputStats, in *core.ProvenanceInput) {
	switch {
	case in.Kind == "learnings" && in.Status != "":
		s.learnStatus = in.Status
	case in.Kind == "override":
		s.overCount += countTrue(in.Status == statusIncluded)
	case in.Kind == "control":
		s.ctrlCount += countTrue(in.Status == statusUsed)
	case in.Kind == "config" && in.Layer != "":
		s.cfgLayer = in.Layer
	}
}

func countTrue(b bool) int {
	if b {
		return 1
	}
	return 0
}

func ptrInt(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}

// footerFieldLen caps one rendered `> Inputs:` field so a long config value
// cannot push the line past a label.
const footerFieldLen = 64

// cutString keeps at most maxLen leading bytes of s.
func cutString(s string, maxLen int) string {
	if len(s) > maxLen {
		return s[:maxLen]
	}
	return s
}

// displayField renders one footer value on a single line: a line-breaking
// rune (CR/LF, U+2028, U+2029) becomes a space and the result is capped, so no
// receipt or config value can open a second line or a second comment marker.
func displayField(v string) string {
	clean := strings.Map(func(r rune) rune {
		if core.IsLineBreak(r) {
			return ' '
		}
		return r
	}, v)
	if utf8.RuneCountInString(clean) > footerFieldLen {
		clean = string([]rune(clean)[:footerFieldLen])
	}
	return clean
}

// footerField is one `label value` field of the line, rendered single-line.
func footerField(label, value string) string {
	return label + displayField(value)
}

// RenderInputsFooterLine formats the compact single-line `> Inputs:` summary
// appended inside the footer marker when provenance.json exists. Missing or
// malformed provenance.json returns "" so legacy bundles remain identical.
func RenderInputsFooterLine(dir string) string {
	if dir == "" {
		return ""
	}
	r, ok, err := ReadReceipt(dir)
	if !ok || err != nil || r == nil {
		return ""
	}
	return inputsFooterLine(r)
}

func inputsFooterLine(r *core.ProvenanceReceipt) string {
	s := collectFooterStats(r.Inputs)
	fields := []string{
		"> Inputs: " + footerField("run ", cutString(r.RunID, runShortLen)),
		footerField("prompts ", strconv.Itoa(s.promptsCnt)) + " (" + displayField(cutString(r.PromptsHash, hashShortLen)) + ")",
		footerField("panel ", s.panelStr),
		displayField(formatFooterKBPart(s.kbInc, s.kbOm, s.kbTrunc)),
		footerField("learnings ", s.learnStatus),
		footerField("overrides ", strconv.Itoa(s.overCount)),
		footerField("controls ", strconv.Itoa(s.ctrlCount)),
		footerField("config ", s.cfgLayer),
		footerField("excluded ", strconv.Itoa(s.exclCount)),
	}
	return strings.Join(fields, " · ")
}

func formatFooterKBPart(kbInc, kbOm int, kbTrunc bool) string {
	if kbOm == 0 && !kbTrunc {
		return fmt.Sprintf("kb %d", kbInc)
	}
	part := fmt.Sprintf("kb %d/%d", kbInc, kbInc+kbOm)
	if kbTrunc {
		return part + " (truncated)"
	}
	return part + fmt.Sprintf(" (%d omitted)", kbOm)
}

// MergeTruncated appends bundle truncation rows absent from r.Truncated.
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
	if hasBundleTruncation(base, extra.TruncationReason) {
		return base
	}
	row := core.NewProvenanceTruncatedRow(kindBundle, "", statusTruncated, extra.TruncationReason, extra)
	return append(base, row)
}

func hasBundleTruncation(rows []core.ProvenanceTruncatedRow, reason string) bool {
	for i := range rows {
		if rows[i].Input == kindBundle && rows[i].Reason == reason {
			return true
		}
	}
	return false
}

// FormatTable renders the full local provenance & truncation tables for
// `report --provenance` and `post --provenance`.
func FormatTable(r *core.ProvenanceReceipt, truncated []core.ProvenanceTruncatedRow) []string {
	if r == nil {
		r = EmptyReceipt()
	}
	lines := []string{
		fmt.Sprintf("provenance: run_id=%s prompts_hash=%s", r.RunID, r.PromptsHash),
		"KIND\tNAME\tSTATUS\tBYTES\tSHA256\tDETAIL",
	}
	for i := range r.Inputs {
		lines = append(lines, formatInputRow(&r.Inputs[i]))
	}
	if len(truncated) == 0 {
		return append(lines, "truncated: none")
	}
	lines = append(lines, fmt.Sprintf("truncated: %d", len(truncated)), "INPUT\tID\tSTATUS\tREASON\tDETAIL")
	for i := range truncated {
		lines = append(lines, formatTruncatedRow(&truncated[i]))
	}
	return lines
}

func formatInputRow(in *core.ProvenanceInput) string {
	bytesCol := dash
	if in.Bytes != nil {
		bytesCol = strconv.FormatInt(*in.Bytes, decimalBase)
	}
	shaCol := dash
	if in.SHA256 != "" {
		shaCol = cutString(in.SHA256, runShortLen)
	}
	details := inputRowDetails(in)
	detailCol := strings.Join(details, " ")
	if detailCol == "" {
		detailCol = dash
	}
	return fmt.Sprintf("%s\t%s\t%s\t%s\t%s\t%s", in.Kind, in.Name, in.Status, bytesCol, shaCol, detailCol)
}

func inputRowDetails(in *core.ProvenanceInput) []string {
	var details []string
	if in.Reason != "" {
		details = append(details, "reason="+in.Reason)
	}
	details = appendIntDetail(details, "included", in.Included)
	details = appendIntDetail(details, statusOmitted, in.Omitted)
	details = appendIntDetail(details, "chars_included", in.CharsIncluded)
	details = appendIntDetail(details, "chars_total", in.CharsTotal)
	details = appendIntDetail(details, "cap", in.Cap)
	details = appendIntDetail(details, "excluded", in.Excluded)
	if in.Layer != "" {
		details = append(details, "layer="+in.Layer)
	}
	if len(in.Members) > 0 {
		details = append(details, "members="+strings.Join(in.Members, sepComma))
	}
	if in.Detail != "" {
		details = append(details, in.Detail)
	}
	return details
}

func appendIntDetail(details []string, key string, val *int) []string {
	if val == nil {
		return details
	}
	return append(details, key+"="+strconv.Itoa(*val))
}

func formatTruncatedRow(row *core.ProvenanceTruncatedRow) string {
	idCol := row.ID
	if idCol == "" {
		idCol = dash
	}
	var details []string
	details = appendIntDetail(details, "included", row.Included)
	details = appendIntDetail(details, statusOmitted, row.Omitted)
	details = appendIntDetail(details, "chars_included", row.CharsIncluded)
	details = appendIntDetail(details, "chars_total", row.CharsTotal)
	details = appendIntDetail(details, "cap", row.Cap)
	details = append(details, fmt.Sprintf("truncation_reason=%s returned=%d", row.TruncationReason, row.Returned))
	details = appendIntDetail(details, "total", row.Total)
	return fmt.Sprintf("%s\t%s\t%s\t%s\t%s", row.Input, idCol, row.Status, row.Reason, strings.Join(details, " "))
}
