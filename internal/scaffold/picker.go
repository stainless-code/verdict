package scaffold

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/agentcatalog"
	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	initcore "github.com/stainless-code/verdict/internal/core/init"
	"github.com/stainless-code/verdict/internal/gitcmd"
	"github.com/stainless-code/verdict/internal/readcommon"
)

const (
	// defaultPreset is the preset a bare `verdict init` has always written, and
	// the picker's own default: Enter keeps today's behavior.
	defaultPreset = "standard"
	// defaultAgentName is the agent a --ci run wires when no agent is named. It
	// is the row a fresh checkout most often has installed, and it is the
	// default the picker offers when PATH answers nothing.
	defaultAgentName = "claude"
	// ciNone is the picker's "no workflow" answer. It is not a --ci value: the
	// flag has no such spelling, because naming --ci is already a choice to
	// write one.
	ciNone = "none"
	// pickerIntro opens the session: one line, so the operator knows how many
	// answers are wanted and that Enter is always safe.
	pickerIntro = "verdict init: three questions, Enter keeps the default.\n"
	// presetLabel, workflowLabel, triggerLabel, and agentLabel are the question
	// texts: the same words the manual uses for the same three choices.
	presetLabel   = "preset"
	workflowLabel = "ci workflow"
	triggerLabel  = "/verdict comment re-run?"
	agentLabel    = "agent"
)

// Picker is the guided `verdict init` session: the stream the answers come
// from, the stream the questions go to, whether both endpoints are terminals,
// and the two probes its defaults read. It resolves flags, not files — every
// answer lands in the same `flags` a `--preset`, `--ci`, `--comment-trigger`,
// or `--agent` argv populates, so a picked run writes the bytes the explicit
// invocation writes. The shipped CLI fills the seam from the real terminal; a
// test scripts every field, so grading needs neither a pseudo-terminal nor a
// network.
type Picker struct {
	// In answers the questions, one line each.
	In io.Reader
	// Out receives the questions: the diagnostic stream, so init's human output
	// on stdout never interleaves with a prompt.
	Out io.Writer
	// LookPath probes PATH for one agent CLI; nil finds nothing.
	LookPath func(string) (string, error)
	// Remote answers the checkout's origin URL; nil means no host detection.
	Remote func(context.Context) (string, error)
	// StdinTTY and StdoutTTY report whether the streams are terminals. Both must
	// be: a piped answer would block a script, and a redirected stdout is a
	// machine reading the plan, not an operator answering it.
	StdinTTY  bool
	StdoutTTY bool
}

// pickIfInteractive runs the guided picker when the invocation is bare and both
// streams are terminals, and answers the flags it resolved. A run that named
// any selection flag, set --json, or has a redirected stream never prompts: CI,
// evals, and scripts keep the non-interactive behavior they already had.
func pickIfInteractive(ctx context.Context, r *cli.Runner, req *readcommon.Request, picker *Picker, f *flags) (int, bool) {
	if f.given || r.JSONMode {
		return 0, false
	}
	if picker == nil {
		picker = productionPicker(r, req)
	}
	if !picker.StdinTTY || !picker.StdoutTTY {
		return 0, false
	}
	picked, err := pickFlags(ctx, picker)
	if err != nil {
		return pickerFailed(ctx, r)
	}
	*f = picked
	return 0, false
}

// pickerFailed maps a picker error onto the process contract. A canceled
// context is the CLI's frozen cancel refusal (exit 3, nothing written), so a
// Ctrl-C during a prompt ends the run the moment it is pressed instead of
// waiting for the terminal to see a newline. Every other picker error is
// [errPickAborted] — the picker answers nothing else — so it aborts before the
// service is called and writes nothing.
func pickerFailed(ctx context.Context, r *cli.Runner) (int, bool) {
	if ctx.Err() != nil {
		return r.Canceled(ctx, "init")
	}
	fmt.Fprint(r.Diag, "init: aborted (nothing written)\n")
	return cli.ExitRefused, true
}

// pickAbortedError is the end-of-input sentinel's own type: the picker returns
// its error, never a wrapped stdlib one.
type pickAbortedError string

// Error is the sentinel's fixed text.
func (e pickAbortedError) Error() string { return string(e) }

// errPickAborted is the operator closing the input before the last answer
// (Ctrl-D): the session ends and the run writes nothing.
var errPickAborted error = pickAbortedError("init picker: input closed")

