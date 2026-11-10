// Package doctorcore is the doctor verb's domain service: the harness
// preflight for one configured host (the host CLI, its auth, and API
// reachability), the shared tool probes, and storage writability; auth, the
// host CLI, and storage hard-fail the run, while scopes, git, ps, and
// reachability warn only. No repo/PR args, no writes, no network beyond the
// host CLI reads through the injected commander. internal/doctor owns argv
// parsing, progress routing, envelope rendering, and exit mapping.
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
	"github.com/stainless-code/verdict/internal/readcommon"
)

// Request wires a doctor run. Provider names the host whose CLI the preflight
// checks, with the fetch enum; empty keeps the GitHub posture.
type Request struct {
	core.ReadRequest
	Provider string `enum:"github|gitlab|gitcafe|forgejo|bitbucket|azure"`
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

// Run executes the harness preflight for the requested host.
func Run(ctx context.Context, req *Request) (*Response, *core.RefusalError) {
	host := hostOf(req)
	if _, known := hostChecks[host]; !known {
		return nil, &core.RefusalError{Code: codeBadProvider, Message: fmt.Sprintf(msgBadProvider, host)}
	}
	st := &state{req: req, host: host, warnings: append([]string{}, req.Warnings...)}
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

const (
	checkStorageWritable = "storage-writable"
	checkAPIReachable    = "api-reachable"
)

const anyWrite = 0o222

// The hosts doctor can preflight, spelled as fetch spells them so one enum
// spans both verbs.
const (
	providerGitHub    = "github"
	providerGitLab    = "gitlab"
	providerGitCafe   = "gitcafe"
	providerForgejo   = "forgejo"
	providerBitbucket = "bitbucket"
	providerAzure     = "azure"
)

// The host CLIs the preflight shells, spelled once. GitHub's `gh` is not here:
// coreexec.Gh owns that name for every verb that reads through it.
const (
	binGlab = "glab"
	binCafe = "cafe"
	binCurl = "curl"
	binAz   = "az"
)

// The credential variables the fetch path reads per host. Doctor reports
// whether they are set, never their values.
//
//nolint:gosec // G101: these name the variables to read, never a credential value
const (
	envForgejoHost    = "FORGEJO_HOST"
	envForgejoToken   = "FORGEJO_TOKEN"
	envBitbucketToken = "BITBUCKET_TOKEN"
	envBitbucketUser  = "BITBUCKET_USERNAME"
	envBitbucketPass  = "BITBUCKET_PASSWORD"
	envAzureOrg       = "AZURE_DEVOPS_ORG"
	envAzurePAT       = "AZURE_DEVOPS_EXT_PAT"
)

const (
	codeBadProvider = "bad-provider"
	msgBadProvider  = "unknown --provider '%s' (want github|gitlab|gitcafe|forgejo|bitbucket|azure)"
)

type state struct {
	req      *Request
	host     string
	warnings []string
	checks   []Check
}

// hostProbe is one provider-specific preflight step. A step appends its own
// rows, so a read that reports twice (github's scope header and its excess row)
// stays one entry.
type hostProbe func(ctx context.Context, st *state)

// hostChecks is the per-host preflight table: head runs before the shared tool
// rows and tail after them, so every host keeps one documented row order — its
// own CLI, then ps and git, then its reachability read. A provider missing from
// this table is refused before any probe runs, so adding a host is one row here
// plus its probes, never a branch in the run.
var hostChecks = map[string]struct{ head, tail []hostProbe }{
	providerGitHub: {
		head: []hostProbe{checkGhCLI, checkGhAuth, checkTokenScopes},
		tail: []hostProbe{checkGitHubReachable},
	},
	providerGitLab: {
		head: []hostProbe{checkGlabCLI, checkGlabAuth},
		tail: []hostProbe{checkGitLabReachable},
	},
	providerGitCafe:   {head: []hostProbe{checkCafeCLI}},
	providerForgejo:   {head: []hostProbe{checkCurl, checkForgejoAuth}},
	providerBitbucket: {head: []hostProbe{checkCurl, checkBitbucketAuth}},
	providerAzure:     {head: []hostProbe{checkAzCLI, checkAzureAuth}},
}

// hostOf resolves the host to preflight: an unset flag keeps today's GitHub
// posture.
func hostOf(req *Request) string {
	if req.Provider == "" {
		return providerGitHub
	}
	return req.Provider
}

// examine runs the preflight: the host's own probes, the shared tool rows, the
// host's reachability read, and storage.
func (st *state) examine(ctx context.Context) {
	host := hostChecks[st.host]
	for _, probe := range host.head {
		probe(ctx, st)
	}
	st.checkTool("ps", "verify runs without timeout enforcement")
	st.checkTool("git", "check-tree and verify degraded without git")
	for _, probe := range host.tail {
		probe(ctx, st)
	}
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

func checkGhCLI(ctx context.Context, st *state) {
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

func checkGhAuth(ctx context.Context, st *state) {
	if _, _, ok := coreexec.Gh(ctx, st.commander(), "auth", "status"); !ok {
		st.add("gh-auth", severityFail, false, "run gh auth login")
		return
	}
	st.add("gh-auth", severityFail, true, "authenticated")
}

// checkGlabCLI proves the GitLab CLI the fetch path shells is installed.
func checkGlabCLI(ctx context.Context, st *state) {
	if _, err := exec.LookPath(binGlab); err != nil {
		st.add("glab-cli", severityFail, false, "missing — install gitlab.com/gitlab-org/cli (glab)")
		return
	}
	version := ""
	if stdout, _, ok := coreexec.Host(ctx, st.commander(), binGlab, "--version"); ok {
		version = firstLine(stdout)
	}
	st.add("glab-cli", severityFail, true, version)
}

func checkGlabAuth(ctx context.Context, st *state) {
	if _, _, ok := coreexec.Host(ctx, st.commander(), binGlab, "auth", "status"); !ok {
		st.add("glab-auth", severityFail, false, "run glab auth login")
		return
	}
	st.add("glab-auth", severityFail, true, "authenticated")
}

// checkCafeCLI proves the gitcafe CLI the fetch path shells is installed. It
// has no auth probe: gitcafe reads carry no credential the fetch path gates on.
func checkCafeCLI(_ context.Context, st *state) {
	if _, err := exec.LookPath(binCafe); err != nil {
		st.add("cafe-cli", severityFail, false, "missing — install the gitcafe CLI (cafe)")
		return
	}
	st.add("cafe-cli", severityFail, true, "present")
}

// checkCurl proves the HTTP client the Forgejo and Bitbucket reads shell.
func checkCurl(_ context.Context, st *state) {
	if _, err := exec.LookPath(binCurl); err != nil {
		st.add(binCurl, severityFail, false, "missing — install curl (forgejo and bitbucket PR reads shell it)")
		return
	}
	st.add(binCurl, severityFail, true, "present")
}

// checkAzCLI proves the Azure CLI the fetch path shells.
func checkAzCLI(_ context.Context, st *state) {
	if _, err := exec.LookPath(binAz); err != nil {
		st.add(binAz, severityFail, false, "missing — install the Azure CLI (az)")
		return
	}
	st.add(binAz, severityFail, true, "present")
}

// checkForgejoAuth reports whether the pair forgejoAuthFor gates on is set.
func checkForgejoAuth(_ context.Context, st *state) {
	missing := st.missingEnv(envForgejoHost, envForgejoToken)
	if len(missing) > 0 {
		st.add("forgejo-auth", severityFail, false, "missing "+strings.Join(missing, ", ")+
			" — forgejo fetch needs both (an app token with repo read)")
		return
	}
	st.add("forgejo-auth", severityFail, true, "FORGEJO_HOST and FORGEJO_TOKEN set")
}

// checkBitbucketAuth reports whether either credential shape bitbucketAuthFor
// accepts is set: a Bearer token, or a username and password pair.
func checkBitbucketAuth(_ context.Context, st *state) {
	if st.envSet(envBitbucketToken) {
		st.add("bitbucket-auth", severityFail, true, "BITBUCKET_TOKEN set")
		return
	}
	missing := st.missingEnv(envBitbucketUser, envBitbucketPass)
	if len(missing) > 0 {
		st.add("bitbucket-auth", severityFail, false, "set BITBUCKET_TOKEN or "+
			strings.Join(missing, "+")+" — bitbucket needs one of the two")
		return
	}
	st.add("bitbucket-auth", severityFail, true, "BITBUCKET_USERNAME and BITBUCKET_PASSWORD set")
}

// checkAzureAuth reports the credential the Azure fetch path resolves: an
// organization plus either the PAT or an az login that supplies one, so an
// unset PAT warns instead of failing a machine that authenticates through az.
func checkAzureAuth(_ context.Context, st *state) {
	if !st.envSet(envAzureOrg) {
		st.add("azure-auth", severityFail, false,
			"missing AZURE_DEVOPS_ORG=https://dev.azure.com/{org} — azure fetch reads it first")
		return
	}
	if !st.envSet(envAzurePAT) {
		st.add("azure-auth", severityWarn, false,
			"AZURE_DEVOPS_EXT_PAT unset — azure fetch then needs an az login with token access")
		return
	}
	st.add("azure-auth", severityFail, true, "AZURE_DEVOPS_ORG and AZURE_DEVOPS_EXT_PAT set")
}

func checkTokenScopes(ctx context.Context, st *state) {
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
	if scopes != "" {
		st.checkTokenExcess(scopes)
	}
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

func checkGitHubReachable(ctx context.Context, st *state) {
	stdout, _, ok := coreexec.Gh(ctx, st.commander(), "api", "rate_limit", "--jq", ".resources.core.remaining")
	remaining := ""
	if ok {
		remaining = strings.TrimRight(stdout, newline)
	}
	if remaining != "" {
		st.add(checkAPIReachable, severityWarn, true, "core quota remaining: "+remaining)
		return
	}
	st.add(checkAPIReachable, severityWarn, false, "cannot reach api.github.com with this token")
}

// checkGitLabReachable proves the token can read the GitLab API, which is what
// every glab-backed fetch starts from.
func checkGitLabReachable(ctx context.Context, st *state) {
	if _, _, ok := coreexec.Host(ctx, st.commander(), binGlab, "api", "user"); !ok {
		st.add(checkAPIReachable, severityWarn, false, "cannot reach the GitLab API with this token")
		return
	}
	st.add(checkAPIReachable, severityWarn, true, "GitLab API reachable")
}

// envSet reports whether a credential variable carries a value, never what it
// carries.
func (st *state) envSet(name string) bool {
	if st.req.Runtime == nil {
		return false
	}
	return st.req.Env[name] != ""
}

// missingEnv lists the named variables that are unset, in the order given.
func (st *state) missingEnv(names ...string) []string {
	var missing []string
	for _, name := range names {
		if !st.envSet(name) {
			missing = append(missing, name)
		}
	}
	return missing
}

func (st *state) checkStorage() {
	state := readcommon.DisplayPath(st.req.RepoRootPath(), readcommon.StateDir(st.req.RepoRootPath()))
	if stat, err := os.Stat(state); err == nil && stat.IsDir() {
		if writable(state) {
			st.add(checkStorageWritable, severityFail, true, state+"/ writable")
		} else {
			st.add(checkStorageWritable, severityFail, false,
				state+"/ not writable — fetch/record cannot write bundles here")
		}
		return
	}
	if writable(".") {
		st.add(checkStorageWritable, severityFail, true, "cwd writable — "+state+"/ created on first fetch")
		return
	}
	st.add(checkStorageWritable, severityFail, false, "cwd not writable — fetch/record cannot create "+state+"/ here")
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
