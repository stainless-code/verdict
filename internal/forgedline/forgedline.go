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

// Value carries every break a stranger could use: a newline, a CRLF, the
// U+2028 line separator, and a U+202E right-to-left override.
const Value = "x\nerror: forged\r\nerror: crlf\xe2\x80\xa8error: sep\xe2\x80\xaelive"

// Named is Value as the one line it must read as: each break and the
// override folded to a space.
const Named = "x error: forged  error: crlf error: sep live"

// AssertOneErrorLine renders one refusal through the CLI writer and pins
// exactly one `error:` line that still names the value, then renders it
// under --json and pins an error.message whose one line names it the same
// way, ahead of the `try:` hint line verdict appends.
func AssertOneErrorLine(tb testing.TB, err error, named string) {
	tb.Helper()
	if err == nil {
		tb.Fatal("no refusal")
	}
	var out, diag bytes.Buffer
	r := cli.NewRunner("forged", "acme/app", "1", &out, &diag, func(string) string { return "" })
	r.Refuse(err)
	var lines []string
	for line := range strings.SplitSeq(diag.String(), "\n") {
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
	if i := strings.LastIndex(msg, "\ntry: "); i >= 0 {
		msg = msg[:i]
	}
	if strings.ContainsAny(msg, "\n\r\xe2\x80\xa8") || !strings.Contains(msg, named) {
		tb.Errorf("--json error.message = %q, want one line naming %q", env.Error.Message, named)
	}
}