// pickFlags asks the choices a bare init leaves open and answers the flags they
// resolve. An error is [errPickAborted] at end of input or the context's own
// error, and either one aborts before anything is written.
func pickFlags(ctx context.Context, p *Picker) (flags, error) {
	ask := &asker{in: bufio.NewReader(p.In), out: p.Out}
	seen := detect(ctx, p)
	fmt.Fprint(ask.out, pickerIntro)
	f := flags{preset: defaultPreset}
	if err := askPreset(ctx, ask, &f); err != nil {
		return f, err
	}
	if err := askSetup(ctx, ask, seen, &f); err != nil {
		return f, err
	}
	if ref := scopedFlagError(&f); ref != nil {
		// Unreachable by construction: the picker offers only values lookup
		// accepts. Answering as an abort keeps the resolved flags valid by the
		// same rule argv is, instead of writing a plan the CLI would refuse.
		return flags{preset: defaultPreset}, errPickAborted
	}
	return f, nil
}

// askPreset asks the style preset.
func askPreset(ctx context.Context, ask *asker, f *flags) error {
	preset, err := ask.value(ctx, presetLabel, defaultPreset, initcore.PresetValues())
	if err != nil {
		return err
	}
	f.preset = preset
	return nil
}

// askSetup asks the workflow and, for a run that writes one, its GitHub comment
// trigger and the agent it launches.
func askSetup(ctx context.Context, ask *asker, seen detection, f *flags) error {
	ci, err := askWorkflow(ctx, ask, seen)
	if err != nil {
		return err
	}
	f.ci = ci
	if ci == "" {
		return nil
	}
	if ci == providerGithub {
		if err := askTrigger(ctx, ask, f); err != nil {
			return err
		}
	}
	return askAgent(ctx, ask, seen, f)
}

// askWorkflow asks which workflow to write, defaulted to the host the origin
// remote names. It answers the resolved --ci value, or "" for no workflow.
func askWorkflow(ctx context.Context, ask *asker, seen detection) (string, error) {
	values := append([]string{ciNone}, initcore.ProviderNames()...)
	choice, err := ask.value(ctx, workflowLabel, seen.ciDefault(), values)
	if err != nil {
		return "", err
	}
	if choice == ciNone {
		return "", nil
	}
	return choice, nil
}

// askTrigger asks whether the GitHub workflow carries the opt-in `/verdict`
// comment re-run. The job gate skips it by default, so Enter keeps it off.
func askTrigger(ctx context.Context, ask *asker, f *flags) error {
	choice, err := ask.value(ctx, triggerLabel, "n", []string{"y", "n"})
	if err != nil {
		return err
	}
	f.commentTrigger = choice == "y"
	return nil
}

// askAgent asks which agent the generated workflow launches, defaulted to the
// first installed CLI the catalog knows. Nothing installed leaves the built-in
// default in place, which is what the flag's own absence means.
func askAgent(ctx context.Context, ask *asker, seen detection, f *flags) error {
	if len(seen.agents) == 0 {
		return nil
	}
	choice, err := ask.value(ctx, agentLabel, seen.agents[0], seen.agents)
	if err != nil {
		return err
	}
	f.agent = choice
	return nil
}

// asker is one prompt session: a line reader over the operator's input and the
// stream the questions are written to.
type asker struct {
	in  *bufio.Reader
	out io.Writer
}

// value asks one question: Enter keeps the default, a listed value is accepted,
// and anything else re-asks with the accepted set.
func (a *asker) value(ctx context.Context, label, def string, values []string) (string, error) {
	for {
		answer, err := a.ask(ctx, label, def, values)
		if err != nil {
			return "", err
		}
		if answer == "" {
			return def, nil
		}
		if slices.Contains(values, answer) {
			return answer, nil
		}
		a.reask(values, def)
	}
}

// ask writes one prompt and reads its trimmed answer.
func (a *asker) ask(ctx context.Context, label, def string, values []string) (string, error) {
	fmt.Fprintf(a.out, "%s (%s) [%s]: ", label, strings.Join(values, "|"), def)
	line, err := a.line(ctx)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// reask explains the accepted set after an answer the picker cannot use.
func (a *asker) reask(values []string, def string) {
	fmt.Fprintf(a.out, "choose one of %s, or press Enter for %s\n",
		strings.Join(values, "|"), def)
}

// line reads one answer, trimming the newline a terminal sends (CRLF on
// Windows). A closed input is [errPickAborted] — Ctrl-D ends the picker instead
// of looping on an empty read — and a canceled context wins over both, because
// a terminal read has no deadline of its own: the process-wide SIGINT context
// must end the prompt the moment the operator presses Ctrl-C.
//
// The read runs in its own goroutine so a pending terminal read can be
// abandoned. The answer channel is buffered, so that goroutine always has
// somewhere to send and exits as soon as the stream yields; only the canceled
// path can leave it inside the read, and there the process exits at once (exit
// 3), before anything else touches the stream.
func (a *asker) line(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	type answer struct {
		err  error
		line string
	}
	answers := make(chan answer, 1)
	go func() {
		line, err := a.in.ReadString('\n')
		answers <- answer{line: line, err: err}
	}()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case got := <-answers:
		if got.line == "" && got.err != nil {
			return "", errPickAborted
		}
		return strings.TrimRight(got.line, "\r\n"), nil
	}
}

