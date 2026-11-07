package cli_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
)

func fixedClock() time.Time {
	return time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
}

// TestRefuseExitEnvelope pins the one envelope a `--json` failure emits: the
// manual's key order (schema_version, verb, repo, pr, at, next, warnings,
// data, error), `data: null`, and the error object's additive keys. stdout is
// the envelope and nothing else; the human streams stay untouched when there
// is no re-entry line.
//
// Break killed: moving the pretty-print to Compact, dropping the
// `data: null` field, or rendering the envelope on Diag changes the bytes;
// ignoring the injected clock (r.Now) makes `at` wall time.
func TestRefuseExitEnvelope(t *testing.T) {
	t.Parallel()
	var out, diag bytes.Buffer
	r := &cli.Runner{
		Out:      &out,
		Diag:     &diag,
		Human:    &out,
		JSONMode: true,
		Cmd:      "fetch",
		Repo:     "acme/app",
		Pr:       "47",
		Warnings: []string{"w1"},
		Now:      fixedClock,
	}
	code := r.Refuse(&cli.RefusalError{
		Code:    "diff-too-large",
		Message: "the diff is 600000 bytes (cap 512000)",
	})
	if code != cli.ExitRefused {
		t.Errorf("exit = %d; want %d", code, cli.ExitRefused)
	}
	want := `{
  "schema_version": "1",
  "verb": "fetch",
  "repo": "acme/app",
  "pr": 47,
  "at": "2026-01-02T03:04:05Z",
  "next": "",
  "warnings": [
    "w1"
  ],
  "data": null,
  "error": {
    "code": "diff-too-large",
    "message": "the diff is 600000 bytes (cap 512000)\ntry: split the range (--range/--base/--base-sha) or narrow with a paths filter, then re-fetch"
  }
}
`
	if got := out.String(); got != want {
		t.Errorf("stdout envelope =\n%s\nwant\n%s", got, want)
	}
	if diag.Len() != 0 {
		t.Errorf("stderr = %q; want nothing in --json mode", diag.String())
	}
}

// TestRefuseExitEnvelopeCandidates pins the additive error keys: candidates
// ride only when the runner has them, retry_at only when the refusal sets it,
// and both keep the manual's key order after code/message.
//
// Break killed: dropping the `r.Candidates != nil` or `ref.RetryAt != ""`
// guards emits empty candidates/retry_at keys and changes the bytes.
func TestRefuseExitEnvelopeCandidates(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	r := &cli.Runner{
		Out:        &out,
		Diag:       &bytes.Buffer{},
		JSONMode:   true,
		Cmd:        "post",
		Repo:       "acme/app",
		Pr:         "9",
		Now:        fixedClock,
		Candidates: []any{"split the range by directory"},
	}
	if code := r.Refuse(&cli.RefusalError{
		Code:    "budget-exceeded",
		Message: "projected $2.40 > budget $2.00",
		RetryAt: "2026-01-02T04:00:00Z",
	}); code != cli.ExitRefused {
		t.Fatalf("exit = %d; want %d", code, cli.ExitRefused)
	}
	wantErr := `  "error": {
    "code": "budget-exceeded",
    "message": "projected $2.40 > budget $2.00\ntry: re-run with --consent-over-budget once the spend is approved, or raise REVIEW_BUDGET_USD/the typed budget_usd default",
    "candidates": [
      "split the range by directory"
    ],
    "retry_at": "2026-01-02T04:00:00Z"
  }
}
`
	if got := out.String(); !strings.HasSuffix(got, wantErr) {
		t.Errorf("envelope error object =\n%s\nwant it to end with\n%s", got, wantErr)
	}
}

