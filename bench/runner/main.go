// Command bench runs the review corpus benchmark: one agent, every checked-in
// case, scored by the shipped binary.
//
// It is a dev tool. Nothing under bench/ is compiled into the shipped binary,
// which is why `make size` does not move.
//
//	go run ./bench/runner -model <id> -agent '<command>' [-runs 3] [-panel p]
//	    [-cases a,b] [-internal] [-out path] [-workdir dir] [-timeout 15m]
//
// The agent command runs under sh(1) with the run dir as its working directory;
// $VERDICT_BENCH_PROMPT names the prompt to read, $VERDICT_BENCH_OUT the
// review to write, and $VERDICT_BENCH_COST an optional spend record. The
// operator runbook is docs/benchmark.md.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/stainless-code/verdict/internal/jq"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "bench:", err)
		os.Exit(1) //nolint:forbidigo // a command's exit status is its only failure channel.
	}
}

func run() error {
	cfg, err := parseFlags(os.Args[1:])
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	_, err = Run(ctx, &cfg)
	return err
}

func parseFlags(args []string) (Config, error) {
	fs := flag.NewFlagSet("bench", flag.ContinueOnError)
	var (
		cfg   Config
		cases string
		panel string
		root  string
	)
	fs.StringVar(&cfg.Model, "model", "", "model id every bundle is attributed to (required)")
	fs.StringVar(&cfg.Agent, "agent", "", "agent command line, run with sh -c per sample (required)")
	fs.StringVar(&panel, "panel", "", "panel the samples run under (default bench-<model>)")
	fs.StringVar(&cases, "cases", "", "comma-separated case ids (default: the whole corpus)")
	fs.StringVar(&cfg.Repo, "repo", benchRepo, "owner/repo label the corpus verbs take")
	fs.StringVar(&root, "root", "", "checkout to read the corpus, prompts, and binary from (default: the module root)")
	fs.StringVar(&cfg.Verdict, "verdict", "", "path to the built binary (default <root>/bin/verdict)")
	fs.StringVar(&cfg.Corpus, "corpus", "", "corpus directory (default <root>/bench/corpus)")
	fs.StringVar(&cfg.WorkDir, "workdir", "", "run directory; must be empty or absent (default: a fresh temp dir)")
	fs.StringVar(&cfg.Out, "out", "", "publication JSON (default <root>/bench/results[/pilot]/<run-id>.json)")
	fs.BoolVar(&cfg.Internal, "internal", false, "mark the run an internal pilot: never citable, kept out of bench/results")
	fs.IntVar(&cfg.Runs, "runs", 1, "samples per case")
	fs.DurationVar(&cfg.Timeout, "timeout", defaultTimeout, "budget for one sample")
	if err := fs.Parse(args); err != nil {
		return Config{}, jq.NewErrf("flags: %s", err)
	}
	if fs.NArg() > 0 {
		return Config{}, jq.NewErrf("unexpected argument %q", fs.Arg(0))
	}
	cfg.Cases = splitCSV(cases)
	cfg.Panel = splitCSV(panel)
	cfg.Summary = os.Stdout
	if root != "" {
		cfg.Root = root
		return cfg, cfg.validate()
	}
	discovered, err := moduleRoot()
	if err != nil {
		return Config{}, err
	}
	cfg.Root = discovered
	return cfg, cfg.validate()
}

func splitCSV(s string) []string {
	var out []string
	for part := range strings.SplitSeq(s, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// moduleRoot finds the checkout: the runner reads the corpus, the prompt files,
// and the built binary from it.
func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", jq.NewErrf("root: %s", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", jq.NewErr("root: no go.mod above the working directory; pass -root")
		}
		dir = parent
	}
}