// detection is what the picker learns about this checkout and machine before it
// asks anything: the host the origin remote names and the CLIs on PATH.
type detection struct {
	host   string
	agents []string
}

// detect answers the defaults: the origin remote's host, and the launchable
// catalog rows whose binary answers on PATH.
func detect(ctx context.Context, p *Picker) detection {
	var seen detection
	if p.Remote != nil {
		if remote, err := p.Remote(ctx); err == nil {
			seen.host = hostFromRemote(remote)
		}
	}
	seen.agents = installedAgents(p.LookPath)
	return seen
}

// ciDefault is the workflow the picker proposes: the detected host when the
// origin remote names one verdict can write for, else no workflow at all.
func (d detection) ciDefault() string {
	if _, ok := initcore.LookupCI(d.host); ok {
		return d.host
	}
	return ciNone
}

// installedAgents lists the launchable catalog rows whose binary answers on
// PATH, in catalog order. The first row is the default; nothing found leaves
// the built-in default agent in place. A row that documents no key variable is
// not offered: the picker asks no secret name, so a workflow naming that agent
// would refuse to render without a --key-secret the session never collects.
func installedAgents(look func(string) (string, error)) []string {
	if look == nil {
		return nil
	}
	var found []string
	for _, row := range agentcatalog.All() {
		if row.Launch == nil || row.Launch.Binary == "" || row.Launch.KeyEnv == "" {
			continue
		}
		if _, err := look(row.Launch.Binary); err == nil {
			found = append(found, row.Name)
		}
	}
	return found
}

// hostFromRemote maps a git remote URL to the provider verdict can write for:
// github.com is GitHub, a host naming gitlab is GitLab, and a host naming
// forgejo (or codeberg.org, the hosted Forgejo) is Forgejo. Anything else is no
// detection, and the picker proposes no workflow.
func hostFromRemote(remote string) string {
	// Hosts are case-insensitive, and operators paste them capitalized.
	host := strings.ToLower(remoteHost(strings.TrimSpace(remote)))
	switch {
	case host == "github.com":
		return providerGithub
	case strings.Contains(host, "gitlab"):
		return "gitlab"
	case strings.Contains(host, "forgejo") || host == "codeberg.org":
		return "forgejo"
	default:
		return ""
	}
}

// remoteHost answers the host of a git remote URL in every shape git accepts:
// scheme form, scp form (`git@host:owner/repo`), and a bare `host/owner/repo`.
func remoteHost(remote string) string {
	if rest, ok := cutScheme(remote); ok {
		return cutAuthority(rest)
	}
	if _, after, ok := strings.Cut(remote, "@"); ok {
		host, _, _ := strings.Cut(after, ":")
		return host
	}
	host, _, _ := strings.Cut(remote, "/")
	return host
}

// cutScheme answers the part after `scheme://`.
func cutScheme(remote string) (string, bool) {
	_, rest, ok := strings.Cut(remote, "://")
	return rest, ok
}

// cutAuthority answers the host of a scheme-form URL: the path first (a path
// may hold an `@`), then userinfo, then port. Cutting `@` before `/` would read
// `https://example.com/a@b/c` as host `b`.
func cutAuthority(rest string) string {
	authority, _, _ := strings.Cut(rest, "/")
	if _, after, ok := strings.Cut(authority, "@"); ok {
		authority = after
	}
	host, _, _ := strings.Cut(authority, ":")
	return host
}

// productionPicker is the seam the shipped CLI installs: the real stdin, the
// diagnostic stream for prompts, the two terminal probes, and the PATH and git
// probes.
func productionPicker(r *cli.Runner, req *readcommon.Request) *Picker {
	return &Picker{
		In:        os.Stdin,
		Out:       r.Diag,
		LookPath:  exec.LookPath,
		Remote:    gitRemote(req.RepoRoot),
		StdinTTY:  cli.IsTerminal(os.Stdin),
		StdoutTTY: cli.IsTerminal(os.Stdout),
	}
}

// gitRemote reads the origin URL through the same commander seam the init
// service uses, so the picker spawns nothing on its own and a repository
// without an origin is simply no detection.
func gitRemote(repoRoot string) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		res, err := (coreexec.Runner{OutputCap: coreexec.FetchOutputCap}).Run(ctx, &core.Command{
			Argv: gitcmd.Argv(repoRoot, "remote", "get-url", "origin"), Env: gitcmd.Env(nil),
		})
		if err != nil {
			return "", fmt.Errorf("read the origin remote: %w", err)
		}
		if res.ExitCode != 0 {
			return "", res.ExitFailure("git")
		}
		return res.Stdout, nil
	}
}
