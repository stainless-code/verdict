package record

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/overrides"
	"github.com/stainless-code/verdict/internal/provenance"
)

// validateOverrides ports validate_overrides: locked-block scans plus the
// parsed-shape gates for the four P5 fragments. Absent fragments pass
// silently; every violation fails closed.
func validateOverrides() error {
	fragments := []string{
		overrides.Preamble, overrides.FooterOrderPath, overrides.Threshold, overrides.Attribution,
	}
	for _, f := range fragments {
		if ref := validateFragment(f); ref != nil {
			return ref
		}
	}
	if ref := validateShapes(); ref != nil {
		return ref
	}
	return nil
}

// validateShapes validates the parsed-shape gates for the P5 fragments.
func validateShapes() error {
	if _, err := overrides.FooterOrder(); err != nil {
		return passRefusal(err)
	}
	if _, _, err := overrides.Thresholds(); err != nil {
		return passRefusal(err)
	}
	// The attribution fragment is validated for shape here; rendering
	// happens post-side, where the record verb discards the body.
	if exists(overrides.Attribution) {
		if _, err := overrides.AttributionText(); err != nil {
			return passRefusal(err)
		}
	}
	return nil
}

// validateFragment validates one override fragment file, if present. A
// missing path — or a non-regular file such as a directory in the
// fragment's place — reads as no fragment (matching the shell's [[ -f ]]
// skip, I12).
func validateFragment(f string) error {
	if fi, err := os.Stat(f); err == nil && fi.Mode().IsRegular() {
		return validateFragmentFile(f)
	}
	return nil
}

// validateFragmentFile validates one present regular override fragment.
func validateFragmentFile(f string) error {
	raw, err := os.ReadFile(f)
	if err != nil {
		return &cli.RefusalError{Code: "bad-override", Message: fmt.Sprintf("override %s is unreadable (fix permissions or remove it)", f)}
	}
	text := strings.ReplaceAll(string(raw), "\r", "")
	if err := overrides.CheckLocked(f, text); err != nil {
		return passRefusal(err)
	}
	if f == overrides.Preamble || f == overrides.Attribution {
		if err := cli.ValidateAttributionPlaceholders(text, "override "+f); err != nil {
			return passRefusal(err)
		}
	}
	return nil
}

// passRefusal returns a sibling package's typed refusal unchanged: refusal
// context lives in Code/Message (rendered at the edge via [errors.As]), so the
// wrapcheck add-context rule is waived here by design.
func passRefusal(err error) error { return err }

var (
	receiptLineRe  = regexp.MustCompile(`(?m)^\s*> verdict `)
	receiptLineRe2 = regexp.MustCompile(`(?m)^\s*> .*Reviewed with \[`)
)

// stripFooterGarnish ports the footer receipt checks: a carried rendered
// footer warns and is replaced; a hand-written receipt warns and is kept.
func stripFooterGarnish(r *cli.Runner, body string) string {
	if strings.Contains(body, cli.FooterDivider) {
		r.Warn("body already carries a rendered footer — replacing it with the canonical block")
		body = provenance.StripFooter(body)
	}
	if receiptLineRe.MatchString(body) || receiptLineRe2.MatchString(body) {
		r.Warn("body already carries a footer receipt — keeping the agent block, script footer skipped")
	}
	return body
}

// writeFindings persists the merged store, mirroring `jq '.' > findings.json`
// with the shell's insertion order.
func writeFindings(st *state) error {
	st.findingsFile = st.dir + "/findings.json"
	if err := atomic.WriteFile(st.findingsFile, []byte(jq.PrettyOrdered(st.entries))); err != nil {
		return &cli.RefusalError{Code: codeRecordFailed, Message: fmt.Sprintf("could not write %s: %v", st.findingsFile, err)}
	}
	return nil
}

