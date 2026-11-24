package recordcore

import (
	"fmt"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/noblock"
	"github.com/stainless-code/verdict/internal/overrides"
	"github.com/stainless-code/verdict/internal/provenance"
	"github.com/stainless-code/verdict/internal/readcommon"
)

func maybeWarn(warn func(string), msg string) {
	if warn != nil {
		warn(msg)
	}
}

func hasFooterDividerLine(body string) bool {
	for ln := range strings.SplitSeq(body, "\n") {
		if ln == core.FooterDivider {
			return true
		}
	}
	return false
}

// ApplyFooter appends the canonical rendered footer block to body.
func ApplyFooter(body string, opts *provenance.FooterOpts, warn func(string)) string {
	block, err := provenance.RenderFooter(opts)
	if err != nil || block == "" {
		return body
	}
	if hasFooterDividerLine(body) {
		maybeWarn(warn, "body already carries a rendered footer — replacing it with the canonical block")
		body = provenance.StripFooter(body)
	}
	return body + "\n\n" + block
}

func (st *state) applyFooter(body string) string {
	return ApplyFooter(body, st.footerOpts(), st.warn)
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

// BodyMarker reports "verdict" when body carries a rendered verdict lead, or
// "plain" otherwise.
func BodyMarker(body string) string {
	if markerRe.MatchString(body) {
		return "verdict"
	}
	return "plain"
}

func (st *state) setMarker(body string) {
	st.marker = BodyMarker(body)
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
	raw, err := noblock.ReadFile(path)
	if err != nil {
		return 0
	}
	return strings.Count(string(raw), "\n")
}
