// Package forgedline is test support for the refusal-message choke point,
// core.Msgf: a value that tries every way to end verdict's `error:` line and
// start one of its own, and the check that it did not. Only tests import it.
package forgedline

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/cli"
)

// newline ends a rendered line.
const newline = "\n"

// Value carries every break a stranger could use: a newline, a CRLF, the
// U+2028 line separator, and a U+202E right-to-left override.
const Value = "x\nerror: forged\r\nerror: crlf\xe2\x80\xa8error: sep\xe2\x80\xaelive"

// Named is Value as the one line it must read as: each break and the
// override folded to a space.
const Named = "x error: forged  error: crlf error: sep live"

// AssertOneErrorLine renders one refusal through the CLI writer and pins
// exactly one `error:` line that still names the value, then renders it
// under --json and pins an error.message that names it the same way, holds
// no CR or line separator, and has no line of its own that starts as
// verdict's `error:` or `warn:` does: a refusal that lays out several lines
// keeps them, and a stranger's value adds none.
func AssertOneErrorLine(tb testing.TB, err error, named string) {
	tb.Helper()
	if err == nil {
		tb.Fatal("no refusal")
	}
	var out, diag bytes.Buffer
	r := cli.NewRunner("forged", "acme/app", "1", &out, &diag, func(string) string { return "" })
	r.Refuse(err)
	var lines []string
	for line := range strings.SplitSeq(diag.String(), newline) {
		if strings.HasPrefix(line, "error:") {
			lines = append(lines, line)
		}
	}
	if len(lines) != 1 || !strings.Contains(lines[0], named) {
		tb.Errorf("error lines = %q, want exactly one naming %q", lines, named)
	}
	assertJSONMessage(tb, err, named)
}

func assertJSONMessage(tb testing.TB, err error, named string) {
	tb.Helper()
	var out, diag bytes.Buffer
	r := cli.NewRunner("forged", "acme/app", "1", &out, &diag, func(string) string { return "" })
	r.JSONMode = true
	r.Refuse(err)
	var env struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if jerr := json.Unmarshal(out.Bytes(), &env); jerr != nil {
		tb.Fatalf("--json envelope: %v\n%s", jerr, out.String())
	}
	msg := env.Error.Message
	if strings.ContainsAny(msg, "\r\xe2\x80\xa8") || forgesLine(msg) || !strings.Contains(msg, named) {
		tb.Errorf("--json error.message = %q, want no forged line, naming %q", env.Error.Message, named)
	}
}

// forgesLine reports a line after the first that starts as verdict's own
// `error:` or `warn:` line does.
func forgesLine(msg string) bool {
	_, rest, _ := strings.Cut(msg, newline)
	for line := range strings.SplitSeq(rest, newline) {
		if strings.HasPrefix(line, "error:") || strings.HasPrefix(line, "warn:") {
			return true
		}
	}
	return false
}

// AssertNoForgedLine fails on any line of out that starts as verdict's own
// `error:` or `warn:` line does, and on out not naming named.
func AssertNoForgedLine(tb testing.TB, out, named string) {
	tb.Helper()
	for line := range strings.SplitSeq(out, newline) {
		if strings.HasPrefix(line, "error:") || strings.HasPrefix(line, "warn:") {
			tb.Errorf("forged line %q in:\n%s", line, out)
		}
	}
	if !strings.Contains(out, named) {
		tb.Errorf("output does not name %q:\n%s", named, out)
	}
}

// Recorder is a core.Progress that keeps every human and diagnostic line a
// service emits, joined as a terminal would show them.
type Recorder struct {
	lines []string
}

// Human records one human line.
func (r *Recorder) Human(line string) { r.lines = append(r.lines, line) }

// Warn records nothing: the warning writer folds its own line.
func (r *Recorder) Warn(string) {}

// Diag records one diagnostic line.
func (r *Recorder) Diag(line string) { r.lines = append(r.lines, line) }

// String is every recorded line, one per line.
func (r *Recorder) String() string { return strings.Join(r.lines, newline) }
