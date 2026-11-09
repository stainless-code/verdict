package run

import (
	"fmt"
	"os"
	"time"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/envsecret"
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
// The matching is [envsecret.ScrubValues], shared with verify's redactor so an
// overlap between two credentials is resolved once, in one place. What lives
// here is only the policy: which values count (environment plus argv) and that
// neither set is length-gated.
func scrubSecrets(text string, environ, argSecrets []string) string {
	values := secretValues(environ)
	values = append(values, argSecrets...)
	return envsecret.ScrubValues(text, values)
}
