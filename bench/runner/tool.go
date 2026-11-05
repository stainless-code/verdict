package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/jq"
)

// grader is the shipped binary's scoring surface: the corpus verbs the runbook
// drives by hand, plus the prompt dump the agent prompt is built from.
//
// Every number the report publishes comes back through it, so a runner change
// cannot quietly invent a rate.
type grader interface {
	// promptPrefix renders the trusted half of an agent prompt: the shipped
	// prompt files and the tool-generated trust boundary, cut at the first
	// fence so a case can append its own. The nonce is the run's.
	promptPrefix(ctx context.Context) (prefix, nonce string, err error)
	// record validates one bundle's review.json and writes findings.json,
	// returning the warnings the tool reported.
	record(ctx context.Context, slug string) ([]string, error)
	// dryRun validates one bundle without writing and preserves raw streams.
	dryRun(ctx context.Context, slug string) (DryRunResult, error)
	// calibrate scores one bundle against the case manifest and returns the
	// envelope's data block verbatim.
	calibrate(ctx context.Context, slug string) (json.RawMessage, error)
	// trend returns the aggregate data.corpus block for every corpus bundle
	// under .review.
	trend(ctx context.Context) (json.RawMessage, error)
}

// envelope is the published JSON envelope every corpus verb writes: data on
// success, a typed error on refusal, warnings either way.
type envelope struct {
	Error    *envelopeError  `json:"error"`
	Data     json.RawMessage `json:"data"`
	Warnings []string        `json:"warnings"`
}

type envelopeError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// tool drives the shipped binary as a child process.
type tool struct {
	bin  string
	repo string
	root string
	work string
}

// promptPrefix renders the trusted half once per run, from a scratch
// repository the binary needs for a bundle.
func (t tool) promptPrefix(ctx context.Context) (string, string, error) {
	dir := filepath.Join(t.work, prefixDir)
	if err := seedPrefixRepo(ctx, t.root, dir); err != nil {
		return "", "", err
	}
	dump, err := inDir(dir, func() (string, error) { return t.renderDump(ctx) })
	if err != nil {
		return "", "", err
	}
	return splitTrustedPrefix(dump)
}

// renderDump asks the binary for the prompt it would send: the four prompt
// files verbatim, then the trust boundary naming a fresh nonce.
func (t tool) renderDump(ctx context.Context) (string, error) {
	env, err := t.json(ctx, "fetch-local", t.repo, prefixName, "--show-prompts")
	if err != nil {
		return "", err
	}
	var data struct {
		Dump string `json:"prompt_dump"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		return "", jq.NewErrf("fetch-local: prompt_dump: %s", err)
	}
	if data.Dump == "" {
		return "", jq.NewErr("fetch-local: prompt_dump is empty")
	}
	return data.Dump, nil
}

// seedPrefixRepo writes the shipped prompt files into a throwaway repository
// with one tracked change, which is all `fetch-local` needs to render a dump.
func seedPrefixRepo(ctx context.Context, root, dir string) error {
	if err := copyPrompts(root, dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return jq.NewErrf("prompt prefix: %s", err)
	}
	if err := atomic.WriteFile(filepath.Join(dir, prefixScaffold), []byte("base\n")); err != nil {
		return jq.NewErrf("prompt prefix: %s", err)
	}
	git := gitIn(dir)
	for _, argv := range [][]string{
		{"init", "-q"},
		{"add", "-A"},
		{"commit", "-qm", "prefix"},
	} {
		if err := git(ctx, argv...); err != nil {
			return err
		}
	}
	if err := atomic.WriteFile(filepath.Join(dir, prefixScaffold), []byte("base\nchange\n")); err != nil {
		return jq.NewErrf("prompt prefix: %s", err)
	}
	return nil
}

const (
	gitEmailName   = "bench@example.com"
	gitDisplayName = "verdict bench"
)

// gitIn returns a git runner scoped to dir, with an identity and no signing so
// an operator's global config cannot break the scratch commit.
func gitIn(dir string) func(context.Context, ...string) error {
	return func(ctx context.Context, argv ...string) error {
		full := append([]string{
			"-C", dir,
			"-c", "user.email=" + gitEmailName,
			"-c", "user.name=" + gitDisplayName,
			"-c", "commit.gpgsign=false",
		}, argv...)
		res, err := coreexec.Runner{}.Run(ctx, core.Command{Argv: append([]string{"git"}, full...), Env: toolEnv()})
		if err != nil {
			return jq.NewErrf("git %s: %s", argv[0], err)
		}
		if res.ExitCode != 0 {
			return jq.NewErrf("git %s: exit %d (stderr: %s)", argv[0], res.ExitCode, excerpt(res.Stderr))
		}
		return nil
	}
}

// inDir runs fn with the process working directory moved to dir: the prompt
// renderer reads its prompt files from there and a child process inherits it.
func inDir[T any](dir string, fn func() (T, error)) (T, error) {
	var zero T
	wd, err := os.Getwd()
	if err != nil {
		return zero, jq.NewErrf("chdir: %s", err)
	}
	if err := os.Chdir(dir); err != nil {
		return zero, jq.NewErrf("chdir %s: %s", dir, err)
	}
	out, fnErr := fn()
	if err := os.Chdir(wd); err != nil {
		return zero, jq.NewErrf("chdir back: %s", err)
	}
	return out, fnErr
}

func (t tool) record(ctx context.Context, slug string) ([]string, error) {
	env, err := t.json(ctx, "record", t.repo, slug)
	return env.Warnings, err
}

func (t tool) calibrate(ctx context.Context, slug string) (json.RawMessage, error) {
	env, err := t.json(ctx, "calibrate", t.repo, slug)
	if err != nil {
		return nil, err
	}
	if len(env.Data) == 0 {
		return nil, jq.NewErr("calibrate: envelope carries no data")
	}
	return env.Data, nil
}

func (t tool) trend(ctx context.Context) (json.RawMessage, error) {
	env, err := t.json(ctx, "trend", t.repo)
	if err != nil {
		return nil, err
	}
	var data struct {
		Corpus json.RawMessage `json:"corpus"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		return nil, jq.NewErrf("trend: data: %s", err)
	}
	return data.Corpus, nil
}

