// Package doctor ports the `doctor` verb: the harness preflight (gh auth,
// jq, shasum hard-fail the run; scopes, git, ps, API reachability warn
// only; storage writability hard-fails). No repo/PR args, no writes, no
// network beyond the existing gh reads.
package doctor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// Request wires a doctor run.
type Request = readcommon.Request

// Run executes `doctor [--json]`, returning the exit code.
func Run(ctx context.Context, r *cli.Runner, args []string, _ Request) int {
	if code, done := parseFlags(r, args); done {
		return code
	}
	if err := ctx.Err(); err != nil {
		return r.RefuseExit(&cli.RefusalError{Code: "canceled", Message: "canceled before doctor completed"}, cli.ExitBroke)
	}
	checks := examine(ctx, r)
	healthy := allHealthy(checks)
	if r.JSONMode {
		emitEnvelope(r, healthy, checks)
	}
	if healthy {
		return 0
	}
	return 1
}

// allHealthy ports the fail-row scan: any failing check fails the run.
func allHealthy(checks []check) bool {
	for _, c := range checks {
		if c.Severity == severityFail && !c.OK {
			return false
		}
	}
	return true
}

// check severities, mirroring the shell's fail|warn column.
const (
	severityFail = "fail"
	severityWarn = "warn"
)

// parseFlags parses the doctor flags.
func parseFlags(r *cli.Runner, args []string) (int, bool) {
	for _, a := range args {
		switch a {
		case "--json":
			r.JSONMode = true
			routeHuman(r)
		default:
			return r.ReportUsage(&cli.UsageError{Message: fmt.Sprintf("unknown flag '%s' for doctor (want --json)", a)}), true
		}
	}
	return 0, false
}

// routeHuman sends human progress to stderr in JSON mode.
func routeHuman(r *cli.Runner) {
	if r.JSONMode {
		r.Human = r.Diag
	} else {
		r.Human = r.Out
	}
}

// check is one doctor row.
type check struct {
	Name     string
	Severity string
	Detail   string
	OK       bool
}

// newline splits output lines and trims command-substitution newlines.
const newline = "\n"

// checkStorageWritable names the storage row.
const checkStorageWritable = "storage-writable"

// anyWrite matches the shell's -w test where the process owns the file:
// any write bit reads writable.
const anyWrite = 0o222

// examine ports the doctor check sequence, printing human lines as it goes.
func examine(ctx context.Context, r *cli.Runner) []check {
	d := &doctor{runner: r}
	d.checkGhCLI(ctx)
	d.checkGhAuth(ctx)
	scopes := d.checkTokenScopes(ctx)
	if scopes != "" {
		d.checkTokenExcess(scopes)
	}
	d.checkTool(ctx, "jq", "jq --version", "missing — install jq")
	d.checkTool(ctx, "shasum", "", "sig hashing hard-requires shasum (startup gate)")
	d.checkTool(ctx, "ps", "", "verify runs without timeout enforcement")
	d.checkTool(ctx, "git", "", "check-tree and verify degraded without git")
	d.checkAPIReachable(ctx)
	d.checkStorage()
	return d.checks
}

// doctor carries the check accumulator. The context threads through the
// method arguments (never stored) per the contained-context discipline.
type doctor struct {
	runner *cli.Runner
	checks []check
}

// add ports add(): the human line plus the warnings accumulation.
func (d *doctor) add(name, severity string, ok bool, detail string) {
	d.checks = append(d.checks, check{Name: name, Severity: severity, OK: ok, Detail: detail})
	switch {
	case ok:
		fmt.Fprintf(d.runner.Human, "ok   %s — %s\n", name, detail)
	case severity == severityWarn:
		fmt.Fprintf(d.runner.Human, "WARN %s — %s\n", name, detail)
		d.runner.Warn(name + ": " + detail)
	default:
		fmt.Fprintf(d.runner.Human, "FAIL %s — %s\n", name, detail)
	}
}

// checkGhCLI ports the gh-cli row.
func (d *doctor) checkGhCLI(ctx context.Context) {
	if _, err := exec.LookPath("gh"); err != nil {
		d.add("gh-cli", severityFail, false, "missing — install github.com/cli/cli")
		return
	}
	version := ""
	if res, err := readcommon.Gh(ctx, "--version"); err == nil {
		version = firstLine(res.Stdout)
	}
	d.add("gh-cli", severityFail, true, version)
}

// checkGhAuth ports the gh-auth row.
func (d *doctor) checkGhAuth(ctx context.Context) {
	_, err := readcommon.Gh(ctx, "auth", "status")
	if err != nil {
		d.add("gh-auth", severityFail, false, "run gh auth login")
		return
	}
	d.add("gh-auth", severityFail, true, "authenticated")
}

// checkTokenScopes ports the token-scopes row, returning the raw header
// line ("" when the lookup fails, matching the shell's || true).
func (d *doctor) checkTokenScopes(ctx context.Context) string {
	res, err := readcommon.Gh(ctx, "api", "user", "--include")
	scopes := ""
	if err == nil {
		scopes = headerLine(res.Stdout)
	}
	switch {
	case strings.Contains(scopes, "repo"):
		d.add("token-scopes", severityWarn, true, scopes)
	case scopes == "":
		d.add("token-scopes", severityWarn, true, "no scopes header — fine-grained token, scopes proven at post time")
	default:
		d.add("token-scopes", severityWarn, false, "needs repo (private) or public_repo scope — "+scopes)
	}
	return scopes
}

