// Package doctorcore is the doctor verb's domain service: the harness
// preflight (gh auth and storage writability hard-fail the run; scopes,
// git, ps, and API reachability warn only). No repo/PR args, no writes, no
// network beyond the gh reads through the injected commander.
// internal/doctor owns argv parsing, progress routing, envelope rendering,
// and exit mapping.
package doctorcore

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/statedir"
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

func allHealthy(checks []Check) bool {
	for _, c := range checks {
		if c.Severity == severityFail && !c.OK {
			return false
		}
	}
	return true
}

const (
	severityFail = "fail"
	severityWarn = "warn"
)

const newline = "\n"

const checkStorageWritable = "storage-writable"

const anyWrite = 0o222

type state struct {
	req      *Request
	warnings []string
	checks   []Check
}

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

func (st *state) human(line string) {
	if st.req.Runtime != nil {
		st.req.Human(line)
	}
}

func (st *state) commander() core.Commander {
	var commander core.Commander
	if rt := st.runtime(); rt != nil {
		commander = rt.Commander
	}
	return commander
}

func (st *state) runtime() *core.Runtime {
	return st.req.Runtime
}

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

func (st *state) checkGhAuth(ctx context.Context) {
	if _, _, ok := coreexec.Gh(ctx, st.commander(), "auth", "status"); !ok {
		st.add("gh-auth", severityFail, false, "run gh auth login")
		return
	}
	st.add("gh-auth", severityFail, true, "authenticated")
}

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

func (st *state) checkStorage() {
	root := statedir.Resolve()
	if stat, err := os.Stat(root.Root); err == nil && stat.IsDir() {
		if writable(root.Root) {
			st.add(checkStorageWritable, severityFail, true,
				fmt.Sprintf("%s writable (scope %s, source %s)", root.Root, root.Scope, root.Source))
		} else {
			st.add(checkStorageWritable, severityFail, false,
				root.Root+" not writable — fetch/record cannot write bundles here")
		}
		return
	}
	if writable(root.Base) {
		st.add(checkStorageWritable, severityFail, true,
			fmt.Sprintf("%s writable — %s created on first fetch", root.Base, root.Root))
		return
	}
	st.add(checkStorageWritable, severityFail, false, root.Base+" not writable — fetch/record cannot create "+root.Root)
}

func writable(path string) bool {
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	return fi.Mode().Perm()&anyWrite != 0
}

func firstLine(s string) string {
	if before, _, ok := strings.Cut(s, newline); ok {
		return before
	}
	return s
}

func dropScopeSpace(r rune) rune {
	if r == ' ' || r == '\t' || r == '\r' {
		return -1
	}
	return r
}

func headerLine(stdout string) string {
	for line := range strings.SplitSeq(stdout, newline) {
		if len(line) >= len("x-oauth-scopes:") && strings.EqualFold(line[:len("x-oauth-scopes:")], "x-oauth-scopes:") {
			return line
		}
	}
	return ""
}
