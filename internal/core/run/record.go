package run

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

const (
	// launcherFile is the launcher's provenance record, written beside the
	// review next to patch.json, post.json, and verification-<group>.json. It is
	// the launch record ("this is what started, at this moment") and, after the
	// process exits, the outcome record.
	launcherFile = "launcher.json"
	// runOutputFile carries the child's full combined output, the way verify's
	// .verify.out carries its command's: raw, for operator debugging, while the
	// redacted tail lives in launcher.json.
	runOutputFile = ".run.out"
	// runOutputFileMode is the log's permission: a raw child stream can hold
	// anything the child printed, so it is the operator's to read and nobody
	// else's, unlike the 0644 store files beside it.
	runOutputFileMode = 0o600
	// launcherSchema is the record's own schema_version, independent of the
	// review file's.
	launcherSchema = "1"
	// tailCap bounds the redacted output tail the record carries (verify's
	// recorded tail uses the same size).
	tailCap = 4000
)

// launcher is the provenance record. The launch write fills everything down to
// Timeout; the outcome write adds the fields from DurationMS on, so a record
// left behind by a launcher that never came back still says what started and
// never claims an exit it did not observe. Pointer fields keep "not observed
// yet" distinct from a real zero.
type launcher struct { //nolint:govet // field order is the published property order
	SchemaVersion string   `json:"schema_version"`
	Agent         string   `json:"agent"`
	Binary        string   `json:"binary"`
	Argv          []string `json:"argv"`
	Bundle        string   `json:"bundle"`
	Artifact      string   `json:"artifact"`
	StartedAt     string   `json:"started_at"`
	Timeout       int      `json:"timeout"`
	DurationMS    *int     `json:"duration_ms,omitempty"`
	EndedAt       string   `json:"ended_at,omitempty"`
	Exit          *int     `json:"exit,omitempty"`
	TimedOut      *bool    `json:"timed_out,omitempty"`
	ArtifactFresh *bool    `json:"artifact_fresh,omitempty"`
	ArtifactBytes *int64   `json:"artifact_bytes,omitempty"`
	OutputTail    string   `json:"output_tail,omitempty"`
}

// path is the record's location beside the bundle's other receipts.
func (r *launcher) path(dir string) string { return dir + "/" + launcherFile }

// writeLauncher writes the record atomically and returns the modification
// time the file got. That stamp is the freshness baseline every artifact
// assertion measures against: record and artifact live on the same
// filesystem, so the same clock and the same timestamp granularity apply to
// both, and a review written during the run can never read as older than the
// launch that caused it.
func writeLauncher(path string, rec *launcher) (time.Time, *core.RefusalError) {
	data := []byte(jq.PrettyOrdered(core.Wire(rec)))
	if err := atomic.WriteFile(path, data); err != nil {
		return time.Time{}, &core.RefusalError{
			Code:    codeRecordFailed,
			Message: fmt.Sprintf("could not write the launcher record %s: %v", path, err),
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}, &core.RefusalError{
			Code:    codeRecordFailed,
			Message: fmt.Sprintf("could not stat the launcher record %s: %v", path, err),
		}
	}
	return info.ModTime(), nil
}

// writeRunOutput keeps the child's full combined output for debugging, at
// 0600: the bytes are raw, so they can carry a secret the child printed. A
// failure is a warning, never a refusal: the review file is the contract,
// the log is evidence.
func writeRunOutput(path, output string) error {
	if err := atomic.WriteFileMode(path, []byte(output), runOutputFileMode); err != nil {
		return jq.Sealed("write run output "+path, err)
	}
	return nil
}

// tailOf returns the last n bytes of s.
func tailOf(s string, n int) string {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}

// scrubSecrets replaces every credential value in text: the secret-named
// environment values and the argument secrets the argv redactor hid, because a
// credential the operator typed after `--` is in neither the environment nor
// the prompt. An argument value is not length-gated the way an environment
// match is: the operator named the flag, so the value is a credential by
// construction, and a needlessly scrubbed short one costs nothing but a garbled
// 4000-byte tail that .run.out still holds in full. It is the narrow scrub the
// launcher's recorded tail gets: the operator's own credentials never land in
// launcher.json, while .run.out keeps the raw stream the way verify keeps
// .verify.out (the fuller token-shape scrub belongs to verify's proof record,
// not to a launcher's log line).
//
// One left-to-right pass, taking the longest credential that starts at each
// position, so a position is consumed once and no replacement can begin inside
// bytes a previous one hid. Nor can two credentials share a region by halves:
// `API_KEY=secretvalue` with `--token valueEXTRA` makes
// `secretvalueEXTRA` one credential region, not `***REDACTED***EXTRA`. Sorting
// the values cannot do that — only a prefix is settled by order — and chaining
// [strings.ReplaceAll] can only shrink the text each round, so the overlap is
// resolved here, against the original bytes.
func scrubSecrets(text string, environ, argSecrets []string) string {
	values := secretValues(environ)
	values = append(values, argSecrets...)
	values = dropEmptyValues(values)
	if !anyValueIn(text, values) {
		return text
	}
	var out strings.Builder
	out.Grow(len(text))
	last := 0
	for i := 0; i < len(text); {
		n := longestMatchAt(text[i:], values)
		if n == 0 {
			i++
			continue
		}
		end := coveredEnd(text, i, i+n, values)
		out.WriteString(text[last:i])
		out.WriteString(redactedValue)
		i, last = end, end
	}
	out.WriteString(text[last:])
	return out.String()
}

// dropEmptyValues removes the values that hide nothing: an empty operand
// (`--api-key ""`) is no credential, and matching the empty string everywhere
// would shred the tail between every rune.
func dropEmptyValues(values []string) []string {
	kept := values[:0]
	for _, value := range values {
		if value != "" {
			kept = append(kept, value)
		}
	}
	return kept
}

// anyValueIn reports whether text holds any credential value at all, so the
// common case — a tail with nothing to hide — costs no allocation.
func anyValueIn(text string, values []string) bool {
	for _, value := range values {
		if strings.Contains(text, value) {
			return true
		}
	}
	return false
}

// longestMatchAt returns the length of the longest credential that begins s,
// or 0 when none does. Longest wins so a prefix of a credential cannot be
// taken as the whole one.
func longestMatchAt(s string, values []string) int {
	best := 0
	for _, value := range values {
		if len(value) > best && strings.HasPrefix(s, value) {
			best = len(value)
		}
	}
	return best
}

// coveredEnd extends a matched region — start to end, the credential that
// begins it - over every credential that starts inside the region and reaches
// past its end. Two credentials that overlap without being prefixes are one
// region: the bytes between them belong to a credential, so leaving them
// visible would publish the head or the tail of one. The walk stops at the
// region's end, so it never crosses text that is already accounted for.
func coveredEnd(text string, start, end int, values []string) int {
	for j := start + 1; j < end; j++ {
		if n := longestMatchAt(text[j:], values); j+n > end {
			end = j + n
		}
	}
	return end
}
