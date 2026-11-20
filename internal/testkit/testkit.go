// Package testkit provides fixtures and service doubles for tests.
package testkit

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/testenv"
)

// WriteFile writes a fixture and creates its parent directories.
func WriteFile(tb testing.TB, path, content string) {
	tb.Helper()
	if err := os.MkdirAll(filepath.Dir(path), fixtureDirMode); err != nil {
		tb.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(testenv.MaybeFindingsReview(path, content)), fixtureFileMode); err != nil {
		tb.Fatal(err)
	}
}

// Collector records service progress in emission order within each stream.
type Collector struct {
	HumanLines  []string
	Warnings    []string
	Diagnostics []string
}

// Human records a human output line.
func (c *Collector) Human(line string) { c.HumanLines = append(c.HumanLines, line) }

// Warn records a warning.
func (c *Collector) Warn(line string) { c.Warnings = append(c.Warnings, line) }

// Diag records a diagnostic line.
func (c *Collector) Diag(line string) { c.Diagnostics = append(c.Diagnostics, line) }

// FixedNow returns the shared deterministic service clock.
func FixedNow() time.Time {
	return time.Date(fixedYear, time.November, fixedDay, fixedHour, 0, 0, 0, time.UTC)
}

// Script answers exact argv keys joined with NUL and rejects unknown commands.
type Script struct {
	T       testing.TB
	Results map[string]core.Result
	Errors  map[string]error
	Calls   []*core.Command
}

// Run records the command and returns its registered result.
func (s *Script) Run(_ context.Context, cmd *core.Command) (core.Result, error) {
	s.T.Helper()
	snapshot := *cmd
	snapshot.Argv = slices.Clone(cmd.Argv)
	s.Calls = append(s.Calls, &snapshot)
	key := strings.Join(cmd.Argv, "\x00")
	res, ok := s.Results[key]
	err, hasErr := s.Errors[key]
	if !ok && !hasErr {
		s.T.Errorf("unexpected argv: %q", cmd.Argv)
		return core.Result{}, scriptError("unexpected argv")
	}
	return res, err
}

// Diff is a three-line fixture with one replaced line.
const Diff = "diff --git a/notes.txt b/notes.txt\n" +
	"index 1111111..2222222 100644\n" +
	"--- a/notes.txt\n" +
	"+++ b/notes.txt\n" +
	"@@ -1,3 +1,3 @@\n" +
	" top\n" +
	"-old line\n" +
	"+new line\n" +
	" bottom\n"

// PRRecord is an open GitHub PR fixture.
const PRRecord = `{"state":"OPEN","headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",` +
	`"baseRefOid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","author":{"login":"teammate"}}`

const (
	fixtureDirMode  = 0o755
	fixtureFileMode = 0o600
	fixedYear       = 2026
	fixedDay        = 2
	fixedHour       = 12
)

type scriptError string

func (e scriptError) Error() string { return string(e) }
