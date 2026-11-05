package recordcore

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/overrides"
	"github.com/stainless-code/verdict/internal/provenance"
	"github.com/stainless-code/verdict/internal/readcommon"
)

var (
	receiptLineRe  = regexp.MustCompile(`(?m)^\s*> verdict `)
	receiptLineRe2 = regexp.MustCompile(`(?m)^\s*> .*Reviewed with \[`)
)

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

func (st *state) setMarker(body string) {
	st.marker = "plain"
	if markerRe.MatchString(body) {
		st.marker = "verdict"
	}
}

func (st *state) countHunks() {
	if st.hunkRows != nil {
		st.hunkN = len(st.hunkRows)
		return
	}
	st.hunkN = hunkLines(st.anchorHunks)
}

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
			continue
		}
		items = append(items, envelopeItem(m))
	}
	return items
}

func envelopeItem(m *jq.Obj) *jq.Obj {
	sv, _ := m.Get("sig")
	tv, _ := m.Get("status")
	av, _ := m.Get("anchored")
	return jq.NewObj(
		"sig", sv,
		"status", tv,
		"anchored", av,
		"locator", readcommon.Locator(m),
	)
}

func hunkLines(path string) int {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	return strings.Count(string(raw), "\n")
}