// TestRefuseExitCanceledExitsBroke pins the GO-CLI-05 rule at the renderer
// that owns it: a cancel renders the ordinary `canceled` envelope and exits 3
// even though every other refusal exits 1, in both stream modes.
//
// Break killed: routing RefuseExit straight to its `code` argument (dropping
// ExitFor) makes the cancel exit 1; negating the ExitFor code comparison
// makes every other refusal exit 3 instead.
func TestRefuseExitCanceledExitsBroke(t *testing.T) {
	t.Parallel()
	var out, diag bytes.Buffer
	r := &cli.Runner{Out: &out, Diag: &diag, JSONMode: true, Cmd: "triage", Repo: "acme/app", Now: fixedClock}
	if code := r.Refuse(&cli.RefusalError{Code: cli.CodeCanceled, Message: "canceled before triage completed"}); code != cli.ExitBroke {
		t.Errorf("--json cancel exit = %d; want %d", code, cli.ExitBroke)
	}
	if !strings.Contains(out.String(), `"code": "canceled"`) {
		t.Errorf("--json cancel stdout = %s; want the canceled envelope", out.String())
	}
	human := &cli.Runner{Out: &diag, Diag: &diag, Effective: cli.Defaults(), Now: fixedClock}
	if code := human.Refuse(&cli.RefusalError{Code: cli.CodeCanceled, Message: "canceled before triage completed"}); code != cli.ExitBroke {
		t.Errorf("human cancel exit = %d; want %d", code, cli.ExitBroke)
	}
	other := &cli.Runner{Out: &diag, Diag: &diag, Effective: cli.Defaults(), Now: fixedClock}
	if code := other.Refuse(&cli.RefusalError{Code: "gate-failed", Message: "the gate said no"}); code != cli.ExitRefused {
		t.Errorf("non-cancel refusal exit = %d; want %d", code, cli.ExitRefused)
	}
}

// TestRefuseHumanLine pins the human refusal line: `error: <message>` plus the
// code's `try:` hint on the diagnostic stream, and the color-always arm wraps
// the whole two-line block in one red span. No envelope reaches stdout.
//
// Break killed: negating the color comparison emits escape bytes in the
// plain arm (or drops them in the colored arm); dropping the "\n"+HintFor
// composition loses the try line.
func TestRefuseHumanLine(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		color string
		want  string
	}{
		{"plain", core.ColorNever, "error: canceled before triage completed\ntry: address the cause above, then re-run the same command\n"},
		{"auto", core.ColorAuto, "error: canceled before triage completed\ntry: address the cause above, then re-run the same command\n"},
		{"always", core.ColorAlways, "\x1b[31merror: canceled before triage completed\ntry: address the cause above, then re-run the same command\x1b[0m\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkHumanRefusal(t, tc.color, tc.want)
		})
	}
}

func checkHumanRefusal(t *testing.T, color, want string) {
	t.Helper()
	var out, diag bytes.Buffer
	r := &cli.Runner{Out: &out, Diag: &diag, Human: &out, Effective: cli.Defaults(), Now: fixedClock}
	r.Effective.Color = color
	r.Refuse(&cli.RefusalError{Code: cli.CodeCanceled, Message: "canceled before triage completed"})
	if got := diag.String(); got != want {
		t.Errorf("stderr = %q; want %q", got, want)
	}
	if out.Len() != 0 {
		t.Errorf("stdout = %q; want no envelope outside --json mode", out.String())
	}
}

// TestRefuseNonRefusalIsInternal pins the fallback for untyped errors: the
// envelope reports code `internal` with the error text, so a bare error never
// renders as a typed refusal code or disappears.
//
// Break killed: negating the `ref, ok := refusalOf(err)` arm drops the
// internal code and reports an empty/zero refusal instead.
func TestRefuseNonRefusalIsInternal(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	r := &cli.Runner{Out: &out, Diag: &bytes.Buffer{}, JSONMode: true, Cmd: "fetch", Now: fixedClock}
	if code := r.Refuse(errors.New("boom")); code != cli.ExitRefused {
		t.Errorf("exit = %d; want %d", code, cli.ExitRefused)
	}
	if !strings.Contains(out.String(), `"code": "internal"`) || !strings.Contains(out.String(), `"message": "boom\ntry:`) {
		t.Errorf("envelope =\n%s\nwant code internal carrying the error text", out.String())
	}
}

