package learncore

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/incident"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

const (
	// incidentNoteMaxBytes caps one note: it lands in a tracked file an agent
	// reads, so the evidence stays a sentence, not a document.
	incidentNoteMaxBytes = 2000
	// shortShaLen is the abbreviation the human line prints.
	shortShaLen = 7
)

const (
	msgIncidentNeedsMode = "learn: --sha, --path, --note, --url, and --at belong to --incident <bundle>"
	msgIncidentBadName   = "refusing learn: --incident '%s' is not a bundle name"
	msgIncidentMissing   = "learn: no bundle directory %s (fetch or record it first)"
	msgIncidentFindings  = "learn: bundle %s carries no findings.json (post or record the review first)"
	msgIncidentSha       = "refusing learn: --sha '%s' is not 7-40 lowercase hex digits"
	msgIncidentPaths     = "refusing learn: --incident needs at least one --path"
	msgIncidentNote      = "refusing learn: --note is over %d bytes"
	msgIncidentURL       = "refusing learn: --url must be an http(s) URL"
	msgIncidentAt        = "refusing learn: --at '%s' is not RFC 3339"
	msgWriteIncident     = "learn could not write %s: %v"
	msgCorruptIncident   = "incident ledger %s is unreadable (restore from backup or fix the bad line)"
	msgBusyIncident      = "incident ledger %s is being written by another attach (retry in a moment)"
)

// nextIncident points the operator at the measurement the attach feeds.
const nextIncident = "run 'trend --incidents' to measure the miss rate over attached incidents"

// incidentInput is the validated attach: one merge of one bundle.
type incidentInput struct { //nolint:govet // every field holds a pointer; the grouping documents the attach
	paths  []string
	note   *string
	url    *string
	bundle string
	sha    string
	at     string
}

// runIncident attaches one incident to a recorded bundle's ledger.
func (st *state) runIncident() *core.RefusalError {
	in, ref := st.validateIncident()
	if ref != nil {
		return ref
	}
	attached, ref := st.attachIncident(in)
	if ref != nil {
		return ref
	}
	st.next = nextIncident
	st.data = &Data{Incident: incidentDoc(in, attached)}
	st.humanf("attached incident %s %s -> %s (%s)\n",
		in.bundle, in.sha[:shortShaLen], st.incidentDisplay(), pathCount(len(in.paths)))
	return nil
}

// attachIncident writes the row and reports whether the pair was new. A dry
// run reads the ledger to answer the same question and writes nothing.
func (st *state) attachIncident(in *incidentInput) (string, *core.RefusalError) {
	row := &incident.Row{
		SchemaVersion: incident.Version,
		Bundle:        in.bundle,
		Sha:           in.sha,
		Paths:         in.paths,
		Note:          in.note,
		URL:           in.url,
		DetectedAt:    in.at,
	}
	if st.req.DryRun {
		return st.previewAttach(row)
	}
	appended, err := st.writeIncident(row)
	if err != nil {
		return "", err
	}
	return attachedText(appended), nil
}

// writeIncident upserts the row, mapping a store failure to its refusal. The
// bundle check proved .verdict exists, so the ledger's directory is not
// something this path creates.
func (st *state) writeIncident(row *incident.Row) (bool, *core.RefusalError) {
	appended, err := incident.Upsert(st.root, row)
	if err != nil {
		return false, st.incidentRefusal(err)
	}
	return appended, nil
}

// incidentRefusal maps a ledger write failure to its refusal: a held lock is
// retryable, a corrupt ledger is not, and every other error is a failed write.
func (st *state) incidentRefusal(err error) *core.RefusalError {
	switch {
	case errors.Is(err, incident.ErrBusy):
		return st.refuse(codeStoreBusy, fmt.Sprintf(msgBusyIncident, st.incidentDisplay()))
	case errors.Is(err, incident.ErrCorrupt):
		return st.corruptIncident()
	default:
		return st.refuse(codeLearnFailed, fmt.Sprintf(msgWriteIncident, st.incidentDisplay(), err))
	}
}

// previewAttach answers what a write would do without touching the ledger.
func (st *state) previewAttach(row *incident.Row) (string, *core.RefusalError) {
	rows, err := incident.Read(st.root)
	if errors.Is(err, incident.ErrMissing) {
		return attachedText(true), nil
	}
	if err != nil {
		return "", st.corruptIncident()
	}
	for _, existing := range rows {
		if existing.Bundle == row.Bundle && existing.Sha == row.Sha {
			return attachedText(false), nil
		}
	}
	return attachedText(true), nil
}

// corruptIncident reports an unreadable ledger: a silent drop would lose
// evidence the operator attached on purpose.
func (st *state) corruptIncident() *core.RefusalError {
	return st.refuse(codeCorruptStore, fmt.Sprintf(msgCorruptIncident, st.incidentDisplay()))
}