// json runs one --json verb and decodes its envelope. A non-zero exit, a
// typed refusal, or an unreadable envelope is an error: the runner reports a
// refusal as the case's failure instead of scoring around it.
func (t tool) json(ctx context.Context, argv ...string) (envelope, error) {
	full := append([]string{t.bin}, argv...)
	full = append(full, "--json")
	res, err := coreexec.Runner{}.Run(ctx, core.Command{Argv: full, Env: toolEnv()})
	if err != nil {
		return envelope{}, jq.NewErrf("%s: %s", argv[0], err)
	}
	var env envelope
	if len(res.Stdout) > 0 {
		if err := json.Unmarshal([]byte(res.Stdout), &env); err != nil {
			return envelope{}, jq.NewErrf("%s: unreadable envelope: %s (stderr: %s)", argv[0], err, excerpt(res.Stderr))
		}
	}
	if env.Error != nil {
		return env, jq.NewErrf("%s: %s: %s", argv[0], env.Error.Code, env.Error.Message)
	}
	if res.ExitCode != 0 {
		return env, jq.NewErrf("%s: exit %d (stderr: %s)", argv[0], res.ExitCode, excerpt(res.Stderr))
	}
	return env, nil
}

// toolEnv is the environment for the shipped binary: the operator's PATH and
// HOME, minus every knob that could change a run, so the same corpus grades
// the same way on every machine.
func toolEnv() []string {
	return withoutPrefixes([]string{
		"REVIEW_", "GH_", "GITHUB_", "VERDICT_BENCH_", "BENCH_", "GIT_",
	})
}

// withoutPrefixes filters the inherited environment: GIT_DIR and the review
// knobs would otherwise redirect git or reshape a scoring run from the
// operator's shell.
func withoutPrefixes(drop []string) []string {
	env := os.Environ()
	kept := make([]string, 0, len(env))
	for _, kv := range env {
		name, _, ok := strings.Cut(kv, "=")
		if ok && hasAnyPrefix(name, drop) {
			continue
		}
		kept = append(kept, kv)
	}
	return kept
}

func hasAnyPrefix(name string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// excerpt keeps a child's error stream readable inside one report line.
func excerpt(s string) string {
	s = strings.TrimSpace(strings.Join(strings.Fields(s), " "))
	const limit = 240
	if len(s) > limit {
		return s[:limit] + "…"
	}
	if s == "" {
		return "none"
	}
	return s
}
