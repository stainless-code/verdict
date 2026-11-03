// Package doctorcore is the doctor verb's domain service: the harness
// preflight (gh auth and storage writability hard-fail the run; scopes,
// git, ps, and API reachability warn only). No repo/PR args, no writes, no
// network beyond the gh reads through the injected commander.
// internal/doctor owns argv parsing, progress routing, envelope rendering,
// and exit mapping.
package doctorcore

import (
	"context"
	"os"
	"os/exec"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/jq"
)

// Request wires a doctor run.
type Request struct {
	core.ReadRequest
	Warnings []string
}

// Response carries the completed preflight: the envelope fields and the
// typed data profile. Human lines stream live through the runtime
// progress observer, so the response carries no rendered lines.
type Response struct { //nolint:govet // field grouping documents the response surface
	At       string
	Next     string
	Warnings []string
	Data     *Data
	Healthy  bool
}

// Data is the doctor verb's --json data profile. Field order is the
// published order.
type Data struct { //nolint:govet // field order is the published property order
	Healthy bool
	Checks  []Check
}

// Check is one doctor preflight row.
type Check struct { //nolint:govet // field order is the published property order
	Name     string
	Severity string
	OK       bool
	Detail   string
}

// Wire renders the profile in published wire order.
func (d *Data) Wire() *jq.Obj {
	rows := make([]any, 0, len(d.Checks))
	for _, c := range d.Checks {
		rows = append(rows, jq.NewObj(
			"name", c.Name,
			"severity", c.Severity,
			"ok", c.OK,
			"detail", c.Detail,
		))
	}
	return jq.NewObj("healthy", d.Healthy, "checks", rows)
}

// Counts tallies the human display buckets over the existing check results:
// ok rows (ok true, whatever the severity), advisory warnings (ok false with
// warn severity), and failures. The healthy predicate stays allHealthy; this
// only counts the rows the human path already renders.
func (d *Data) Counts() (ok, warn, fail int) {
	for _, c := range d.Checks {
		switch {
		case c.OK:
			ok++
		case c.Severity == severityWarn:
			warn++
		default:
			fail++
		}
	}
	return ok, warn, fail
}

// Run executes the harness preflight.
func Run(ctx context.Context, req *Request) (*Response, *core.RefusalError) {
	st := &state{req: req, warnings: append([]string{}, req.Warnings...)}
	if err := ctx.Err(); err != nil {
		return nil, &core.RefusalError{Code: "canceled", Message: "canceled before doctor completed"}
	}
	st.examine(ctx)
	healthy := allHealthy(st.checks)
	next := "fix FAIL lines above (WARN lines are advisory), then re-run"
	if healthy {
		next = "harness healthy"
	}
	return &Response{
		At:       req.TimeUTC(),
		Next:     next,
		Warnings: st.warnings,
		Data:     &Data{Healthy: healthy, Checks: st.checks},
		Healthy:  healthy,
	}, nil
}

// allHealthy owns the fail-row scan: any failing check fails the run.
func allHealthy(checks []Check) bool {
	for _, c := range checks {
		if c.Severity == severityFail && !c.OK {
			return false
		}
	}
	return true
}

// check severities (the fail|warn column).
const (
	severityFail = "fail"
	severityWarn = "warn"
)

// newline splits output lines and trims command-substitution newlines.
const newline = "\n"

// checkStorageWritable names the storage row.
const checkStorageWritable = "storage-writable"

// anyWrite applies the write test where the process owns the file:
// any write bit reads writable.
const anyWrite = 0o222

// state carries the check accumulator. The context threads through the
// method arguments (never stored) per the contained-context discipline.
type state struct {
	req      *Request
	warnings []string
	checks   []Check
}

// examine owns the doctor check sequence, printing human lines as it goes.
func (st *state) examine(ctx context.Context) {
	st.checkGhCLI(ctx)
	st.checkGhAuth(ctx)
	scopes := st.checkTokenScopes(ctx)
	if scopes != "" {
		st.checkTokenExcess(scopes)
	}
	st.checkTool("ps", "verify runs without timeout enforcement")
	st.checkTool("git", "check-tree and verify degraded without git")
	st.checkAPIReachable(ctx)
	st.checkStorage()
}

// add appends one check: the human line plus the warnings accumulation.
func (st *state) add(name, severity string, ok bool, detail string) {
	st.checks = append(st.checks, Check{Name: name, Severity: severity, OK: ok, Detail: detail})
	switch {
	case ok:
		st.human("ok   " + name + " — " + detail)
	case severity == severityWarn:
		st.human("WARN " + name + " — " + detail)
		st.warnings = append(st.warnings, name+": "+detail)
	default:
		st.human("FAIL " + name + " — " + detail)
	}
}