// incidentDisplay names the ledger in operator-facing output.
func (st *state) incidentDisplay() string {
	return filepath.ToSlash(filepath.Join(st.root, incident.File))
}

// validateIncident resolves the bundle and checks every attach value.
func (st *state) validateIncident() (*incidentInput, *core.RefusalError) {
	if _, ref := st.incidentBundleDir(); ref != nil {
		return nil, ref
	}
	sha, ref := st.incidentSha()
	if ref != nil {
		return nil, ref
	}
	paths, ref := st.incidentPaths()
	if ref != nil {
		return nil, ref
	}
	in := &incidentInput{bundle: st.req.Incident, sha: sha, paths: paths, at: st.now}
	in.note, ref = st.incidentNote()
	if ref != nil {
		return nil, ref
	}
	in.url, ref = st.incidentURL()
	if ref != nil {
		return nil, ref
	}
	if ref := st.applyIncidentAt(in); ref != nil {
		return nil, ref
	}
	return in, nil
}

// incidentBundleDir resolves the named bundle's directory.
func (st *state) incidentBundleDir() (string, *core.RefusalError) {
	dir, ref := readcommon.ResolveBundleDir(st.root, st.req.Incident)
	if ref != nil {
		return "", st.refuse(codeBadFlags, fmt.Sprintf(msgIncidentBadName, st.req.Incident))
	}
	if !readcommon.IsDir(dir) {
		return "", st.refuse(codeMissingBundle, fmt.Sprintf(msgIncidentMissing, dir))
	}
	if !readcommon.IsFile(dir + "/findings.json") {
		return "", st.refuse(codeMissingFindings, fmt.Sprintf(msgIncidentFindings, dir))
	}
	return dir, nil
}

// incidentSha reads the merge the incident is attached to.
func (st *state) incidentSha() (string, *core.RefusalError) {
	sha := st.req.Sha
	if !incident.ValidSha(sha) {
		return "", st.refuse(codeBadFlags, fmt.Sprintf(msgIncidentSha, sha))
	}
	return sha, nil
}

// incidentPaths validates, dedupes, and sorts the touched paths, so the same
// attach always writes the same row.
func (st *state) incidentPaths() ([]string, *core.RefusalError) {
	if len(st.req.Paths) == 0 {
		return nil, st.refuse(codeBadFlags, msgIncidentPaths)
	}
	seen := map[string]bool{}
	paths := make([]string, 0, len(st.req.Paths))
	for _, p := range st.req.Paths {
		if !validLearningPath(p) {
			return nil, &core.RefusalError{Code: codeBadScope, Message: msgLearningScope}
		}
		if !seen[p] {
			seen[p] = true
			paths = append(paths, p)
		}
	}
	slices.Sort(paths)
	return paths, nil
}

// incidentNote strips controls and caps the note at the evidence budget.
func (st *state) incidentNote() (*string, *core.RefusalError) {
	note := strings.TrimSpace(stripControls(st.req.Note))
	if note == "" {
		return nil, nil
	}
	if len(note) > incidentNoteMaxBytes {
		return nil, st.refuse(codeBadFlags, fmt.Sprintf(msgIncidentNote, incidentNoteMaxBytes))
	}
	return &note, nil
}

// incidentURL accepts only an http(s) URL, and returns the parsed rendering.
func (st *state) incidentURL() (*string, *core.RefusalError) {
	raw := strings.TrimSpace(st.req.URL)
	if raw == "" {
		return nil, nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, st.refuse(codeBadFlags, msgIncidentURL)
	}
	return &raw, nil
}

// applyIncidentAt overrides the runtime stamp with an explicit RFC 3339 time.
func (st *state) applyIncidentAt(in *incidentInput) *core.RefusalError {
	raw := strings.TrimSpace(st.req.At)
	if raw == "" {
		return nil
	}
	at, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return st.refuse(codeBadAt, fmt.Sprintf(msgIncidentAt, raw))
	}
	in.at = at.UTC().Format(time.RFC3339)
	return nil
}

// incidentDoc renders the envelope's data.incident in published key order.
func incidentDoc(in *incidentInput, attached string) *jq.Obj {
	paths := make([]any, 0, len(in.paths))
	for _, p := range in.paths {
		paths = append(paths, p)
	}
	return jq.NewObj(
		"bundle", in.bundle,
		"sha", in.sha,
		"paths", paths,
		"note", optional(in.note),
		"url", optional(in.url),
		"detected_at", in.at,
		"attached", attached,
	)
}

// optional renders a nil pointer as JSON null.
func optional(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

// stripControls removes control characters: the note lands on one JSONL line
// and in one human line, so neither may carry a newline or an escape.
func stripControls(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

// attachedText names what the attach did to the ledger.
func attachedText(appended bool) string {
	if appended {
		return "appended"
	}
	return "existing"
}

// pathCount renders the human path tally.
func pathCount(n int) string {
	if n == 1 {
		return "1 path"
	}
	return fmt.Sprintf("%d paths", n)
}
