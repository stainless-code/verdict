package recordcore

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/overrides"
	"github.com/stainless-code/verdict/internal/provenance"
)

// validateOverridesAt ports validate_overrides: locked-block scans plus the
// parsed-shape gates for the four P5 fragments, read from the bundle's
// controls snapshot. Absent fragments pass silently; every violation fails
// closed.
func validateOverridesAt(dir string) error {
	set := overrides.At(dir)
	fragments := []string{
		overrides.Preamble, overrides.FooterOrderPath, overrides.Threshold, overrides.Attribution,
	}
	for _, f := range fragments {
		if ref := validateFragment(filepath.Join(dir, filepath.Base(f)), f); ref != nil {
			return ref
		}
	}
	return validateShapes(set)
}

// validateShapes validates the parsed-shape gates for the P5 fragments.
func validateShapes(set overrides.Set) error {
	if _, err := set.FooterOrder(); err != nil {
		return passRefusal(err)
	}
	if _, _, err := set.Thresholds(); err != nil {
		return passRefusal(err)
	}
	// The attribution fragment is validated for shape here; rendering
	// happens post-side, where the record verb discards the body.
	if _, err := set.AttributionText(); err != nil {
		return passRefusal(err)
	}
	return nil
}

// validateFragment validates one override fragment file, if present. A
// missing path — or a non-regular file such as a directory in the
// fragment's place — reads as no fragment (matching the shell's [[ -f ]]
// skip, I12).
func validateFragment(path, f string) error {
	if fi, err := os.Stat(path); err == nil && fi.Mode().IsRegular() {
		return validateFragmentFile(path, f)
	}
	return nil
}

// validateFragmentFile validates one present regular override fragment.
func validateFragmentFile(path, f string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return &core.RefusalError{Code: "bad-override", Message: fmt.Sprintf("override %s is unreadable (fix permissions or remove it)", f)}
	}
	text := strings.ReplaceAll(string(raw), "\r", "")
	if err := overrides.CheckLocked(f, text); err != nil {
		return passRefusal(err)
	}
	if f == overrides.Preamble || f == overrides.Attribution {
		if err := core.ValidateAttributionPlaceholders(text, "override "+f); err != nil {
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

// applyFooter ports the footer receipt tail: a carried rendered footer warns
// and is replaced with the canonical block; a hand-written receipt warns and
// keeps the agent block (script footer skipped). It returns the curated body.
func (st *state) applyFooter(body string) string {
	block, err := provenance.RenderFooter(st.footerOpts())
	if err != nil || block == "" {
		return body
	}
	if strings.Contains(body, core.FooterDivider) {
		st.warn("body already carries a rendered footer — replacing it with the canonical block")
		body = provenance.StripFooter(body)
	}
	if receiptLineRe.MatchString(body) || receiptLineRe2.MatchString(body) {
		st.warn("body already carries a footer receipt — keeping the agent block, script footer skipped")
		return body
	}
	return body + "\n\n" + block
}

// footerOpts builds the footer inputs from the merge prefs and bundle. The
// review model is empty on the record/post path (review.json carries none;
// cost-record model or unrecorded decides, like the shell).
func (st *state) footerOpts() *provenance.FooterOpts {
	return FooterOpts(&st.effective, st.forces, st.root, st.dir, overrides.At(st.controls.OverridesDir()))
}

// FooterOpts builds the footer inputs shared by the merge and the post
// split hook (which must reserve the same block it later appends).
func FooterOpts(effective *core.Effective, forces *core.GlobalForces, root, dir string, set overrides.Set) *provenance.FooterOpts {
	return &provenance.FooterOpts{
		Dir:                    dir,
		PublishFooter:          effective.PublishFooter,
		PublishAttribution:     effective.PublishAttribution,
		PublishAttributionText: effective.PublishAttributionText,
		PublishCopyright:       effective.PublishCopyright,
		PublishLogo:            effective.PublishLogo,
		PublishLogoURL:         effective.PublishLogoURL,
		Root:                   root,
		ReviewModel:            "",
		AttributionTextSet:     forces != nil && forces.PublishAttributionTextSet,
		Overrides:              set,
	}
}

// setMarker ports the verdict-marker tail: a verdict lead marks the review,
// anything else reads plain. It runs on the curated body (footer included,
// like the shell — the receipt never flips the marker).
func (st *state) setMarker(body string) {
	st.marker = "plain"
	if markerRe.MatchString(body) {
		st.marker = "verdict"
	}
}

// countHunks ports `hunk_n=$(wc -l < anchor_hunks)`: newline count, read
// before the derived per-chunk snapshot is removed.
func (st *state) countHunks() {
	st.hunkN = hunkLines(st.anchorHunks)
}

// writeFindings persists the merged store, mirroring `jq '.' > findings.json`
// with the shell's insertion order. The caller sets st.findingsFile before
// the merge (the post verb writes findings.dryrun.json on dry runs).
func writeFindings(st *state) error {
	if err := atomic.WriteFile(st.findingsFile, []byte(jq.PrettyOrdered(st.entries))); err != nil {
		return &core.RefusalError{Code: codeRecordFailed, Message: fmt.Sprintf("could not write %s: %v", st.findingsFile, err)}
	}
	return nil
}

// EnvelopeItems builds the machine envelope's finding summaries, shared by
// the record service and the post dry-run envelope.
func EnvelopeItems(entries []any) []any {
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

// hunkLines counts hunks.tsv lines the way `wc -l` does.
func hunkLines(path string) int {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	return strings.Count(string(raw), "\n")
}