// TestRefuseRentryLine pins the post-failure re-entry hint: it follows the
// error block on the diagnostic stream, so the operator's next command is the
// last thing on screen.
//
// Break killed: negating the `r.Rentry != ""` guard drops the line.
func TestRefuseRentryLine(t *testing.T) {
	t.Parallel()
	var out, diag bytes.Buffer
	r := &cli.Runner{Out: &out, Diag: &diag, Human: &out, Effective: cli.Defaults(), Now: fixedClock, Rentry: "record acme/app local-x"}
	r.Refuse(&cli.RefusalError{Code: "no-candidates", Message: "no candidates found"})
	want := "error: no candidates found\ntry: address the cause above, then re-run the same command\nre-enter: record acme/app local-x\n"
	if got := diag.String(); got != want {
		t.Errorf("stderr = %q; want %q", got, want)
	}
}

// TestRefuseTypedCandidates pins RefuseTyped's replay of a typed refusal's
// candidates into the envelope (warnings already have their own test).
//
// Break killed: negating `ref.Candidates != nil` never adopts the typed list,
// so the envelope loses the candidates key.
func TestRefuseTypedCandidates(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	r := &cli.Runner{Out: &out, Diag: &bytes.Buffer{}, JSONMode: true, Cmd: "report", Now: fixedClock}
	r.RefuseTyped(&cli.RefusalError{
		Code:       "no-store",
		Message:    "no store for acme/app#9",
		Candidates: []any{"run fetch first"},
	})
	if got := out.String(); !strings.Contains(got, "run fetch first") {
		t.Errorf("envelope =\n%s\nwant the typed candidates", got)
	}
}

// TestReportUsage pins the usage path: the message alone on the diagnostic
// stream, exit 2, nothing on stdout (usage is not machine output).
//
// Break killed: swapping the exit to ExitRefused, or writing to Out, fails a
// row.
func TestReportUsage(t *testing.T) {
	t.Parallel()
	var out, diag bytes.Buffer
	r := &cli.Runner{Out: &out, Diag: &diag, Human: &out, Effective: cli.Defaults()}
	if code := r.ReportUsage(cli.MissingValue("--color", "auto|always|never")); code != cli.ExitUsage {
		t.Errorf("exit = %d; want %d", code, cli.ExitUsage)
	}
	want := "--color needs a value (want auto|always|never)\n"
	if got := diag.String(); got != want {
		t.Errorf("stderr = %q; want %q", got, want)
	}
	if out.Len() != 0 {
		t.Errorf("stdout = %q; want nothing", out.String())
	}
	if got := cli.MissingValue("--color", "auto").Error(); got != "--color needs a value (want auto)" {
		t.Errorf("UsageError.Error() = %q; want the message alone", got)
	}
}

// TestWarnStreamsAndAccumulates pins the two warn surfaces: the runner's Warn
// writes a colored or plain line to the diagnostic stream and appends the text
// for the envelope; Progress.Diag writes raw lines. Both read the runner's
// current streams.
//
// Break killed: negating Warn's color comparison emits escape bytes in the
// plain row or plain text in the colored row; dropping the append leaves
// Warnings short.
func TestWarnStreamsAndAccumulates(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, color, want string }{
		{"plain", core.ColorAuto, "warn: no gh remote\ntry: run outside --json\nwarn: second even with --json\n"},
		{"always", core.ColorAlways, "\x1b[33mwarn: no gh remote\x1b[0m\ntry: run outside --json\n\x1b[33mwarn: second even with --json\x1b[0m\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkWarnStreams(t, tc.color, tc.want)
		})
	}
}