// finalizeRecord ports the record-block tail: the record.json artifact, the
// human summary, and the machine envelope.
func finalizeRecord(st *state) (int, error) {
	r := st.runner
	st.nOpen = countStatus(st.entries, "open")
	st.nDeferred = countStatus(st.entries, "deferred")
	st.nAddressed = countStatus(st.entries, "addressed")
	diffSHA := sha256File(st.dir + "/full.diff")
	toolVersion, promptsHash, model := provenance.Triple(st.root, st.dir)
	recordDoc := recordDoc(st, diffSHA, toolVersion, promptsHash, model)
	if err := atomic.WriteFile(st.dir+"/record.json", []byte(jq.PrettyOrdered(recordDoc))); err != nil {
		return 0, &cli.RefusalError{Code: codeRecordFailed, Message: fmt.Sprintf("could not write %s/record.json: %v", st.dir, err)}
	}
	fmt.Fprintf(r.Human, msgRecorded,
		st.dir, st.nOpen, st.dir)
	if !r.JSONMode {
		return 0, nil
	}
	return printEnvelope(r, envelopeDoc(st, envelopeItems(st.entries)))
}

// recordDoc builds the record.json artifact document.
func recordDoc(st *state, diffSHA, toolVersion, promptsHash, model string) *jq.Obj {
	var modelVal any
	if model != "" {
		modelVal = model
	}
	return jq.NewObj(
		"schema_version", "1",
		"status", st.prov,
		"head", st.headFull,
		"diff_sha256", diffSHA,
		"recorded_at", st.now,
		"findings", len(st.entries),
		"open", st.nOpen,
		"confidence", st.confidence,
		"security_summary", st.security,
		"tool_version", toolVersion,
		"prompts_hash", promptsHash,
		"model", modelVal,
	)
}

// envelopeItems builds the machine envelope's finding summaries.
func envelopeItems(entries []any) []any {
	items := make([]any, 0, len(entries))
	for _, e := range entries {
		m, ok := e.(*jq.Obj)
		if !ok || m == nil {
			// Hand-corrupt stores can carry non-object entries; the shell
			// aborts on them, the Go port skips (documented divergence).
			continue
		}
		items = append(items, envelopeItem(m))
	}
	return items
}

// envelopeItem builds one finding summary with its locator.
func envelopeItem(m *jq.Obj) *jq.Obj {
	pv, _ := m.Get("path")
	lv, _ := m.Get("line")
	hv, _ := m.Get("heading")
	sv, _ := m.Get("sig")
	tv, _ := m.Get("status")
	av, _ := m.Get("anchored")
	return jq.NewObj(
		"sig", sv,
		"status", tv,
		"anchored", av,
		"locator", itemLocator(pv, lv, hv),
	)
}

// itemLocator renders an entry's path:line or heading fallback.
func itemLocator(pv, lv, hv any) string {
	if jq.Falsey(pv) {
		return jq.Interp(jq.Alt(hv, "?"))
	}
	locator := jq.Interp(pv)
	if !jq.Falsey(lv) {
		locator += ":" + jq.Interp(lv)
	}
	return locator
}

// envelopeDoc builds the machine envelope for --json mode.
func envelopeDoc(st *state, items []any) *jq.Obj {
	r := st.runner
	return jq.NewObj(
		"schema_version", "1",
		"verb", "record",
		"repo", r.Repo,
		"pr", cli.PrJSON(r.Pr),
		"at", st.now,
		"next", "report or triage the local bundle; post stays refused (no PR target)",
		"warnings", stringSlice(r.Warnings),
		"data", jq.NewObj(
			"dir", st.dir,
			"anchors", jq.NewObj(
				"kept", st.nKept,
				"moved", st.nMoved,
			),
			"findings", jq.NewObj(
				"open", st.nOpen,
				"deferred", st.nDeferred,
				"addressed", st.nAddressed,
				"file", st.findingsFile,
			),
			"confidence", st.confidence,
			"security_summary", st.security,
			"items", items,
		),
		"error", nil,
	)
}

// finalizeRecord tail: print the envelope in --json mode.
func printEnvelope(r *cli.Runner, env *jq.Obj) (int, error) {
	fmt.Fprint(r.Out, jq.PrettyOrdered(env))
	return 0, nil
}

// stringSlice converts warnings to JSON string values.
func stringSlice(in []string) []any {
	out := make([]any, 0, len(in))
	for _, s := range in {
		out = append(out, s)
	}
	return out
}

// hunkLines counts hunks.tsv lines the way `wc -l` does.
func hunkLines(path string) int {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	return strings.Count(string(raw), "\n")
}
