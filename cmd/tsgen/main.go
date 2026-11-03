// Command tsgen writes (or checks) the TypeScript client's Go-derived
// sources: clients/ts/src/refusal-codes.gen.ts is rendered from the refusal
// hint table in internal/core, so a code added to a hintRules row cannot be
// missed by a source scrape. Run it through `make clients-ts` (check) or
// `make tsgen` (write); never edit the generated file by hand.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/schemagen"
)

// cliPrefix names the command in every user-facing failure.
const cliPrefix = "tsgen:"

// codesRelPath is the generated file, relative to the repository root.
const codesRelPath = "clients/ts/src/refusal-codes.gen.ts"

// options are the parsed command-line choices.
type options struct {
	root  string
	write bool
}

// cliError is the command's sealed error type; it never leaks fmt/errors
// values across the run boundary. The cause comes first so the GC scans the
// fewest pointer bytes (govet fieldalignment).
type cliError struct {
	cause error
	msg   string
}

// Error implements error.
func (e *cliError) Error() string { return e.msg }

// Unwrap exposes the cause to [errors.Is]/[errors.As].
func (e *cliError) Unwrap() error { return e.cause }

// errorf builds a command error without a prefix (run adds it once).
func errorf(format string, args ...any) error {
	if len(args) == 0 {
		return &cliError{msg: format}
	}
	return &cliError{msg: fmt.Sprintf(format, args...)}
}

// wrapError annotates a cause and keeps the chain.
func wrapError(prefix string, cause error) error {
	return &cliError{msg: prefix + ": " + cause.Error(), cause: cause}
}

func main() {
	//nolint:forbidigo // GO-LAYOUT-02: main is the only os.Exit caller by design
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run executes one tsgen invocation and returns its exit code.
func run(args []string, stdout, stderr io.Writer) int {
	opts, err := parseFlags(args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		fail(stderr, err)
		return 2
	}
	if err := execute(opts, stdout); err != nil {
		fail(stderr, err)
		return 1
	}
	return 0
}

// fail prints one prefixed command failure.
func fail(stderr io.Writer, err error) {
	fmt.Fprintln(stderr, cliPrefix, err)
}

// execute renders the generated sources, then writes or checks them.
func execute(opts options, stdout io.Writer) error {
	codes := core.ExactRefusalCodes()
	if len(codes) == 0 {
		return errorf("the refusal hint table carries no exact codes")
	}
	path := filepath.Join(opts.root, filepath.FromSlash(codesRelPath))
	data := renderRefusalCodes(codes)
	if opts.write {
		if err := atomic.WriteFile(path, data); err != nil {
			return wrapError("write "+path, err)
		}
		fmt.Fprintf(stdout, "%s wrote %s\n", cliPrefix, codesRelPath)
		return nil
	}
	have, err := os.ReadFile(path)
	if err != nil {
		return wrapError("read "+path, err)
	}
	if bytes.Equal(have, data) {
		fmt.Fprintf(stdout, "%s %s is current\n", cliPrefix, codesRelPath)
		return nil
	}
	return errorf("%s is stale; run `make tsgen` from the repo root", codesRelPath)
}

// parseFlags parses the command-line flags without exiting the process.
func parseFlags(args []string, stderr io.Writer) (options, error) {
	fs := flag.NewFlagSet("tsgen", flag.ContinueOnError)
	fs.SetOutput(stderr)
	write := fs.Bool("write", false, "rewrite the generated client sources instead of checking them")
	root := fs.String("root", "", "repository root (default: discovered from the working directory)")
	if err := fs.Parse(args); err != nil {
		return options{}, wrapError("parse flags", err)
	}
	if fs.NArg() != 0 {
		return options{}, errorf("usage: tsgen [-write] [-root dir]")
	}
	if *root != "" {
		return options{root: *root, write: *write}, nil
	}
	found, err := schemagen.RepoRoot()
	if err != nil {
		return options{}, wrapError("locate repository root", err)
	}
	return options{root: found, write: *write}, nil
}

// renderRefusalCodes renders refusal-codes.gen.ts for the given exact codes.
// The union stays open at the use site (`| (string & {})` in errors.ts)
// because the prefix families accept any suffix.
func renderRefusalCodes(codes []string) []byte {
	var out strings.Builder
	out.WriteString("// GENERATED from internal/core/hint.go hintRules by cmd/tsgen. Do not edit.\n")
	out.WriteString("// Open families (missing-*, bad-*) accept any suffix; keep this union\n")
	out.WriteString("// exact and widen at the use site with ` | (string & {})`.\n")
	out.WriteString("export type RefusalCode =\n")
	for _, code := range codes {
		out.WriteString("  | " + strconv.Quote(code) + "\n")
	}
	return []byte(strings.TrimSuffix(out.String(), "\n") + ";\n")
}