func checkWarnStreams(t *testing.T, color, want string) {
	t.Helper()
	var diag bytes.Buffer
	r := &cli.Runner{Out: &bytes.Buffer{}, Diag: &diag, Human: &bytes.Buffer{}, Effective: cli.Defaults()}
	r.Effective.Color = color
	p := cli.Progress{R: r}
	r.Warn("no gh remote")
	p.Diag("try: run outside --json")
	p.Warn("second even with --json")
	if got := diag.String(); got != want {
		t.Errorf("stderr = %q; want %q", got, want)
	}
	wantList := []string{"no gh remote", "second even with --json"}
	if len(r.Warnings) != len(wantList) || r.Warnings[0] != wantList[0] || r.Warnings[1] != wantList[1] {
		t.Errorf("Warnings = %v; want %v in call order", r.Warnings, wantList)
	}
}

// TestNowUTCIsSecondPrecisionUTC pins the envelope clock: a zoned time renders
// as its UTC instant, and nanoseconds truncate rather than round.
//
// Break killed: dropping the .UTC() conversion renders +02:00; keeping
// nanoseconds renders three more digits.
func TestNowUTCIsSecondPrecisionUTC(t *testing.T) {
	t.Parallel()
	in := time.Date(2026, time.January, 2, 5, 4, 5, 987654321, time.FixedZone("EET", 2*60*60))
	if got := cli.NowUTC(in); got != "2026-01-02T03:04:05Z" {
		t.Errorf("NowUTC = %q; want 2026-01-02T03:04:05Z", got)
	}
}

// TestSortedKeysIsByteOrder pins the helper that feeds jq's `keys[]`: ASCII
// byte order, so upper case sorts before lower case.
//
// Break killed: a case-folding or locale collation orders [a, A, b] instead.
func TestSortedKeysIsByteOrder(t *testing.T) {
	t.Parallel()
	got := cli.SortedKeys(map[string]any{"b": 1, "A": 2, "a": 3})
	want := []string{"A", "a", "b"}
	if len(got) != len(want) {
		t.Fatalf("SortedKeys = %v; want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("SortedKeys = %v; want %v", got, want)
		}
	}
}

// TestRefuseHumanProblems pins the review-input block: the summary line, every
// problem in document order with its path/code/message and indented fix and
// example lines, the truncation note, then the try and re-enter lines.
func TestRefuseHumanProblems(t *testing.T) {
	t.Parallel()
	var out, diag bytes.Buffer
	r := &cli.Runner{
		Out: &out, Diag: &diag, Human: &out, Effective: cli.Defaults(), Now: fixedClock,
		Rentry: "verdict record acme/app local-x --json",
	}
	code := r.Refuse(&cli.RefusalError{
		Code:    "bad-review-input",
		Message: "review.json has 2 problem(s); fix every one, then re-run",
		Problems: []core.Problem{
			{Path: "$.comments[0].path", Code: "wrong-type", Level: "error", Message: "path must be a string", Fix: "use a changed file path", Example: "file.go"},
			{Path: "$.comments[1].rigor", Code: "rigor-unset", Level: "warning", Message: "rigor is unset", Fix: "set rigor to said|pointed|walked|ran|reproduced"},
		},
		ProblemsTruncated: 3,
	})
	if code != cli.ExitRefused {
		t.Fatalf("exit = %d, want %d", code, cli.ExitRefused)
	}
	want := "error: review.json has 2 problem(s); fix every one, then re-run\n" +
		"$.comments[0].path  wrong-type  path must be a string\n" +
		"  fix: use a changed file path\n" +
		"  example: \"file.go\"\n" +
		"$.comments[1].rigor  rigor-unset  rigor is unset\n" +
		"  fix: set rigor to said|pointed|walked|ran|reproduced\n" +
		"  ... 3 more problem(s) not shown\n" +
		"try: fix the malformed agent input named above (review.json wants {body, approved, comments[]}; claims want string sigs), then re-run\n" +
		"re-enter: verdict record acme/app local-x --json\n"
	if got := diag.String(); got != want {
		t.Errorf("stderr =\n%q\nwant\n%q", got, want)
	}
	if out.Len() != 0 {
		t.Errorf("stdout = %q; want no envelope outside --json mode", out.String())
	}
}