// checkTokenExcess ports the token-scope excess audit: granted scopes
// beyond the repo family plus read:* warn on a token-scopes-excess row.
func (d *doctor) checkTokenExcess(scopes string) {
	value := scopes
	if i := strings.Index(value, ":"); i >= 0 {
		value = value[i+1:]
	}
	var excess []string
	for scope := range strings.SplitSeq(strings.ToLower(value), ",") {
		scope = strings.Map(dropScopeSpace, scope)
		switch {
		case scope == "", scope == "repo", scope == "public_repo":
		case strings.HasPrefix(scope, "read:"):
		default:
			excess = append(excess, scope)
		}
	}
	if len(excess) == 0 {
		return
	}
	excessMsg := "token grants more than runs need: " + strings.Join(excess, ", ") +
		" — try: re-mint with only the repo scope (least privilege) and re-run"
	d.add("token-scopes-excess", severityWarn, false, excessMsg)
}

// checkTool ports the jq/shasum/ps/git rows: presence gates the detail.
func (d *doctor) checkTool(ctx context.Context, name, _ string, missing string) {
	present := true
	if _, err := exec.LookPath(name); err != nil {
		present = false
	}
	detail := missing
	okDetail := map[string]string{
		"shasum": "sig hashing available",
		"ps":     "verify timeout enforced",
		"git":    "check-tree/verify available",
	}
	if present {
		detail = okDetail[name]
		if name == "jq" {
			detail = jqVersionLine(ctx)
		}
	}
	severity := severityFail
	if name == "ps" || name == "git" {
		severity = severityWarn
	}
	d.add(name, severity, present, detail)
}

// jqVersionLine runs `jq --version`, returning stdout with trailing
// newlines stripped like the shell's command substitution.
func jqVersionLine(ctx context.Context) string {
	cmd := exec.CommandContext(ctx, "jq", "--version")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(out), newline)
}

// checkAPIReachable ports the api-reachable row.
func (d *doctor) checkAPIReachable(ctx context.Context) {
	res, err := readcommon.Gh(ctx, "api", "rate_limit", "--jq", ".resources.core.remaining")
	remaining := ""
	if err == nil {
		remaining = strings.TrimRight(res.Stdout, newline)
	}
	if remaining != "" {
		d.add("api-reachable", severityWarn, true, "core quota remaining: "+remaining)
		return
	}
	d.add("api-reachable", severityWarn, false, "cannot reach api.github.com with this token")
}

// checkStorage ports the storage-writability row over the .review bundle
// dir or the cwd. Writability reads permission bits (the shell's -w test
// via access(2) agrees wherever tests run as the file owner); no probe
// files are written, so the read-only discipline holds.
func (d *doctor) checkStorage() {
	if stat, err := os.Stat(".review"); err == nil && stat.IsDir() {
		if writable(".review") {
			d.add(checkStorageWritable, severityFail, true, ".review/ writable")
		} else {
			d.add(checkStorageWritable, severityFail, false,
				".review/ not writable — fetch/record cannot write bundles here")
		}
		return
	}
	if writable(".") {
		d.add(checkStorageWritable, severityFail, true, "cwd writable — .review/ created on first fetch")
		return
	}
	d.add(checkStorageWritable, severityFail, false, "cwd not writable — fetch/record cannot create .review/ here")
}

// writable reports whether any write bit is set.
func writable(path string) bool {
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	return fi.Mode().Perm()&anyWrite != 0
}

// firstLine ports `head -n 1`: the first output line.
func firstLine(s string) string {
	if before, _, ok := strings.Cut(s, newline); ok {
		return before
	}
	return s
}

// dropScopeSpace deletes scope-list whitespace, mirroring tr -d ' \t\r'.
func dropScopeSpace(r rune) rune {
	if r == ' ' || r == '\t' || r == '\r' {
		return -1
	}
	return r
}

// headerLine ports the scopes header read: the first case-insensitive
// x-oauth-scopes line, kept raw like the shell's grep|head pipeline.
func headerLine(stdout string) string {
	for line := range strings.SplitSeq(stdout, newline) {
		if len(line) >= len("x-oauth-scopes:") && strings.EqualFold(line[:len("x-oauth-scopes:")], "x-oauth-scopes:") {
			return line
		}
	}
	return ""
}

// emitEnvelope ports the doctor --json envelope.
func emitEnvelope(r *cli.Runner, healthy bool, checks []check) {
	rows := make([]any, 0, len(checks))
	for _, c := range checks {
		rows = append(rows, jq.NewObj(
			"name", c.Name,
			"severity", c.Severity,
			"ok", c.OK,
			"detail", c.Detail,
		))
	}
	next := "fix FAIL lines above (WARN lines are advisory), then re-run"
	if healthy {
		next = "harness healthy"
	}
	env := jq.NewObj(
		"schema_version", "1",
		"verb", "doctor",
		"repo", "",
		"pr", nil,
		"at", cli.NowUTC(r.Now()),
		"next", next,
		"warnings", cliStrings(r.Warnings),
		"data", jq.NewObj("healthy", healthy, "checks", rows),
		"error", nil,
	)
	fmt.Fprint(r.Out, jq.PrettyOrdered(env))
}

// cliStrings converts warnings to JSON string values.
func cliStrings(in []string) []any {
	out := make([]any, 0, len(in))
	for _, s := range in {
		out = append(out, s)
	}
	return out
}