// human streams one human output line.
func (st *state) human(line string) {
	if st.req.Runtime != nil {
		st.req.Human(line)
	}
}

// commander returns the injected commander (nil when the caller set none).
func (st *state) commander() core.Commander {
	var commander core.Commander
	if rt := st.runtime(); rt != nil {
		commander = rt.Commander
	}
	return commander
}

// runtime returns the injected runtime (nil when the caller set none).
func (st *state) runtime() *core.Runtime {
	return st.req.Runtime
}

// checkGhCLI owns the gh-cli row.
func (st *state) checkGhCLI(ctx context.Context) {
	if _, err := exec.LookPath("gh"); err != nil {
		st.add("gh-cli", severityFail, false, "missing — install github.com/cli/cli")
		return
	}
	version := ""
	if stdout, _, ok := coreexec.Gh(ctx, st.commander(), "--version"); ok {
		version = firstLine(stdout)
	}
	st.add("gh-cli", severityFail, true, version)
}

// checkGhAuth owns the gh-auth row.
func (st *state) checkGhAuth(ctx context.Context) {
	if _, _, ok := coreexec.Gh(ctx, st.commander(), "auth", "status"); !ok {
		st.add("gh-auth", severityFail, false, "run gh auth login")
		return
	}
	st.add("gh-auth", severityFail, true, "authenticated")
}

// checkTokenScopes owns the token-scopes row, returning the raw header
// line ("" when the lookup fails).
func (st *state) checkTokenScopes(ctx context.Context) string {
	stdout, _, ok := coreexec.Gh(ctx, st.commander(), "api", "user", "--include")
	scopes := ""
	if ok {
		scopes = headerLine(stdout)
	}
	switch {
	case strings.Contains(scopes, "repo"):
		st.add("token-scopes", severityWarn, true, scopes)
	case scopes == "":
		st.add("token-scopes", severityWarn, true, "no scopes header — fine-grained token, scopes proven at post time")
	default:
		st.add("token-scopes", severityWarn, false, "needs repo (private) or public_repo scope — "+scopes)
	}
	return scopes
}

// checkTokenExcess owns the token-scope excess audit: granted scopes
// beyond the repo family plus read:* warn on a token-scopes-excess row.
func (st *state) checkTokenExcess(scopes string) {
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
	st.add("token-scopes-excess", severityWarn, false, excessMsg)
}

// checkTool owns the ps/git rows: presence gates the detail. Both warn only;
// the Go binary needs neither to read a bundle.
func (st *state) checkTool(name, missing string) {
	present := true
	if _, err := exec.LookPath(name); err != nil {
		present = false
	}
	detail := missing
	if present {
		detail = map[string]string{
			"ps":  "verify timeout enforced",
			"git": "check-tree/verify available",
		}[name]
	}
	st.add(name, severityWarn, present, detail)
}

// checkAPIReachable owns the api-reachable row.
func (st *state) checkAPIReachable(ctx context.Context) {
	stdout, _, ok := coreexec.Gh(ctx, st.commander(), "api", "rate_limit", "--jq", ".resources.core.remaining")
	remaining := ""
	if ok {
		remaining = strings.TrimRight(stdout, newline)
	}
	if remaining != "" {
		st.add("api-reachable", severityWarn, true, "core quota remaining: "+remaining)
		return
	}
	st.add("api-reachable", severityWarn, false, "cannot reach api.github.com with this token")
}

// checkStorage owns the storage-writability row over the .review bundle
// dir or the cwd. Writability reads permission bits (the write test
// via access(2) agrees wherever tests run as the file owner); no probe
// files are written, so the read-only discipline holds.
func (st *state) checkStorage() {
	if stat, err := os.Stat(".review"); err == nil && stat.IsDir() {
		if writable(".review") {
			st.add(checkStorageWritable, severityFail, true, ".review/ writable")
		} else {
			st.add(checkStorageWritable, severityFail, false,
				".review/ not writable — fetch/record cannot write bundles here")
		}
		return
	}
	if writable(".") {
		st.add(checkStorageWritable, severityFail, true, "cwd writable — .review/ created on first fetch")
		return
	}
	st.add(checkStorageWritable, severityFail, false, "cwd not writable — fetch/record cannot create .review/ here")
}

// writable reports whether any write bit is set.
func writable(path string) bool {
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	return fi.Mode().Perm()&anyWrite != 0
}

// firstLine returns the first output line.
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

// headerLine owns the scopes header read: the first case-insensitive
// x-oauth-scopes line, kept raw.
func headerLine(stdout string) string {
	for line := range strings.SplitSeq(stdout, newline) {
		if len(line) >= len("x-oauth-scopes:") && strings.EqualFold(line[:len("x-oauth-scopes:")], "x-oauth-scopes:") {
			return line
		}
	}
	return ""
}
